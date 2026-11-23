package audit

import (
	"context"
	"github.com/jackc/pgx/v5"
)

func RecordGlobalBoolean(ctx context.Context, tx pgx.Tx, actor, request, key string, previous, value bool, revision int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.global_setting_audit(actor_id,request_id,key,previous,enabled,revision) VALUES($1,$2,$3,$4,$5,$6)`, actor, request, key, previous, value, revision)
	return err
}

type TenantAdministrationEvent struct {
	Tenant, Case, Actor, Action, Evidence, Reason, PreviousOwner, NewOwner, Status string
	Revision                                                                       int64
	Fingerprint                                                                    []byte
}

func RecordTenantAdministration(ctx context.Context, tx pgx.Tx, e TenantAdministrationEvent) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.tenant_administration_audit
 (tenant_id,case_id,actor_id,action,evidence_reference,reason,previous_owner,new_owner,revision,status,fingerprint)
 VALUES($1,$2,$3,$4,NULLIF($5,''),$6,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,$9,$10,$11)`,
		e.Tenant, e.Case, e.Actor, e.Action, e.Evidence, e.Reason, e.PreviousOwner, e.NewOwner, e.Revision, e.Status, e.Fingerprint)
	return err
}
