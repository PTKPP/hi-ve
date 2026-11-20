package audit

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

type Writer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type ProvisioningEvent struct {
	Actor, Tenant, Claim string
	Action, Outcome      string
	Method, Reference    string
}

func RecordProvisioning(ctx context.Context, db Writer, event ProvisioningEvent) error {
	_, err := db.Exec(ctx, `INSERT INTO hive.provisioning_audit
		(actor_id, tenant_id, claim_id, action, outcome, verification_method, verification_reference)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, $5, NULLIF($6,''), NULLIF($7,''))`,
		event.Actor, event.Tenant, event.Claim, event.Action, event.Outcome, event.Method, event.Reference)
	return err
}
