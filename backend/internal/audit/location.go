package audit

import "context"

func RecordLocationCreate(ctx context.Context, tx Writer, tenant, location, actor, kind string) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.location_create_audit
		(tenant_id,location_id,actor_id,kind) VALUES ($1,$2,$3,$4)`, tenant, location, actor, kind)
	return err
}
