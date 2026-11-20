package audit

import "context"

func RecordDevicePairing(ctx context.Context, tx Writer, action, tenant, actor, pairing, device string) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.device_pairing_audit (action,tenant_id,actor_id,pairing_id,device_id)
		VALUES ($1,$2,$3,$4,NULLIF($5,'')::uuid)`, action, tenant, actor, pairing, device)
	return err
}
