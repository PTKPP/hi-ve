package identity

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Invitation struct {
	Tenant, Owner, Generation string
	Expires                   time.Time
}

func RecordOwnerClaim(ctx context.Context, tx pgx.Tx, invitation Invitation, proof GoogleIdentity) (string, error) {
	if !proof.valid() {
		return "", ErrInvalidProof
	}
	var claim string
	err := tx.QueryRow(ctx, `INSERT INTO hive.owner_claims
		(tenant_id, owner_id, generation, issuer, subject, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id`, invitation.Tenant, invitation.Owner, invitation.Generation, proof.issuer, proof.subject, invitation.Expires).Scan(&claim)
	return claim, err
}

func ActivatePrimaryOwner(ctx context.Context, tx pgx.Tx, invitation Invitation, claim string) error {
	var issuer, subject string
	err := tx.QueryRow(ctx, `UPDATE hive.owner_claims SET consumed_at=clock_timestamp()
		WHERE id=$1 AND tenant_id=$2 AND owner_id=$3 AND generation=$4
		  AND consumed_at IS NULL AND expires_at > clock_timestamp()
		RETURNING issuer, subject`, claim, invitation.Tenant, invitation.Owner, invitation.Generation).Scan(&issuer, &subject)
	if err != nil {
		return err
	}
	var principal string
	err = tx.QueryRow(ctx, `INSERT INTO hive.google_principals (issuer, subject) VALUES ($1,$2)
		ON CONFLICT (issuer, subject) DO UPDATE SET subject=EXCLUDED.subject RETURNING id`, issuer, subject).Scan(&principal)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO hive.tenant_memberships (tenant_id, principal_id, owner_id, role, owner_google_principal_id)
		VALUES ($1,$2,$3,'primary_owner',$2)`, invitation.Tenant, principal, invitation.Owner)
	return err
}
