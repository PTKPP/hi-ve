package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
)

const CatalogPriceRead = "catalog.pricing.read"
const DeviceOperatorUse = "devices.operators.use"

type LocationAssignment struct {
	Role     string `json:"role_id"`
	Location string `json:"location_id"`
}

type LocationPolicy[T any] struct {
	Values   []T
	Revision int64
}

func lockLocationMember(ctx context.Context, tx pgx.Tx, tenant, member string, exclusive bool) error {
	q := `SELECT role FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$2 AND active FOR SHARE`
	if exclusive {
		q = `SELECT role FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$2 AND active FOR NO KEY UPDATE`
	}
	var role string
	err := tx.QueryRow(ctx, q, tenant, member).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRBACTarget
	}
	if err == nil && role == "primary_owner" {
		return ErrForbidden
	}
	return err
}

// AuthorizeLocationGrant checks the exact grant after locking its membership and supporting roles.
func AuthorizeLocationGrant(ctx context.Context, tx pgx.Tx, tenant, member, location, action string) error {
	if action != DeviceOperatorUse && action != CatalogPriceRead && !IsInventoryLocationAction(action) && !IsSalesLocationAction(action) {
		return ErrForbidden
	}
	if err := lockLocationMember(ctx, tx, tenant, member, false); err != nil {
		if errors.Is(err, ErrRBACTarget) {
			return ErrForbidden
		}
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT r.id FROM hive.tenant_roles r
		JOIN hive.tenant_location_role_assignments a ON (a.tenant_id,a.role_id)=(r.tenant_id,r.id)
		WHERE a.tenant_id=$1 AND a.member_id=$2 AND a.location_id=$3 ORDER BY r.id FOR SHARE OF r`, tenant, member, location); err != nil {
		return err
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hive.tenant_location_role_assignments a
		JOIN hive.tenant_role_location_actions p USING(tenant_id,role_id)
		JOIN hive.active_tenants t ON t.id=a.tenant_id
		WHERE a.tenant_id=$1 AND a.member_id=$2 AND a.location_id=$3 AND p.action=$4)`, tenant, member, location, action).Scan(&allowed)
	if err == nil && !allowed {
		return ErrForbidden
	}
	return err
}

func (s RBAC) LocationActions(ctx context.Context, tenant, token, target, key string, expected *int64, actions []string) (LocationPolicy[string], error) {
	const op = "role.location_actions.replace"
	if key != "" && (expected == nil || *expected < 0 || actions == nil) {
		return LocationPolicy[string]{}, ErrRBACInput
	}
	actions = slices.Clone(actions)
	slices.Sort(actions)
	for i, action := range actions {
		if (action != DeviceOperatorUse && !(s.CatalogPrices && action == CatalogPriceRead) && !(s.InventoryActions && IsInventoryLocationAction(action)) && !(s.SalesActions && IsSalesLocationAction(action))) || (i > 0 && actions[i-1] == action) {
			return LocationPolicy[string]{}, ErrRBACInput
		}
	}
	return locationPolicy(ctx, s, tenant, token, target, key, op, expected, actions,
		func(tx pgx.Tx, write bool) ([]string, error) {
			q := `SELECT id FROM hive.tenant_roles WHERE tenant_id=$1 AND id=$2`
			if write {
				q += ` FOR NO KEY UPDATE`
			}
			var id string
			if err := tx.QueryRow(ctx, q, tenant, target).Scan(&id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					err = ErrRBACTarget
				}
				return nil, err
			}
			var before []string
			err := tx.QueryRow(ctx, `SELECT ARRAY(SELECT action FROM hive.tenant_role_location_actions
				WHERE tenant_id=$1 AND role_id=$2 ORDER BY action)`, tenant, target).Scan(&before)
			if err != nil || !write {
				return before, err
			}
			if _, err = tx.Exec(ctx, `DELETE FROM hive.tenant_role_location_actions WHERE tenant_id=$1 AND role_id=$2`, tenant, target); err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO hive.tenant_role_location_actions SELECT $1,$2,unnest($3::text[])`, tenant, target, actions)
			return before, err
		})
}

func loadLocationAssignments(ctx context.Context, tx pgx.Tx, tenant, member string) ([]LocationAssignment, error) {
	var items []LocationAssignment
	err := tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('role_id',role_id,'location_id',location_id)
		ORDER BY role_id,location_id),'[]'::jsonb) FROM hive.tenant_location_role_assignments WHERE tenant_id=$1 AND member_id=$2`, tenant, member).Scan(&items)
	return items, err
}

func (s RBAC) LocationAssignments(ctx context.Context, tenant, token, target, key string, expected *int64, assignments []LocationAssignment) (LocationPolicy[LocationAssignment], error) {
	const op = "membership.location_roles.replace"
	if key != "" && (expected == nil || *expected < 0 || assignments == nil || len(assignments) > 100) {
		return LocationPolicy[LocationAssignment]{}, ErrRBACInput
	}
	items := slices.Clone(assignments)
	slices.SortFunc(items, func(a, b LocationAssignment) int {
		if n := strings.Compare(a.Role, b.Role); n != 0 {
			return n
		}
		return strings.Compare(a.Location, b.Location)
	})
	for i, a := range items {
		if !roleIDPattern.MatchString(a.Role) || !roleIDPattern.MatchString(a.Location) || (i > 0 && a == items[i-1]) {
			return LocationPolicy[LocationAssignment]{}, ErrRBACInput
		}
	}
	return locationPolicy(ctx, s, tenant, token, target, key, op, expected, items,
		func(tx pgx.Tx, write bool) ([]LocationAssignment, error) {
			if err := lockLocationMember(ctx, tx, tenant, target, write); err != nil {
				return nil, err
			}
			before, err := loadLocationAssignments(ctx, tx, tenant, target)
			if err != nil || !write {
				return before, err
			}
			if len(items) > 0 {
				locations := make([]string, 0, len(items))
				roles := make([]string, 0, len(items))
				for _, a := range items {
					locations = append(locations, a.Location)
					roles = append(roles, a.Role)
				}
				slices.Sort(locations)
				lockLocations := s.LockOutlets
				if s.InventoryActions && s.LockInventoryLocations != nil {
					lockLocations = s.LockInventoryLocations
				}
				ok, err := lockLocations(ctx, tx, tenant, slices.Compact(locations))
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, ErrRBACTarget
				}
				roles = slices.Compact(roles)
				var count int
				err = tx.QueryRow(ctx, `SELECT count(DISTINCT role_id) FROM hive.tenant_role_location_actions
					WHERE tenant_id=$1 AND role_id=ANY($2::text[]::uuid[])`, tenant, roles).Scan(&count)
				if err != nil {
					return nil, err
				}
				if count != len(roles) {
					return nil, ErrRBACTarget
				}
			}
			if _, err = tx.Exec(ctx, `DELETE FROM hive.tenant_location_role_assignments WHERE tenant_id=$1 AND member_id=$2`, tenant, target); err != nil {
				return nil, err
			}
			payload, err := json.Marshal(items)
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO hive.tenant_location_role_assignments(tenant_id,member_id,role_id,location_id)
				SELECT $1,$2,role_id,location_id FROM jsonb_to_recordset($3::jsonb) AS a(role_id uuid,location_id uuid)`, tenant, target, payload)
			return before, err
		})
}

func locationPolicy[T any](ctx context.Context, s RBAC, tenant, token, target, key, operation string, expected *int64, values []T,
	change func(pgx.Tx, bool) ([]T, error)) (LocationPolicy[T], error) {
	var result LocationPolicy[T]
	var opts pgx.TxOptions
	if key == "" {
		opts.IsoLevel = pgx.RepeatableRead
	}
	err := pgx.BeginTxFunc(ctx, s.DB, opts, func(tx pgx.Tx) error {
		actor, err := AuthorizeTenantOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		if key == "" {
			result.Values, err = change(tx, false)
			if err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT COALESCE((SELECT revision FROM hive.tenant_rbac_revisions WHERE tenant_id=$1),0)`, tenant).Scan(&result.Revision)
		}
		payload, err := json.Marshal(struct {
			Revision *int64
			Values   []T
		}{expected, values})
		if err != nil {
			return err
		}
		mac := hmac.New(sha256.New, s.FingerprintKey)
		mac.Write(payload)
		fingerprint := mac.Sum(nil)
		args := []any{tenant, actor, target, operation, key}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.location_rbac_receipts(tenant_id,actor_id,target_id,operation,request_key,fingerprint)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, append(args, fingerprint)...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var saved []byte
			err = tx.QueryRow(ctx, `SELECT fingerprint,result FROM hive.location_rbac_receipts
				WHERE tenant_id=$1 AND actor_id=$2 AND target_id=$3 AND operation=$4 AND request_key=$5`, args...).Scan(&saved, &result)
			if err == nil && !hmac.Equal(saved, fingerprint) {
				return ErrRBACReplay
			}
			return err
		}
		result.Revision, err = advanceRevision(ctx, tx, tenant, expected)
		if err != nil {
			return err
		}
		before, err := change(tx, true)
		if err != nil {
			return err
		}
		result.Values = values
		if err = audit.RecordRBAC(ctx, tx, audit.RBACEvent{Tenant: tenant, Actor: actor, Target: target, Action: operation, Revision: result.Revision, Before: before, After: values}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.location_rbac_receipts SET result=$6
			WHERE tenant_id=$1 AND actor_id=$2 AND target_id=$3 AND operation=$4 AND request_key=$5`, append(args, result)...)
		return err
	})
	return result, err
}

// LockOperatorMember confirms eligibility without granting any location action.
func LockOperatorMember(ctx context.Context, tx pgx.Tx, tenant, member string) error {
	return lockLocationMember(ctx, tx, tenant, member, false)
}

func AuthorizeDeviceOperator(ctx context.Context, tx pgx.Tx, tenant, member, outlet string) error {
	return AuthorizeLocationGrant(ctx, tx, tenant, member, outlet, DeviceOperatorUse)
}

func IsInventoryLocationAction(action string) bool {
	switch action {
	case "inventory.balance.read",
		"inventory.traceability.read",
		"inventory.receiving.create",
		"inventory.receiving.post",
		"inventory.repacking.create",
		"inventory.repacking.post",
		"inventory.transfer.create",
		"inventory.transfer.send",
		"inventory.transfer.quick",
		"inventory.transfer.receive",
		"inventory.transfer.record_discrepancy",
		"inventory.transfer.approve_discrepancy",
		"inventory.adjustment.create",
		"inventory.adjustment.read",
		"inventory.adjustment.approve",
		"inventory.stock_count.create",
		"inventory.stock_count.observe",
		"inventory.stock_count.approve":
		return true
	default:
		return false
	}
}

func IsSalesLocationAction(action string) bool {
	switch action {
	case "sales.checkout.post", "sales.transactions.discount", "sales.payments.confirm", "sales.history.read", "sales.receipts.read", "sales.cashier.search",
		"sales.returns.create", "sales.returns.approve", "sales.voids.create", "sales.voids.approve", "sales.payments.refund_confirm":
		return true
	default:
		return false
	}
}
