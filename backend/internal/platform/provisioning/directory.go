package provisioning

import (
	"context"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/identity"
)

type TenantEntry struct {
	Revision int64   `json:"revision"`
	ID       string  `json:"tenant_id"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Owner    *string `json:"primary_owner_id,omitempty"`
}

func (s Service) Directory(ctx context.Context, token, after string) ([]TenantEntry, error) {
	out := []TenantEntry{}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := identity.AuthorizePlatform(ctx, tx, token, "platform.tenants.read"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT t.authorization_epoch,t.id,t.display_name,t.status,m.principal_id FROM hive.tenants t
		 LEFT JOIN hive.tenant_memberships m ON m.tenant_id=t.id AND m.role='primary_owner' AND m.active
		 WHERE $1='' OR t.id>NULLIF($1,'')::uuid ORDER BY t.id LIMIT 51`, after)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[TenantEntry])
		return err
	})
	return out, err
}
