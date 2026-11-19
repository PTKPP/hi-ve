package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
)

// Logout revokes exactly the presented, unexpired session in its own context.
// The caller clears the cookie only when the audit and deletion have committed.
func (s Store) Logout(ctx context.Context, tenant, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	digest := Digest(token)
	scope := "platform"
	remove := `DELETE FROM hive.platform_sessions WHERE digest=$1 AND expires_at>clock_timestamp() RETURNING principal_id`
	lock := `SELECT principal_id FROM hive.platform_sessions WHERE digest=$1 FOR UPDATE`
	args := []any{digest}
	if tenant != "" {
		scope = "tenant"
		remove = `DELETE FROM hive.tenant_sessions WHERE digest=$1 AND tenant_id=$2 AND expires_at>clock_timestamp() RETURNING principal_id`
		lock = `SELECT principal_id FROM hive.tenant_sessions WHERE digest=$1 AND tenant_id=$2 FOR UPDATE`
		args = append(args, tenant)
	}
	removed := false
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var actor string
		if tenant != "" {
			// Match issuance and membership lifecycle: membership before session.
			err := tx.QueryRow(ctx, `SELECT m.principal_id FROM hive.tenant_sessions s
				JOIN hive.tenant_memberships m USING(tenant_id,principal_id)
				WHERE s.digest=$1 AND s.tenant_id=$2 FOR SHARE OF m`, args...).Scan(&actor)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		// Check expiry after acquiring the row lock, so a waiter does not count an
		// already rotated or expired session as an explicit logout.
		err := tx.QueryRow(ctx, lock, args...).Scan(&actor)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, remove, args...).Scan(&actor)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = audit.RecordLogout(ctx, tx, scope, tenant, actor); err != nil {
			return err
		}
		removed = true
		return nil
	})
	return removed && err == nil, err
}
