package devices

import (
	"context"
	"github.com/jackc/pgx/v5"
)

func FenceTenantContexts(ctx context.Context, tx pgx.Tx, tenant string) error {
	// The tenant lock belongs to the coordinator. Session creation checks it too.
	_, err := tx.Exec(ctx, `UPDATE hive.operational_contexts SET ended_at=clock_timestamp() WHERE tenant_id=$1 AND ended_at IS NULL`, tenant)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM hive.device_operation_challenges WHERE tenant_id=$1`, tenant)
	return err
}
