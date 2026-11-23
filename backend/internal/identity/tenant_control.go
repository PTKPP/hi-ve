package identity

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

var ErrOwnerCandidate = errors.New("candidate lacks a verified Google identity")

func LockTenantMemberships(ctx context.Context, tx pgx.Tx, tenant string) error {
	_, err := tx.Exec(ctx, `SELECT principal_id FROM hive.tenant_memberships WHERE tenant_id=$1 ORDER BY principal_id FOR UPDATE`, tenant)
	return err
}
func RevokeTenantSessions(ctx context.Context, tx pgx.Tx, tenant string) error {
	_, err := tx.Exec(ctx, `DELETE FROM hive.tenant_sessions WHERE tenant_id=$1`, tenant)
	return err
}

// TransferPrimaryOwner requires the caller's tenant and membership locks.
// The manual proof is recorded by Platform. A Google subject, not a contact address, identifies the candidate.
func TransferPrimaryOwner(ctx context.Context, tx pgx.Tx, tenant, expected, candidate string) error {
	var linked string
	err := tx.QueryRow(ctx, `SELECT p.id FROM hive.google_principals p JOIN hive.identity_principals i ON i.id=p.id
    WHERE p.id=$1 AND p.issuer='https://accounts.google.com' AND length(p.subject)>0 FOR KEY SHARE OF p`, candidate).Scan(&linked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOwnerCandidate
	}
	if err != nil {
		return err
	}
	if expected == candidate {
		return ErrForbidden
	}
	// The legacy owner_id is the tenant's provisioning anchor. Authorization
	// follows principal_id and the validated Google subject, not this anchor.
	var anchor string
	err = tx.QueryRow(ctx, `UPDATE hive.tenant_memberships SET active=false,lifecycle_revision=lifecycle_revision+1
       WHERE tenant_id=$1 AND principal_id=$2 AND active AND role='primary_owner' RETURNING owner_id`, tenant, expected).Scan(&anchor)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO hive.tenant_memberships (tenant_id,principal_id,owner_id,role,active,owner_google_principal_id)
        VALUES($1,$2,$3,'primary_owner',true,$2)
        ON CONFLICT (tenant_id,principal_id) DO UPDATE SET owner_id=EXCLUDED.owner_id,role='primary_owner',active=true,
        owner_google_principal_id=EXCLUDED.owner_google_principal_id,lifecycle_revision=hive.tenant_memberships.lifecycle_revision+1`, tenant, candidate, anchor)
	return err
}
