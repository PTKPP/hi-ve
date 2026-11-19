package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
)

const rolesRead = "identity.roles.read"

var (
	ErrRBACInput    = errors.New("invalid RBAC input")
	ErrRBACTarget   = errors.New("RBAC target unavailable")
	ErrRBACConflict = errors.New("RBAC state conflict")
	ErrRBACReplay   = errors.New("RBAC idempotency conflict")
	ErrRBACRevision = errors.New("RBAC revision conflict")
	roleKeyPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	roleIDPattern   = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

type RBAC struct {
	DB             *pgxpool.Pool
	FingerprintKey []byte
}

type Role struct {
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type RoleResult struct {
	Role     Role  `json:"role"`
	Revision int64 `json:"revision"`
}

type RoleList struct {
	Roles    []Role `json:"roles"`
	Revision int64  `json:"revision"`
}

type AssignmentResult struct {
	MemberID string   `json:"member_id"`
	RoleIDs  []string `json:"role_ids"`
	Revision int64    `json:"revision"`
}

type CreateRole struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type ReplaceRole struct {
	Name     string   `json:"name"`
	Actions  []string `json:"actions"`
	Revision *int64   `json:"revision"`
}

type ReplaceAssignments struct {
	RoleIDs  []string `json:"role_ids"`
	Revision *int64   `json:"revision"`
}

func validateRole(name string, actions []string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || strings.ContainsFunc(name, unicode.IsControl) ||
		actions == nil || len(actions) > 1 || (len(actions) == 1 && actions[0] != rolesRead) {
		return "", ErrRBACInput
	}
	return name, nil
}

// ownerActor locks only the presented session and its membership for control-plane mutations.
func ownerActor(ctx context.Context, tx pgx.Tx, tenant, token string) (string, error) {
	var actor, role string
	err := tx.QueryRow(ctx, `SELECT m.principal_id,m.role
		FROM hive.tenant_sessions s JOIN hive.tenant_memberships m USING (tenant_id,principal_id)
		JOIN hive.active_tenants t ON t.id=s.tenant_id
		WHERE s.digest=$1 AND s.tenant_id=$2 AND s.expires_at>clock_timestamp() AND m.active`,
		Digest(token), tenant).Scan(&actor, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	if role != "primary_owner" {
		return "", ErrForbidden
	}
	// Match the issuance/lifecycle lock order. Recheck the presented session after
	// locking its membership, since another browser transaction could rotate it.
	err = tx.QueryRow(ctx, `SELECT role FROM hive.tenant_memberships
		WHERE tenant_id=$1 AND principal_id=$2 AND active FOR SHARE`, tenant, actor).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	if role != "primary_owner" {
		return "", ErrForbidden
	}
	err = tx.QueryRow(ctx, `SELECT s.principal_id FROM hive.tenant_sessions s
		JOIN hive.active_tenants t ON t.id=s.tenant_id
		WHERE s.digest=$1 AND s.tenant_id=$2 AND s.principal_id=$3 AND s.expires_at>clock_timestamp()
		FOR SHARE OF s`, Digest(token), tenant, actor).Scan(&actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	return actor, err
}

func (s RBAC) List(ctx context.Context, tenant, token string) (RoleList, error) {
	result := RoleList{Roles: []Role{}}
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var allowed bool
		err := tx.QueryRow(ctx, `SELECT m.role='primary_owner' OR EXISTS (
			SELECT 1 FROM hive.tenant_role_assignments a JOIN hive.tenant_role_actions p USING (tenant_id,role_id)
			WHERE a.tenant_id=m.tenant_id AND a.principal_id=m.principal_id AND p.action=$3
		) FROM hive.tenant_sessions s JOIN hive.tenant_memberships m USING (tenant_id,principal_id)
		JOIN hive.active_tenants t ON t.id=s.tenant_id
		WHERE s.digest=$1 AND s.tenant_id=$2 AND s.expires_at>clock_timestamp() AND m.active`, Digest(token), tenant, rolesRead).Scan(&allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnauthenticated
		}
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbidden
		}
		err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT revision FROM hive.tenant_rbac_revisions WHERE tenant_id=$1),0)`, tenant).Scan(&result.Revision)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, roleSelect+` WHERE r.tenant_id=$1 ORDER BY r.role_key`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var role Role
			if err = rows.Scan(&role.ID, &role.Key, &role.Name, &role.Actions); err != nil {
				return err
			}
			result.Roles = append(result.Roles, role)
		}
		return rows.Err()
	})
	return result, err
}

const roleSelect = `SELECT r.id,r.role_key,r.name,ARRAY(
	SELECT action FROM hive.tenant_role_actions a WHERE a.tenant_id=r.tenant_id AND a.role_id=r.id ORDER BY action
) FROM hive.tenant_roles r`

// Only RBAC mutations advance the revision. The compare-and-swap and audit commit together.
func advanceRevision(ctx context.Context, tx pgx.Tx, tenant string, expected *int64) (int64, error) {
	_, err := tx.Exec(ctx, `INSERT INTO hive.tenant_rbac_revisions (tenant_id) VALUES ($1) ON CONFLICT DO NOTHING`, tenant)
	if err != nil {
		return 0, err
	}
	var next int64
	err = tx.QueryRow(ctx, `UPDATE hive.tenant_rbac_revisions SET revision=revision+1
		WHERE tenant_id=$1 AND ($2::bigint IS NULL OR revision=$2) RETURNING revision`, tenant, expected).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRBACRevision
	}
	return next, err
}

func putActions(ctx context.Context, tx pgx.Tx, tenant, role string, actions []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM hive.tenant_role_actions WHERE tenant_id=$1 AND role_id=$2`, tenant, role); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO hive.tenant_role_actions (tenant_id,role_id,action)
		SELECT $1,$2,unnest($3::text[])`, tenant, role, actions)
	return err
}

func (s RBAC) Create(ctx context.Context, tenant, token, key string, input CreateRole) (RoleResult, error) {
	name, err := validateRole(input.Name, input.Actions)
	if err != nil || !roleKeyPattern.MatchString(input.Key) || input.Key == "primary_owner" || input.Key == "primary-owner" {
		return RoleResult{}, ErrRBACInput
	}
	input.Name = name
	payload, err := json.Marshal(input)
	if err != nil {
		return RoleResult{}, err
	}
	mac := hmac.New(sha256.New, s.FingerprintKey)
	mac.Write(payload)
	fingerprint := mac.Sum(nil)
	result := RoleResult{Role: Role{Key: input.Key, Name: name, Actions: input.Actions}}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := ownerActor(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.tenant_role_create_receipts (tenant_id,actor_id,request_key,fingerprint)
			VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, actor, key, fingerprint)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var previous []byte
			err = tx.QueryRow(ctx, `SELECT fingerprint,result FROM hive.tenant_role_create_receipts
				WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key).Scan(&previous, &result)
			if err != nil {
				return err
			}
			if !hmac.Equal(previous, fingerprint) {
				return ErrRBACReplay
			}
			return nil
		}
		result.Revision, err = advanceRevision(ctx, tx, tenant, nil)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO hive.tenant_roles (tenant_id,role_key,name) VALUES ($1,$2,$3) RETURNING id`,
			tenant, input.Key, name).Scan(&result.Role.ID)
		var unique *pgconn.PgError
		if errors.As(err, &unique) && unique.ConstraintName == "tenant_roles_tenant_id_role_key_key" {
			return ErrRBACConflict
		}
		if err != nil {
			return err
		}
		if err = putActions(ctx, tx, tenant, result.Role.ID, input.Actions); err != nil {
			return err
		}
		if err = audit.RecordRBAC(ctx, tx, audit.RBACEvent{Tenant: tenant, Actor: actor, Target: result.Role.ID, Action: "role.create",
			Revision: result.Revision, After: result.Role}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.tenant_role_create_receipts SET result=$4
			WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key, result)
		return err
	})
	return result, err
}

func (s RBAC) Replace(ctx context.Context, tenant, token, id string, input ReplaceRole) (RoleResult, error) {
	name, err := validateRole(input.Name, input.Actions)
	if err != nil || input.Revision == nil || *input.Revision < 0 {
		return RoleResult{}, ErrRBACInput
	}
	var result RoleResult
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := ownerActor(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		result.Revision, err = advanceRevision(ctx, tx, tenant, input.Revision)
		if err != nil {
			return err
		}
		var before Role
		err = tx.QueryRow(ctx, roleSelect+` WHERE r.tenant_id=$1 AND r.id=$2`, tenant, id).Scan(&before.ID, &before.Key, &before.Name, &before.Actions)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRBACTarget
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE hive.tenant_roles SET name=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, name); err != nil {
			return err
		}
		if err = putActions(ctx, tx, tenant, id, input.Actions); err != nil {
			return err
		}
		result.Role = Role{ID: id, Key: before.Key, Name: name, Actions: input.Actions}
		return audit.RecordRBAC(ctx, tx, audit.RBACEvent{Tenant: tenant, Actor: actor, Target: id, Action: "role.replace",
			Revision: result.Revision, Before: before, After: result.Role})
	})
	return result, err
}

func (s RBAC) Assign(ctx context.Context, tenant, token, member string, input ReplaceAssignments) (AssignmentResult, error) {
	if input.Revision == nil || *input.Revision < 0 || input.RoleIDs == nil || len(input.RoleIDs) > 100 {
		return AssignmentResult{}, ErrRBACInput
	}
	ids := slices.Clone(input.RoleIDs)
	slices.Sort(ids)
	for i, id := range ids {
		if !roleIDPattern.MatchString(id) || (i > 0 && ids[i-1] == id) {
			return AssignmentResult{}, ErrRBACInput
		}
	}
	result := AssignmentResult{MemberID: member, RoleIDs: ids}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := ownerActor(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		result.Revision, err = advanceRevision(ctx, tx, tenant, input.Revision)
		if err != nil {
			return err
		}
		var kind string
		err = tx.QueryRow(ctx, `SELECT role FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$2 AND active FOR SHARE`, tenant, member).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRBACTarget
		}
		if err != nil {
			return err
		}
		if kind == "primary_owner" {
			return ErrForbidden
		}
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM hive.tenant_roles WHERE tenant_id=$1 AND id IN (SELECT unnest($2::text[])::uuid)`, tenant, ids).Scan(&count)
		if err != nil {
			return err
		}
		if count != len(ids) {
			return ErrRBACTarget
		}
		var before []string
		err = tx.QueryRow(ctx, `SELECT ARRAY(SELECT role_id::text FROM hive.tenant_role_assignments
			WHERE tenant_id=$1 AND principal_id=$2 ORDER BY role_id)`, tenant, member).Scan(&before)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM hive.tenant_role_assignments WHERE tenant_id=$1 AND principal_id=$2`, tenant, member); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hive.tenant_role_assignments (tenant_id,principal_id,role_id)
			SELECT $1,$2,unnest($3::text[])::uuid`, tenant, member, ids)
		if err != nil {
			return err
		}
		return audit.RecordRBAC(ctx, tx, audit.RBACEvent{Tenant: tenant, Actor: actor, Target: member, Action: "membership.roles.replace",
			Revision: result.Revision, Before: before, After: ids})
	})
	return result, err
}
