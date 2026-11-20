package audit

import "context"

func RecordDeviceContext(ctx context.Context, w Writer, tenant, device, actor, action string, revision int64, id, outlet string, count *int) error {
	_, err := w.Exec(ctx, `INSERT INTO hive.operational_device_audit(tenant_id,device_id,actor_id,action,revision,context_id,outlet_id,assignment_count)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,$8)`, tenant, device, actor, action, revision, id, outlet, count)
	return err
}
