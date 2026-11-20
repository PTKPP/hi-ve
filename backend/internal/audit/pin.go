package audit

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func RecordStaffPIN(ctx context.Context, tx pgx.Tx, tenant, actor, member, action string, revision int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.staff_pin_management_events
        (tenant_id,actor_id,member_id,action,revision) VALUES ($1,$2,$3,$4,$5)`,
		tenant, actor, member, action, revision)
	return err
}
