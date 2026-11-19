package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
)

var (
	ErrUnauthenticated = errors.New("invalid session")
	ErrForbidden       = errors.New("permission denied")
)

type Store struct{ DB *pgxpool.Pool }

type Flow struct {
	Purpose, Tenant, Generation, Nonce, Verifier, EventID string
	Expires                                               time.Time
}

func Digest(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func (s Store) SaveFlow(ctx context.Context, state, browser string, f Flow) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO hive.oidc_flows
		(state_digest,browser_digest,nonce,verifier,purpose,tenant_id,generation,expires_at)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,$8)`,
		Digest(state), Digest(browser), f.Nonce, f.Verifier, f.Purpose, f.Tenant, f.Generation, f.Expires)
	return err
}

func (s Store) TakeFlow(ctx context.Context, state, browser string) (Flow, error) {
	var f Flow
	err := s.DB.QueryRow(ctx, `DELETE FROM hive.oidc_flows
		WHERE state_digest=$1 AND browser_digest=$2 AND expires_at > clock_timestamp()
		RETURNING purpose,COALESCE(tenant_id::text,''),COALESCE(generation::text,''),nonce,verifier,expires_at,event_id`,
		Digest(state), Digest(browser)).Scan(&f.Purpose, &f.Tenant, &f.Generation, &f.Nonce, &f.Verifier, &f.Expires, &f.EventID)
	return f, err
}

// StartSession replaces only the browser's previous session in the same context.
func (s Store) StartSession(ctx context.Context, proof GoogleIdentity, flow Flow, secret, previous string, expires time.Time) error {
	if !proof.valid() {
		return ErrInvalidProof
	}
	if flow.Purpose == "tenant" && flow.Tenant != "" && flow.EventID != "" {
		return s.startOwnerSession(ctx, proof, flow, secret, previous, expires)
	}
	if flow.Purpose != "platform" || flow.Tenant != "" {
		return ErrInvalidProof
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO hive.platform_sessions (digest,principal_id,expires_at)
			SELECT $1,p.id,$4 FROM hive.google_principals p JOIN hive.platform_principals a ON a.principal_id=p.id
			WHERE p.issuer=$2 AND p.subject=$3 AND a.active`, Digest(secret), proof.issuer, proof.subject, expires)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrForbidden
		}
		if previous == "" || previous == secret {
			return nil
		}
		_, err = tx.Exec(ctx, `DELETE FROM hive.platform_sessions WHERE digest=$1`, Digest(previous))
		return err
	})
}

func (s Store) startOwnerSession(ctx context.Context, proof GoogleIdentity, flow Flow, secret, previous string, expires time.Time) error {
	denied := false
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var actor string
		err := tx.QueryRow(ctx, `SELECT m.principal_id FROM hive.tenant_memberships m
			JOIN hive.google_principals p ON p.id=m.principal_id
			WHERE m.tenant_id=$1 AND p.issuer=$2 AND p.subject=$3 AND m.role='primary_owner'`,
			flow.Tenant, proof.issuer, proof.subject).Scan(&actor)
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Debug("tenant Google login rejected before Owner attribution")
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		// Lock both memberships before touching sessions, including a cookie from
		// a different principal in the same tenant. Match lifecycle and logout ordering.
		if _, err = tx.Exec(ctx, `SELECT m.principal_id FROM hive.tenant_memberships m
			WHERE m.tenant_id=$1 AND (
				m.principal_id=$2 OR m.principal_id=(
					SELECT principal_id FROM hive.tenant_sessions WHERE tenant_id=$1 AND digest=$3
				)
			) ORDER BY m.principal_id FOR SHARE`, flow.Tenant, actor, Digest(previous)); err != nil {
			return err
		}
		var active bool
		err = tx.QueryRow(ctx, `SELECT active FROM hive.tenant_memberships
			WHERE tenant_id=$1 AND principal_id=$2 AND role='primary_owner'`, flow.Tenant, actor).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if active {
			var tenant string
			err = tx.QueryRow(ctx, `SELECT id FROM hive.active_tenants WHERE id=$1 FOR SHARE`, flow.Tenant).Scan(&tenant)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			active = tenant != ""
		}
		if !active {
			denied = true
			_, err = audit.RecordOwnerLogin(ctx, tx, flow.EventID, flow.Tenant, actor, "denied")
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.tenant_sessions (digest,tenant_id,principal_id,expires_at,auth_method)
			VALUES ($1,$2,$3,$4,'google')`, Digest(secret), flow.Tenant, actor, expires); err != nil {
			return err
		}
		if previous != "" && previous != secret {
			if _, err = tx.Exec(ctx, `DELETE FROM hive.tenant_sessions WHERE digest=$1 AND tenant_id=$2`, Digest(previous), flow.Tenant); err != nil {
				return err
			}
		}
		inserted, err := audit.RecordOwnerLogin(ctx, tx, flow.EventID, flow.Tenant, actor, "success")
		if err == nil && !inserted {
			return ErrInvalidProof
		}
		return err
	})
	if denied {
		if err != nil {
			slog.Error("denied tenant Google login event persistence failed")
		}
		return ErrForbidden
	}
	return err
}

func AuthorizePlatform(ctx context.Context, tx pgx.Tx, secret, permission string) (string, error) {
	var actor string
	err := tx.QueryRow(ctx, `SELECT a.principal_id FROM hive.platform_sessions s
		JOIN hive.platform_principals a ON a.principal_id=s.principal_id
		WHERE s.digest=$1 AND s.expires_at > clock_timestamp() AND a.active FOR SHARE OF s,a`, Digest(secret)).Scan(&actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	var granted string
	err = tx.QueryRow(ctx, `SELECT permission FROM hive.platform_permissions
		WHERE principal_id=$1 AND permission=$2 FOR SHARE`, actor, permission).Scan(&granted)
	if errors.Is(err, pgx.ErrNoRows) {
		return actor, ErrForbidden
	}
	return actor, err
}

func (s Store) TenantPrincipal(ctx context.Context, secret, tenant string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `SELECT s.principal_id FROM hive.tenant_sessions s
		JOIN hive.tenant_memberships m USING (tenant_id,principal_id) JOIN hive.active_tenants t ON t.id=s.tenant_id
		WHERE s.digest=$1 AND s.tenant_id=$2 AND s.expires_at > clock_timestamp() AND m.active
		AND (s.auth_method='device_pin' OR m.role='primary_owner')`,
		Digest(secret), tenant).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	return id, err
}

// RemoveExpiredAuthState removes a bounded batch from each Identity-owned auth table.
func (s Store) RemoveExpiredAuthState(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.DB.Exec(ctx, `WITH expired_flows AS (
		DELETE FROM hive.oidc_flows WHERE state_digest IN (
			SELECT state_digest FROM hive.oidc_flows WHERE expires_at <= statement_timestamp()
			ORDER BY expires_at LIMIT 1000 FOR UPDATE SKIP LOCKED
		)
	), expired_platform AS (
		DELETE FROM hive.platform_sessions WHERE digest IN (
			SELECT digest FROM hive.platform_sessions WHERE expires_at <= statement_timestamp()
			ORDER BY expires_at LIMIT 1000 FOR UPDATE SKIP LOCKED
		)
	)
	DELETE FROM hive.tenant_sessions WHERE digest IN (
		SELECT digest FROM hive.tenant_sessions WHERE expires_at <= statement_timestamp()
		ORDER BY expires_at LIMIT 1000 FOR UPDATE SKIP LOCKED
	)`)
	return err
}
