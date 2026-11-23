package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrOfflineConflict = errors.New("offline audit event differs from persisted evidence")

func RecordOfflineLogin(ctx context.Context, tx pgx.Tx, tenant, lease, event, member, device, outlet, outcome string, occurred time.Time, digest [32]byte) error {
	h := sha256.New()
	h.Write([]byte("hive.offline.login.receipt.v1:" + lease + ":"))
	h.Write(digest[:])
	fingerprint := h.Sum(nil)
	tag, err := tx.Exec(ctx, `INSERT INTO hive.offline_login_receipts VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, event, fingerprint)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var stored []byte
		if err = tx.QueryRow(ctx, `SELECT fingerprint FROM hive.offline_login_receipts WHERE tenant_id=$1 AND event_id=$2`, tenant, event).Scan(&stored); err != nil {
			return err
		}
		if !bytes.Equal(stored, fingerprint) {
			return ErrOfflineConflict
		}
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO hive.operator_login_history(tenant_id,attempt_id,member_id,device_id,outlet_id,outcome,offline_occurred_at,offline_lease_id)
	    VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, event, member, device, outlet, outcome, occurred, lease)
	return err
}

func RecordOfflineControl(ctx context.Context, tx pgx.Tx, tenant, device, actor, lease, action, reason, outlet string) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.offline_control_audit(tenant_id,device_id,actor_id,lease_id,action,reason,outlet_id)
	    VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,NULLIF($6,''),NULLIF($7,'')::uuid)`, tenant, device, actor, lease, action, reason, outlet)
	return err
}

func HasOfflineControl(ctx context.Context, tx pgx.Tx, tenant, lease, action, reason string) (bool, error) {
	var same bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.offline_control_audit WHERE tenant_id=$1 AND lease_id=$2 AND action=$3 AND reason=$4)`, tenant, lease, action, reason).Scan(&same)
	return same, err
}
