package provisioning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
	"strings"
)

type Administration struct {
	Case      string `json:"case_id"`
	Reason    string `json:"reason"`
	Evidence  string `json:"evidence_reference,omitempty"`
	Expected  string `json:"expected_owner_id,omitempty"`
	Candidate string `json:"candidate_principal_id,omitempty"`
	Revision  *int64 `json:"revision"`
}
type Lifecycle struct {
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

func (s Service) Administer(ctx context.Context, token, tenant, action string, in Administration, fence func(context.Context, pgx.Tx, string) error, removeOperator func(context.Context, pgx.Tx, string, string) error) (Lifecycle, error) {
	var out Lifecycle
	in.Reason, in.Evidence = strings.TrimSpace(in.Reason), strings.TrimSpace(in.Evidence)
	if fence == nil || len(in.Case) < 1 || len(in.Case) > 128 || len(in.Reason) < 1 || len(in.Reason) > 500 || in.Revision == nil || *in.Revision < 0 ||
		action == "transfer" && (in.Evidence == "" || len(in.Evidence) > 128 || in.Expected == in.Candidate || removeOperator == nil) {
		return out, ErrConflict
	}
	permission := "platform.tenants." + action
	if action == "transfer" {
		permission = "platform.tenants.owner.transfer"
	}
	raw, _ := json.Marshal(in)
	hash := sha256.Sum256(append([]byte(action+"\x00"), raw...))
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := identity.AuthorizePlatform(ctx, tx, token, permission)
		if err != nil {
			return err
		}
		// A tenant's admin commands serialize before taking member locks.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('tenant-administration:'||$1::text,0))`, tenant); err != nil {
			return err
		}
		var prior []byte
		var priorActor string
		err = tx.QueryRow(ctx, `SELECT fingerprint,actor_id,status,revision FROM hive.tenant_administration_audit WHERE tenant_id=$1 AND case_id=$2`, tenant, in.Case).Scan(&prior, &priorActor, &out.Status, &out.Revision)
		if err == nil {
			if priorActor != actor || !bytes.Equal(prior, hash[:]) {
				return ErrKeyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err = identity.LockTenantMemberships(ctx, tx, tenant); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT status,authorization_epoch FROM hive.tenants WHERE id=$1 FOR UPDATE`, tenant).Scan(&out.Status, &out.Revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if out.Revision != *in.Revision {
			return ErrConflict
		}
		var next string
		switch action {
		case "suspend":
			if out.Status != "active" {
				return ErrConflict
			}
			next = "suspended"
		case "reactivate":
			if out.Status != "suspended" {
				return ErrConflict
			}
			var active bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.tenant_memberships WHERE tenant_id=$1 AND role='primary_owner' AND active)`, tenant).Scan(&active)
			if err != nil {
				return err
			}
			if !active {
				return ErrConflict
			}
			next = "active"
		case "close":
			if out.Status != "active" && out.Status != "suspended" {
				return ErrConflict
			}
			next = "closed"
		case "transfer":
			if out.Status != "active" && out.Status != "suspended" {
				return ErrConflict
			}
			if err = identity.TransferPrimaryOwner(ctx, tx, tenant, in.Expected, in.Candidate); err != nil {
				return err
			}
			if err = removeOperator(ctx, tx, tenant, in.Candidate); err != nil {
				return err
			}
			if err = removeOperator(ctx, tx, tenant, in.Expected); err != nil {
				return err
			}
		default:
			return ErrConflict
		}
		if next != "" {
			err = tx.QueryRow(ctx, `UPDATE hive.tenants SET status=$2 WHERE id=$1 RETURNING status,authorization_epoch`, tenant, next).Scan(&out.Status, &out.Revision)
		} else {
			err = tx.QueryRow(ctx, `UPDATE hive.tenants SET authorization_epoch=authorization_epoch+1 WHERE id=$1 RETURNING authorization_epoch`, tenant).Scan(&out.Revision)
		}
		if err != nil {
			return err
		}
		if err = identity.RevokeTenantSessions(ctx, tx, tenant); err != nil {
			return err
		}
		if err = fence(ctx, tx, tenant); err != nil {
			return err
		}
		return audit.RecordTenantAdministration(ctx, tx, audit.TenantAdministrationEvent{
			Tenant: tenant, Case: in.Case, Actor: actor, Action: action, Evidence: in.Evidence, Reason: in.Reason,
			PreviousOwner: in.Expected, NewOwner: in.Candidate, Revision: out.Revision, Status: out.Status, Fingerprint: hash[:],
		})
	})
	return out, err
}
