package provisioning

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
)

const (
	CreatePermission   = "platform.tenants.provision"
	ActivatePermission = "platform.tenants.activate"
)

var (
	ErrConflict    = errors.New("provisioning state conflict")
	ErrKeyConflict = errors.New("idempotency conflict")
	ErrNotFound    = errors.New("provisioning target unavailable")
)

type Service struct {
	DB             *pgxpool.Pool
	FingerprintKey []byte
}

type CreateInput struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"owner_contact_email"`
}

type ActivateInput struct {
	Claim     string `json:"owner_claim_id"`
	Method    string `json:"verification_method"`
	Reference string `json:"verification_reference"`
}

type Result struct {
	Tenant       string `json:"tenant_id"`
	Owner        string `json:"owner_id"`
	TenantStatus string `json:"tenant_status"`
	OwnerStatus  string `json:"owner_status"`
}

type Delivery struct{ Email, Code string }

type mutation func(pgx.Tx, string) (Result, Delivery, error)

func (s Service) perform(ctx context.Context, session, key, operation, target, permission string, input any, change mutation) (Result, Delivery, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return Result{}, Delivery{}, err
	}
	mac := hmac.New(sha256.New, s.FingerprintKey)
	mac.Write(payload)
	fingerprint := mac.Sum(nil)
	var actor string
	var result Result
	var delivery Delivery
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var err error
		actor, err = identity.AuthorizePlatform(ctx, tx, session, permission)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.provisioning_receipts (actor_id,operation,target,request_key,fingerprint)
			VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, actor, operation, target, key, fingerprint)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var previous []byte
			err = tx.QueryRow(ctx, `SELECT fingerprint,tenant_id,owner_id FROM hive.provisioning_receipts
				WHERE actor_id=$1 AND operation=$2 AND target=$3 AND request_key=$4`, actor, operation, target, key).
				Scan(&previous, &result.Tenant, &result.Owner)
			if err != nil {
				return err
			}
			if !hmac.Equal(previous, fingerprint) {
				return ErrKeyConflict
			}
			result.TenantStatus, result.OwnerStatus = "pending", "pending"
			if operation == "activate" {
				result.TenantStatus, result.OwnerStatus = "active", "active"
			}
			return nil
		}
		result, delivery, err = change(tx, actor)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.provisioning_receipts SET tenant_id=$5,owner_id=$6
			WHERE actor_id=$1 AND operation=$2 AND target=$3 AND request_key=$4`, actor, operation, target, key, result.Tenant, result.Owner)
		return err
	})
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrUnauthenticated) ||
			errors.Is(err, ErrConflict) || errors.Is(err, ErrKeyConflict) || errors.Is(err, ErrNotFound) {
			auditErr := audit.RecordProvisioning(ctx, s.DB, audit.ProvisioningEvent{Actor: actor, Action: operation, Outcome: "denied"})
			if auditErr != nil {
				return Result{}, Delivery{}, auditErr
			}
		}
		return Result{}, Delivery{}, err
	}
	return result, delivery, nil
}

func issue(ctx context.Context, tx pgx.Tx, tenant, owner string) (string, error) {
	code := rand.Text()
	_, err := tx.Exec(ctx, `INSERT INTO hive.owner_invitations (tenant_id,owner_id,digest,expires_at)
		VALUES ($1,$2,$3,clock_timestamp()+interval '24 hours')
		ON CONFLICT (tenant_id) DO UPDATE SET generation=gen_random_uuid(),digest=EXCLUDED.digest,
		expires_at=EXCLUDED.expires_at,claimed_at=NULL`, tenant, owner, identity.Digest(code))
	return code, err
}

func (s Service) Create(ctx context.Context, session, key string, input CreateInput) (Result, Delivery, error) {
	return s.perform(ctx, session, key, "create", "", CreatePermission, input, func(tx pgx.Tx, actor string) (Result, Delivery, error) {
		result := Result{TenantStatus: "pending", OwnerStatus: "pending"}
		err := tx.QueryRow(ctx, `INSERT INTO hive.tenants (display_name,status) VALUES ($1,'pending') RETURNING id`, input.DisplayName).Scan(&result.Tenant)
		if err != nil {
			return Result{}, Delivery{}, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO hive.owner_candidates (tenant_id,contact_email) VALUES ($1,$2) RETURNING id`, result.Tenant, input.Email).Scan(&result.Owner)
		if err != nil {
			return Result{}, Delivery{}, err
		}
		code, err := issue(ctx, tx, result.Tenant, result.Owner)
		if err != nil {
			return Result{}, Delivery{}, err
		}
		err = audit.RecordProvisioning(ctx, tx, audit.ProvisioningEvent{Actor: actor, Tenant: result.Tenant, Action: "create", Outcome: "succeeded"})
		return result, Delivery{Email: input.Email, Code: code}, err
	})
}

func lockPending(ctx context.Context, tx pgx.Tx, tenant string) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM hive.tenants WHERE id=$1 FOR UPDATE`, tenant).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "pending" {
		return ErrConflict
	}
	return nil
}

func (s Service) Reissue(ctx context.Context, session, tenant string) (Delivery, error) {
	var delivery Delivery
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := identity.AuthorizePlatform(ctx, tx, session, CreatePermission)
		if err != nil {
			return err
		}
		if err = lockPending(ctx, tx, tenant); err != nil {
			return err
		}
		var owner string
		err = tx.QueryRow(ctx, `SELECT id,contact_email FROM hive.owner_candidates WHERE tenant_id=$1`, tenant).Scan(&owner, &delivery.Email)
		if err != nil {
			return err
		}
		delivery.Code, err = issue(ctx, tx, tenant, owner)
		if err != nil {
			return err
		}
		return audit.RecordProvisioning(ctx, tx, audit.ProvisioningEvent{Actor: actor, Tenant: tenant, Action: "reissue", Outcome: "succeeded"})
	})
	return delivery, err
}

func (s Service) Invitation(ctx context.Context, code string) (identity.Invitation, error) {
	var invitation identity.Invitation
	err := s.DB.QueryRow(ctx, `SELECT i.tenant_id,i.owner_id,i.generation,i.expires_at FROM hive.owner_invitations i
		JOIN hive.tenants t ON t.id=i.tenant_id
		WHERE i.digest=$1 AND i.claimed_at IS NULL AND i.expires_at>clock_timestamp() AND t.status='pending'`, identity.Digest(code)).
		Scan(&invitation.Tenant, &invitation.Owner, &invitation.Generation, &invitation.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return invitation, ErrNotFound
	}
	return invitation, err
}

func (s Service) Claim(ctx context.Context, tenant, generation string, validUntil time.Time, proof identity.GoogleIdentity) (string, error) {
	var claim string
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := lockPending(ctx, tx, tenant); err != nil {
			return err
		}
		invitation := identity.Invitation{Tenant: tenant, Generation: generation}
		err := tx.QueryRow(ctx, `UPDATE hive.owner_invitations SET claimed_at=clock_timestamp() WHERE tenant_id=$1 AND generation=$2 AND
			claimed_at IS NULL AND expires_at>clock_timestamp() AND $3::timestamptz>clock_timestamp() RETURNING owner_id,expires_at`, tenant, generation, validUntil).
			Scan(&invitation.Owner, &invitation.Expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		claim, err = identity.RecordOwnerClaim(ctx, tx, invitation, proof)
		return err
	})
	return claim, err
}

func (s Service) Activate(ctx context.Context, session, key, tenant string, input ActivateInput) (Result, error) {
	result, _, err := s.perform(ctx, session, key, "activate", tenant, ActivatePermission, input, func(tx pgx.Tx, actor string) (Result, Delivery, error) {
		if err := lockPending(ctx, tx, tenant); err != nil {
			return Result{}, Delivery{}, err
		}
		invitation := identity.Invitation{Tenant: tenant}
		err := tx.QueryRow(ctx, `SELECT owner_id,generation FROM hive.owner_invitations WHERE tenant_id=$1 AND claimed_at IS NOT NULL
			AND expires_at>clock_timestamp()`, tenant).Scan(&invitation.Owner, &invitation.Generation)
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, Delivery{}, ErrConflict
		}
		if err != nil {
			return Result{}, Delivery{}, err
		}
		err = identity.ActivatePrimaryOwner(ctx, tx, invitation, input.Claim)
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, Delivery{}, ErrConflict
		}
		if err != nil {
			return Result{}, Delivery{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.tenants SET status='active' WHERE id=$1`, tenant)
		if err != nil {
			return Result{}, Delivery{}, err
		}
		err = audit.RecordProvisioning(ctx, tx, audit.ProvisioningEvent{Actor: actor, Tenant: tenant, Claim: input.Claim, Action: "activate",
			Outcome: "succeeded", Method: input.Method, Reference: input.Reference})
		return Result{Tenant: tenant, Owner: invitation.Owner, TenantStatus: "active", OwnerStatus: "active"}, Delivery{}, err
	})
	return result, err
}
