package audit

import "context"

func RecordLogout(ctx context.Context, tx Writer, scope, tenant, actor string) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.session_logout_audit(scope,tenant_id,actor_id)
		VALUES ($1,NULLIF($2,'')::uuid,$3)`, scope, tenant, actor)
	return err
}
