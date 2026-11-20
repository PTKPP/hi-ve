package audit

import "context"

func RecordReceiptSettings(ctx context.Context, w Writer, tenant, actor string, revision int64) error {
	_, err := w.Exec(ctx, `INSERT INTO hive.receipt_settings_audit(tenant_id,actor_id,revision,field_name)
		VALUES($1,$2,$3,'receipt_footer')`, tenant, actor, revision)
	return err
}
