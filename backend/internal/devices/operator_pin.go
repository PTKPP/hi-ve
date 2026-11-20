package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/identity"
)

func validOperationTarget(target OperationTarget, pin bool) bool {
	switch target.Operation {
	case "profiles":
		return pin && target.ContextID == "" && target.Revision == nil && outletID.MatchString(target.Outlet) &&
			target.Member == "" && (target.After == "" || outletID.MatchString(target.After))
	case "context.login":
		return pin && target.ContextID == "" && target.Revision != nil && *target.Revision >= 0 &&
			outletID.MatchString(target.Outlet) && outletID.MatchString(target.Member) && target.After == ""
	default:
		if target.Outlet != "" || target.Member != "" || target.After != "" {
			return false
		}
		return target.Operation == "state" && target.ContextID == "" && target.Revision == nil ||
			(target.Operation == "context.read" || target.Operation == "context.logout") &&
				outletID.MatchString(target.ContextID) && target.Revision != nil && *target.Revision > 0
	}
}

func sameOperation(a, b OperationTarget) bool {
	return a.Operation == b.Operation && a.ContextID == b.ContextID && a.Outlet == b.Outlet &&
		a.Member == b.Member && a.After == b.After && sameRevision(a.Revision, b.Revision)
}

func (s Service) verifiedPINOperation(ctx context.Context, tenant, device, proof string, target OperationTarget) (operationClaims, error) {
	var claims operationClaims
	if s.PIN == nil || proof == "" || len(proof) > 2048 || !validOperationTarget(target, true) {
		return claims, ErrProof
	}
	var key PublicKey
	err := s.DB.QueryRow(ctx, `SELECT d.public_key FROM hive.devices d JOIN hive.active_tenants t ON t.id=d.tenant_id
        WHERE d.tenant_id=$1 AND d.id=$2 AND d.revoked_at IS NULL`, tenant, device).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return claims, ErrProof
	}
	if err != nil {
		return claims, err
	}
	if json.Unmarshal(signedPayload(key, proof), &claims) != nil || claims.Purpose != "hive.device.operation.v1" ||
		claims.Tenant != tenant || claims.Device != device || !sameOperation(claims.OperationTarget, target) ||
		!outletID.MatchString(claims.ID) {
		return claims, ErrProof
	}
	return claims, nil
}

func consumePINProof(ctx context.Context, tx pgx.Tx, claims operationClaims) error {
	tag, err := tx.Exec(ctx, `DELETE FROM hive.device_operation_challenges WHERE tenant_id=$1 AND device_id=$2
        AND id=$3 AND nonce=$4 AND device_revision=$5 AND operation=$6 AND outlet_id=$7
        AND member_id IS NOT DISTINCT FROM NULLIF($8,'')::uuid AND context_revision IS NOT DISTINCT FROM $9::bigint
        AND expires_at=$10 AND expires_at>clock_timestamp() AND page_after IS NOT DISTINCT FROM NULLIF($11,'')::uuid`,
		claims.Tenant, claims.Device, claims.ID, claims.Nonce, claims.DeviceRevision, claims.Operation, claims.Outlet,
		claims.Member, claims.Revision, claims.ExpiresAt, claims.After)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrProof
	}
	return err
}

func (s Service) lockPINDevice(ctx context.Context, tx pgx.Tx, claims operationClaims, update bool) (int64, error) {
	var revision, contextRevision int64
	var outlet *string
	query := `SELECT d.revision,d.context_revision,d.last_outlet_id FROM hive.devices d
        JOIN hive.active_tenants t ON t.id=d.tenant_id WHERE d.tenant_id=$1 AND d.id=$2 AND d.revoked_at IS NULL`
	if update {
		query += " FOR NO KEY UPDATE OF d FOR SHARE OF t"
	} else {
		query += " FOR SHARE OF d,t"
	}
	err := tx.QueryRow(ctx, query, claims.Tenant, claims.Device).Scan(&revision, &contextRevision, &outlet)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrProof
	}
	if err != nil {
		return 0, err
	}
	if claims.DeviceRevision != revision || (outlet != nil && *outlet != claims.Outlet) {
		return 0, ErrProof
	}
	valid, err := s.LockOutlets(ctx, tx, claims.Tenant, []string{claims.Outlet})
	if err != nil {
		return 0, err
	}
	if !valid {
		return 0, ErrProof
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.device_outlets WHERE tenant_id=$1 AND device_id=$2 AND outlet_id=$3)`,
		claims.Tenant, claims.Device, claims.Outlet).Scan(&allowed)
	if err != nil {
		return 0, err
	}
	if !allowed {
		return 0, ErrProof
	}
	return contextRevision, nil
}

type StaffProfiles struct {
	Profiles []identity.StaffProfile `json:"profiles"`
	Next     string                  `json:"next_cursor"`
}

func (s Service) Profiles(ctx context.Context, tenant, device, outlet, after, proof string) (StaffProfiles, error) {
	page := StaffProfiles{Profiles: []identity.StaffProfile{}}
	claims, err := s.verifiedPINOperation(ctx, tenant, device, proof, OperationTarget{Operation: "profiles", Outlet: outlet, After: after})
	if err != nil {
		return page, err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.lockPINDevice(ctx, tx, claims, false); err != nil {
			return err
		}
		if err := consumePINProof(ctx, tx, claims); err != nil {
			return err
		}
		var assigned []string
		err := tx.QueryRow(ctx, `SELECT ARRAY(SELECT member_id::text FROM hive.device_operator_assignments
            WHERE tenant_id=$1 AND device_id=$2 AND outlet_id=$3 AND ($4='' OR member_id>NULLIF($4,'')::uuid) ORDER BY member_id)`,
			tenant, device, outlet, after).Scan(&assigned)
		if err != nil {
			return err
		}
		items, err := s.PIN.EligibleProfiles(ctx, tx, tenant, outlet, assigned)
		if err != nil {
			return err
		}
		if len(items) > 50 {
			items = items[:50]
			page.Next = items[49].Member
		}
		page.Profiles = append(page.Profiles, items...)
		return nil
	})
	return page, err
}

func (s Service) lockLoginScope(ctx context.Context, tx pgx.Tx, claims operationClaims, update bool) (identity.PINAttempt, error) {
	attempt := identity.PINAttempt{}
	err := s.AuthorizeOperator(ctx, tx, claims.Tenant, claims.Member, claims.Outlet)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrRBACTarget) {
		return attempt, ErrProof
	}
	if err != nil {
		return attempt, err
	}
	attempt, err = s.PIN.LockCredential(ctx, tx, claims.Tenant, claims.Member)
	if errors.Is(err, identity.ErrPINIneligible) {
		return attempt, ErrProof
	}
	if err != nil {
		return attempt, err
	}
	revision, err := s.lockPINDevice(ctx, tx, claims, update)
	if err != nil {
		return attempt, err
	}
	var assigned bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.device_operator_assignments
        WHERE tenant_id=$1 AND device_id=$2 AND outlet_id=$3 AND member_id=$4)`,
		claims.Tenant, claims.Device, claims.Outlet, claims.Member).Scan(&assigned)
	if err != nil {
		return attempt, err
	}
	if !assigned {
		return attempt, ErrProof
	}
	if revision != *claims.Revision {
		return attempt, ErrConflict
	}
	return attempt, nil
}

func (s Service) admitPIN(ctx context.Context, claims operationClaims, peer netip.Addr) (identity.PINAttempt, func(), error) {
	var snapshot identity.PINAttempt
	var release func()
	var declined error
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var err error
		snapshot, err = s.lockLoginScope(ctx, tx, claims, false)
		if err != nil {
			return err
		}
		if err = consumePINProof(ctx, tx, claims); err != nil {
			return err
		}
		if err = takeStartLimit(ctx, tx, "pin_peer", keyed(s.RateKey, "pin-peer", peer.Unmap().WithZone("").String()), "1 minute", 120); err != nil {
			if errors.Is(err, ErrLimited) {
				declined = err
				return nil
			}
			return err
		}
		if err = takeStartLimit(ctx, tx, "pin_device", keyed(s.RateKey, "pin-device", claims.Tenant+claims.Device), "1 minute", 30); err != nil {
			if errors.Is(err, ErrLimited) {
				declined = err
				return nil
			}
			return err
		}
		release, err = s.PIN.TryDeviceWork(claims.Tenant + claims.Device)
		if errors.Is(err, identity.ErrPINBusy) {
			declined = ErrLimited
			return nil
		}
		if err != nil {
			return err
		}
		err = s.PIN.Claim(ctx, tx, claims.Tenant, claims.Member, claims.ID, claims.ExpiresAt)
		switch {
		case errors.Is(err, identity.ErrPINBusy):
			declined = ErrLimited
			return nil
		case errors.Is(err, identity.ErrPINIneligible):
			declined = ErrProof
			return nil
		default:
			return err
		}
	})
	if err == nil {
		err = declined
	}
	if err != nil && release != nil {
		release()
		release = nil
	}
	return snapshot, release, err
}
