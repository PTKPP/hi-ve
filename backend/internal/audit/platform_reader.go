package audit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type PlatformEvent struct {
	ID      string    `json:"id"`
	Actor   *string   `json:"actor_id"`
	Action  string    `json:"action"`
	Outcome string    `json:"outcome"`
	At      time.Time `json:"occurred_at"`
}

// PlatformProvisioning projects only operational audit metadata. Organization
// verification references and credential details stay in the write model.
func PlatformProvisioning(ctx context.Context, tx pgx.Tx, tenant, beforeID string, before *time.Time) ([]PlatformEvent, error) {
	rows, err := tx.Query(ctx, `SELECT id,actor_id,action,outcome,occurred_at FROM hive.provisioning_audit
	    WHERE tenant_id=$1 AND ($2::timestamptz IS NULL OR (occurred_at,id)<($2,NULLIF($3,'')::uuid))
	    ORDER BY occurred_at DESC,id DESC LIMIT 51`, tenant, before, beforeID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[PlatformEvent])
}
