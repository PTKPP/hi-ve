package audit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type PlatformEvent struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Actor      *string   `json:"actor_id"`
	Action     string    `json:"action"`
	Outcome    string    `json:"outcome"`
	At         time.Time `json:"occurred_at"`
	Capability *string   `json:"capability_id,omitempty"`
}

// PlatformHistory projects operational audit metadata. Organization
// verification references and credential details stay in the write model.
func PlatformHistory(ctx context.Context, tx pgx.Tx, tenant, beforeID, beforeSource string, before *time.Time) ([]PlatformEvent, error) {
	rows, err := tx.Query(ctx, `
	    (SELECT id,'provisioning'::text source,actor_id,action,outcome,occurred_at,NULL::text capability_id
	     FROM hive.provisioning_audit WHERE tenant_id=$1 AND
	     ($2::timestamptz IS NULL OR (occurred_at,id,'provisioning'::text)<($2,NULLIF($3,'')::uuid,$4))
	     ORDER BY occurred_at DESC,id DESC LIMIT 51)
	    UNION ALL
	    (SELECT id,'entitlement'::text source,actor_id,'entitlement.'||action,'succeeded',occurred_at,capability_id
	     FROM hive.entitlement_audit WHERE tenant_id=$1 AND
	     ($2::timestamptz IS NULL OR (occurred_at,id,'entitlement'::text)<($2,NULLIF($3,'')::uuid,$4))
	     ORDER BY occurred_at DESC,id DESC LIMIT 51)
	    ORDER BY occurred_at DESC,id DESC,source DESC LIMIT 51`, tenant, before, beforeID, beforeSource)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[PlatformEvent])
}
