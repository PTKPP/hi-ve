package audit

import "context"

type EntitlementEvent struct {
	Tenant, Capability, Actor, Action, Reason string
	PreviousRevision, Revision                int64
}

func RecordEntitlement(ctx context.Context, tx Writer, event EntitlementEvent) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.entitlement_audit
		(tenant_id,capability_id,actor_id,action,previous_revision,revision,reason_code)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''))`, event.Tenant, event.Capability, event.Actor, event.Action, event.PreviousRevision, event.Revision, event.Reason)
	return err
}
