package entitlements

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform"
)

var (
	ErrInput    = errors.New("invalid entitlement input")
	ErrMissing  = errors.New("entitlement target unavailable")
	ErrState    = errors.New("entitlement state conflict")
	ErrRevision = errors.New("entitlement revision conflict")
	ErrReplay   = errors.New("entitlement idempotency conflict")
	ErrDenied   = errors.New("entitlement denied")
	reference   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

type Service struct {
	DB             *pgxpool.Pool
	FingerprintKey []byte
}

type Grant struct {
	Effective time.Time `json:"effective_at"`
	Expiry    time.Time `json:"expires_at"`
	Source    string    `json:"source_reference"`
	Revision  *int64    `json:"revision"`
}

type Revoke struct {
	Reason   string `json:"reason_code"`
	Revision *int64 `json:"revision"`
}

type Result struct {
	Tenant          string     `json:"tenant_id"`
	Capability      string     `json:"capability_id"`
	Effective       time.Time  `json:"effective_at"`
	Expiry          time.Time  `json:"expires_at"`
	Source          string     `json:"source"`
	SourceReference string     `json:"source_reference"`
	Revision        int64      `json:"revision"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	State           string     `json:"state"`
}

func grantable(capability string) bool {
	switch capability {
	case "catalog.products", "catalog.pricing", "inventory.receiving", "inventory.repacking",
		"inventory.transfer", "inventory.adjustment", "inventory.stock_count", "inventory.balance", "inventory.traceability",
		"sales.cashier", "sales.transactions", "sales.payments", "sales.history", "sales.receipts", "sales.returns", "sales.voids",
		"reports.dashboard", "reports.sales", "reports.inventory", "reports.stock_movements", "realtime.events", "realtime.notifications":
		return platform.IsPilotCapability(capability)
	default:
		return false
	}
}

func (r *Result) at(now time.Time) {
	r.Effective, r.Expiry = r.Effective.UTC(), r.Expiry.UTC()
	switch {
	case r.RevokedAt != nil:
		utc := r.RevokedAt.UTC()
		r.RevokedAt = &utc
		r.State = "revoked"
	case !now.Before(r.Expiry):
		r.State = "expired"
	case now.Before(r.Effective):
		r.State = "scheduled"
	default:
		r.State = "active"
	}
}

const current = `SELECT effective_at,expires_at,source_reference,revoked_at,revision FROM hive.tenant_entitlements
	WHERE tenant_id=$1 AND capability_id=$2`

func read(ctx context.Context, tx pgx.Tx, tenant, capability, lock string) (Result, error) {
	r := Result{Tenant: tenant, Capability: capability, Source: "manual_platform"}
	err := tx.QueryRow(ctx, current+lock, tenant, capability).Scan(&r.Effective, &r.Expiry, &r.SourceReference, &r.RevokedAt, &r.Revision)
	return r, err
}

func (s Service) Get(ctx context.Context, token, tenant, capability string) (Result, error) {
	if !grantable(capability) {
		return Result{}, ErrInput
	}
	var r Result
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := identity.AuthorizePlatform(ctx, tx, token, "platform.entitlements.read"); err != nil {
			return err
		}
		var err error
		r, err = read(ctx, tx, tenant, capability, "")
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMissing
		}
		if err != nil {
			return err
		}
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		r.at(now)
		return nil
	})
	return r, err
}

func (s Service) Grant(ctx context.Context, token, tenant, capability, key string, input Grant) (Result, error) {
	if input.Revision == nil || *input.Revision < 0 || input.Effective.IsZero() || input.Expiry.IsZero() || !reference.MatchString(input.Source) {
		return Result{}, ErrInput
	}
	input.Effective = input.Effective.UTC().Truncate(time.Microsecond)
	input.Expiry = input.Expiry.UTC().Truncate(time.Microsecond)
	if !input.Effective.Before(input.Expiry) {
		return Result{}, ErrInput
	}
	return s.mutate(ctx, token, tenant, capability, key, "grant", *input.Revision, input, &input, "")
}

func (s Service) Revoke(ctx context.Context, token, tenant, capability, key string, input Revoke) (Result, error) {
	if input.Revision == nil || *input.Revision < 0 || input.Reason != "administrative_revocation" {
		return Result{}, ErrInput
	}
	return s.mutate(ctx, token, tenant, capability, key, "revoke", *input.Revision, input, nil, input.Reason)
}

func (s Service) mutate(ctx context.Context, token, tenant, capability, key, operation string, expected int64, input any, grant *Grant, reason string) (Result, error) {
	if !grantable(capability) {
		return Result{}, ErrInput
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Result{}, err
	}
	mac := hmac.New(sha256.New, s.FingerprintKey)
	mac.Write(payload)
	fingerprint := mac.Sum(nil)
	var r Result
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := identity.AuthorizePlatform(ctx, tx, token, "platform.entitlements."+operation)
		if err != nil {
			return err
		}
		var status string
		query := `SELECT status FROM hive.tenants WHERE id=$1`
		if grant != nil {
			query += " FOR SHARE"
		}
		err = tx.QueryRow(ctx, query, tenant).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMissing
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.tenant_entitlement_receipts
			(tenant_id,capability_id,actor_id,operation,request_key,fingerprint)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, tenant, capability, actor, operation, key, fingerprint)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var previous []byte
			err = tx.QueryRow(ctx, `SELECT fingerprint,result FROM hive.tenant_entitlement_receipts
				WHERE tenant_id=$1 AND capability_id=$2 AND actor_id=$3 AND operation=$4 AND request_key=$5`,
				tenant, capability, actor, operation, key).Scan(&previous, &r)
			if err != nil {
				return err
			}
			if !hmac.Equal(fingerprint, previous) {
				return ErrReplay
			}
			return nil
		}
		r, err = read(ctx, tx, tenant, capability, " FOR UPDATE")
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if r.Revision != expected {
			return ErrRevision
		}
		if grant == nil && r.Revision == 0 {
			return ErrMissing
		}
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if grant != nil {
			if status != "active" || !grant.Expiry.After(now) || (r.Revision > 0 && r.RevokedAt == nil && r.Expiry.After(now)) {
				return ErrState
			}
			tag, err = tx.Exec(ctx, `INSERT INTO hive.tenant_entitlements
				(tenant_id,capability_id,effective_at,expires_at,source_reference,revision)
				VALUES ($1,$2,$3,$4,$5,$6)
				ON CONFLICT (tenant_id,capability_id) DO UPDATE SET effective_at=EXCLUDED.effective_at,
					expires_at=EXCLUDED.expires_at,source_reference=EXCLUDED.source_reference,revoked_at=NULL,revision=EXCLUDED.revision
				WHERE hive.tenant_entitlements.revision=$7`, tenant, capability, grant.Effective, grant.Expiry, grant.Source, expected+1, expected)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrRevision
			}
		} else {
			if r.RevokedAt != nil || !r.Expiry.After(now) {
				return ErrState
			}
			_, err = tx.Exec(ctx, `UPDATE hive.tenant_entitlements SET revoked_at=$3,revision=revision+1
				WHERE tenant_id=$1 AND capability_id=$2`, tenant, capability, now)
			if err != nil {
				return err
			}
		}
		r, err = read(ctx, tx, tenant, capability, "")
		if err != nil {
			return err
		}
		r.at(now)
		if _, err = tx.Exec(ctx, `INSERT INTO hive.tenant_entitlement_history (tenant_id,capability_id,revision,snapshot)
			VALUES ($1,$2,$3,$4)`, tenant, capability, r.Revision, r); err != nil {
			return err
		}
		if err = audit.RecordEntitlement(ctx, tx, audit.EntitlementEvent{Tenant: tenant, Capability: capability, Actor: actor, Action: operation, Reason: reason, PreviousRevision: expected, Revision: r.Revision}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.tenant_entitlement_receipts SET result=$6
			WHERE tenant_id=$1 AND capability_id=$2 AND actor_id=$3 AND operation=$4 AND request_key=$5`,
			tenant, capability, actor, operation, key, r)
		return err
	})
	return r, err
}

// Check allows a current online capability only. Consumers separately enforce user permissions and rollout.
func (s Service) Check(ctx context.Context, tenant, capability string) error {
	return check(ctx, s.DB, tenant, capability)
}

// CheckWrite coordinates an authorized business write with revocation using only the grant row.
// The caller must use this same transaction for the write and commit.
func (s Service) CheckWrite(ctx context.Context, tx pgx.Tx, tenant, capability string) error {
	_, err := s.CheckWriteSnapshot(ctx, tx, tenant, capability)
	return err
}

type CapabilitySnapshot struct {
	Revision int64
	Expires  time.Time
}

// CheckWriteSnapshot reports the version and time boundary of the grant locked
// until commit by this write transaction.
func (s Service) CheckWriteSnapshot(ctx context.Context, tx pgx.Tx, tenant, capability string) (CapabilitySnapshot, error) {
	var snapshot CapabilitySnapshot
	if !grantable(capability) {
		return snapshot, ErrDenied
	}
	err := tx.QueryRow(ctx, `SELECT revision,expires_at FROM hive.tenant_entitlements WHERE tenant_id=$1 AND capability_id=$2 FOR SHARE`, tenant, capability).
		Scan(&snapshot.Revision, &snapshot.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, ErrDenied
	}
	if err != nil {
		return snapshot, err
	}
	return snapshot, check(ctx, tx, tenant, capability)
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func check(ctx context.Context, db queryer, tenant, capability string) error {
	if !grantable(capability) {
		return ErrDenied
	}
	var r Result
	var now time.Time
	err := db.QueryRow(ctx, `SELECT e.effective_at,e.expires_at,e.revoked_at,clock_timestamp()
		FROM hive.tenant_entitlements e JOIN hive.active_tenants t ON t.id=e.tenant_id
		WHERE e.tenant_id=$1 AND e.capability_id=$2`, tenant, capability).
		Scan(&r.Effective, &r.Expiry, &r.RevokedAt, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	r.at(now)
	if r.State != "active" {
		return ErrDenied
	}
	return nil
}
