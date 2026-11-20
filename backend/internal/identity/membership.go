package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
)

type MembershipLifecycle struct {
	DB                   *pgxpool.Pool
	StaffPINSchema       bool
	FingerprintKey       []byte
	RemoveOperatorAccess func(context.Context, pgx.Tx, string, string) error
}

type MembershipChange struct {
	Revision *int64 `json:"revision"`
	Reason   string `json:"reason_code"`
}

type MembershipResult struct {
	Member   string `json:"member_id"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

func (s MembershipLifecycle) Transition(ctx context.Context, tenant, member, token, key string, enable bool, input MembershipChange) (MembershipResult, error) {
	if input.Revision == nil || *input.Revision < 0 || input.Reason != "administrative_action" {
		return MembershipResult{}, ErrRBACInput
	}
	operation, status := "deactivate", "inactive"
	if enable {
		operation, status = "reactivate", "active"
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return MembershipResult{}, err
	}
	mac := hmac.New(sha256.New, s.FingerprintKey)
	mac.Write(payload)
	fingerprint := mac.Sum(nil)
	var result MembershipResult
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := AuthorizeTenantOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		if actor == member {
			return ErrForbidden
		}
		// Precheck the target before reserving a receipt. The locked check below is authoritative.
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$2)`, tenant, member).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return ErrRBACTarget
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.membership_lifecycle_receipts
			(tenant_id,actor_id,member_id,operation,request_key,fingerprint)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, tenant, actor, member, operation, key, fingerprint)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var previous []byte
			err = tx.QueryRow(ctx, `SELECT fingerprint,result FROM hive.membership_lifecycle_receipts
				WHERE tenant_id=$1 AND actor_id=$2 AND member_id=$3 AND operation=$4 AND request_key=$5`,
				tenant, actor, member, operation, key).Scan(&previous, &result)
			if err != nil {
				return err
			}
			if !hmac.Equal(previous, fingerprint) {
				return ErrRBACReplay
			}
			return nil
		}
		// Same order as role assignment: RBAC revision, then the target membership.
		if !enable {
			if err = lockRBACRevision(ctx, tx, tenant); err != nil {
				return err
			}
		}
		var role string
		var active bool
		var revision int64
		err = tx.QueryRow(ctx, `SELECT role,active,lifecycle_revision FROM hive.tenant_memberships
			WHERE tenant_id=$1 AND principal_id=$2 FOR NO KEY UPDATE`, tenant, member).Scan(&role, &active, &revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRBACTarget
		}
		if err != nil {
			return err
		}
		if role == "primary_owner" {
			return ErrForbidden
		}
		if revision != *input.Revision {
			return ErrRBACRevision
		}
		if active == enable {
			return ErrRBACConflict
		}
		var revokedSessions int64
		revokedRoles := []string{}
		if !enable {
			if s.StaffPINSchema {
				if _, err = tx.Exec(ctx, `UPDATE hive.member_pin_credentials SET revision=revision+1,pepper_version=NULL,salt=NULL,verifier=NULL
                    WHERE tenant_id=$1 AND member_id=$2 AND verifier IS NOT NULL`, tenant, member); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, `DELETE FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2`, tenant, member); err != nil {
					return err
				}
			}
			if s.RemoveOperatorAccess != nil {
				if err = s.RemoveOperatorAccess(ctx, tx, tenant, member); err != nil {
					return err
				}
			}
			revokedRoles, revokedSessions, err = removeMemberAccess(ctx, tx, tenant, actor, member)
			if err != nil {
				return err
			}
		}
		err = tx.QueryRow(ctx, `UPDATE hive.tenant_memberships SET active=$3,lifecycle_revision=lifecycle_revision+1
			WHERE tenant_id=$1 AND principal_id=$2 RETURNING lifecycle_revision`, tenant, member, enable).Scan(&revision)
		if err != nil {
			return err
		}
		if err = audit.RecordMembership(ctx, tx, audit.MembershipEvent{Tenant: tenant, Actor: actor, Member: member, Action: operation,
			Reason: input.Reason, PreviousActive: active, Active: enable, Revision: revision, RevokedSessions: revokedSessions, RevokedRoles: revokedRoles}); err != nil {
			return err
		}
		result = MembershipResult{Member: member, Status: status, Revision: revision}
		_, err = tx.Exec(ctx, `UPDATE hive.membership_lifecycle_receipts SET result=$6
			WHERE tenant_id=$1 AND actor_id=$2 AND member_id=$3 AND operation=$4 AND request_key=$5`,
			tenant, actor, member, operation, key, result)
		return err
	})
	return result, err
}

func lockRBACRevision(ctx context.Context, tx pgx.Tx, tenant string) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.tenant_rbac_revisions (tenant_id) VALUES ($1) ON CONFLICT DO NOTHING`, tenant)
	if err != nil {
		return err
	}
	var revision int64
	return tx.QueryRow(ctx, `SELECT revision FROM hive.tenant_rbac_revisions WHERE tenant_id=$1 FOR UPDATE`, tenant).Scan(&revision)
}

func removeMemberAccess(ctx context.Context, tx pgx.Tx, tenant, actor, member string) ([]string, int64, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM hive.tenant_sessions WHERE tenant_id=$1 AND principal_id=$2`, tenant, member)
	if err != nil {
		return nil, 0, err
	}
	var roles []string
	err = tx.QueryRow(ctx, `WITH deleted AS (
		DELETE FROM hive.tenant_role_assignments WHERE tenant_id=$1 AND principal_id=$2 RETURNING role_id
	) SELECT ARRAY(SELECT role_id::text FROM deleted ORDER BY role_id)`, tenant, member).Scan(&roles)
	if err != nil {
		return nil, 0, err
	}
	if len(roles) > 0 {
		revision, err := advanceRevision(ctx, tx, tenant, nil)
		if err != nil {
			return nil, 0, err
		}
		if err = audit.RecordRBAC(ctx, tx, audit.RBACEvent{Tenant: tenant, Actor: actor, Target: member, Action: "membership.roles.replace",
			Revision: revision, Before: roles, After: []string{}}); err != nil {
			return nil, 0, err
		}
	}
	scoped, err := loadLocationAssignments(ctx, tx, tenant, member)
	if err != nil {
		return nil, 0, err
	}
	if len(scoped) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM hive.tenant_location_role_assignments WHERE tenant_id=$1 AND member_id=$2`, tenant, member); err != nil {
			return nil, 0, err
		}
		revision, err := advanceRevision(ctx, tx, tenant, nil)
		if err != nil {
			return nil, 0, err
		}
		if err = audit.RecordRBAC(ctx, tx, audit.RBACEvent{Tenant: tenant, Actor: actor, Target: member, Action: "membership.location_roles.replace", Revision: revision, Before: scoped, After: []LocationAssignment{}}); err != nil {
			return nil, 0, err
		}
	}
	return roles, tag.RowsAffected(), nil
}
