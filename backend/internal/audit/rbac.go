package audit

import "context"

type RBACEvent struct {
	Tenant, Actor, Target, Action string
	Revision                      int64
	Before, After                 any
}

func RecordRBAC(ctx context.Context, w Writer, event RBACEvent) error {
	_, err := w.Exec(ctx, `INSERT INTO hive.tenant_rbac_audit
		(tenant_id,actor_id,target_id,action,revision,before_state,after_state)
		VALUES ($1,$2,$3,$4,$5,COALESCE($6::jsonb,'null'::jsonb),$7::jsonb)`,
		event.Tenant, event.Actor, event.Target, event.Action, event.Revision, event.Before, event.After)
	return err
}
