package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUnauthenticated = errors.New("invalid session")
	ErrForbidden       = errors.New("permission denied")
)

type Store struct{ DB *pgxpool.Pool }

type Flow struct {
	Purpose, Tenant, Generation, Nonce, Verifier string
	Expires                                      time.Time
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
		RETURNING purpose,COALESCE(tenant_id::text,''),COALESCE(generation::text,''),nonce,verifier,expires_at`,
		Digest(state), Digest(browser)).Scan(&f.Purpose, &f.Tenant, &f.Generation, &f.Nonce, &f.Verifier, &f.Expires)
	return f, err
}

// StartSession replaces the browser's previous session in the same context after a valid login.
func (s Store) StartSession(ctx context.Context, proof GoogleIdentity, tenant, secret, previous string, expires time.Time) error {
	if !proof.valid() {
		return ErrInvalidProof
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := insertSession(ctx, tx, proof, tenant, secret, expires); err != nil {
			return err
		}
		if previous == "" || previous == secret {
			return nil
		}
		if tenant == "" {
			_, err := tx.Exec(ctx, `DELETE FROM hive.platform_sessions WHERE digest=$1`, Digest(previous))
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM hive.tenant_sessions WHERE digest=$1 AND tenant_id=$2`, Digest(previous), tenant)
		return err
	})
}

func insertSession(ctx context.Context, tx pgx.Tx, proof GoogleIdentity, tenant, secret string, expires time.Time) error {
	var result string
	var err error
	if tenant == "" {
		err = tx.QueryRow(ctx, `INSERT INTO hive.platform_sessions (digest,principal_id,expires_at)
			SELECT $1,p.id,$4 FROM hive.google_principals p JOIN hive.platform_principals a ON a.principal_id=p.id
			WHERE p.issuer=$2 AND p.subject=$3 AND a.active RETURNING principal_id`,
			Digest(secret), proof.issuer, proof.subject, expires).Scan(&result)
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO hive.tenant_sessions (digest,tenant_id,principal_id,expires_at)
			SELECT $1,m.tenant_id,p.id,$4 FROM hive.google_principals p
			JOIN hive.tenant_memberships m ON m.principal_id=p.id JOIN hive.active_tenants t ON t.id=m.tenant_id
			WHERE p.issuer=$2 AND p.subject=$3 AND m.active AND t.id=$5 RETURNING principal_id`,
			Digest(secret), proof.issuer, proof.subject, expires, tenant).Scan(&result)
	}
	if errors.Is(err, pgx.ErrNoRows) {
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
		WHERE s.digest=$1 AND s.tenant_id=$2 AND s.expires_at > clock_timestamp() AND m.active`,
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
