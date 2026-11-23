package devices

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
)

var commandHash = regexp.MustCompile("^[a-f0-9]{64}$")

func validOperationTarget(target OperationTarget, pin bool) bool {
	if isBoundCommand(target.Operation) {
		return pin && commandHash.MatchString(target.CommandDigest) && outletID.MatchString(target.ContextID) && target.Revision != nil && *target.Revision > 0 && target.Outlet == "" && target.Member == "" && target.After == ""
	}
	if target.CommandDigest != "" {
		return false
	}
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

// DV3 proof payloads are flat. Reject duplicate and non-canonical field names
// before binding them to the registered operation.
func decodePINProof(payload []byte, dst any) bool {
	d := json.NewDecoder(bytes.NewReader(payload))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool)
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return false
		}
		switch name {
		case "purpose", "tenant_id", "device_id", "request_id", "issued_at", "challenge_id", "nonce",
			"device_revision", "expires_at", "operation", "outlet_id", "member_id", "page_after", "expected_revision":
		default:
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF {
		return false
	}
	strict := json.NewDecoder(bytes.NewReader(payload))
	strict.DisallowUnknownFields()
	return strict.Decode(dst) == nil
}

func sameOperation(a, b OperationTarget) bool {
	return a.CommandDigest == b.CommandDigest && a.Operation == b.Operation && a.ContextID == b.ContextID && a.Outlet == b.Outlet &&
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
	if !decodePINProof(signedPayload(key, proof), &claims) || claims.Purpose != "hive.device.operation.v1" ||
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

type pinLoginScope struct {
	identity.PINAttempt
	epoch int64
}

func (s Service) lockLoginScope(ctx context.Context, tx pgx.Tx, claims operationClaims, update bool) (pinLoginScope, error) {
	var attempt pinLoginScope
	err := s.AuthorizeOperator(ctx, tx, claims.Tenant, claims.Member, claims.Outlet)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrRBACTarget) {
		return attempt, ErrProof
	}
	if err != nil {
		return attempt, err
	}
	attempt.PINAttempt, err = s.PIN.LockCredential(ctx, tx, claims.Tenant, claims.Member)
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
	if s.TenantEpochSchema {
		// lockPINDevice already holds the tenant lock until this transaction commits.
		err = tx.QueryRow(ctx, `SELECT authorization_epoch FROM hive.tenants WHERE id=$1`, claims.Tenant).Scan(&attempt.epoch)
		if err != nil {
			return attempt, err
		}
	}
	return attempt, nil
}

func (s Service) admitPIN(ctx context.Context, claims operationClaims, peer netip.Addr) (pinLoginScope, func(), error) {
	var snapshot pinLoginScope
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
		if err = s.chargePINLimits(ctx, tx, claims, peer); err != nil {
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
			recordPINDenial(ctx, tx, claims, false)
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

// Check both buckets before charging either. An exhausted device must
// not spend another installation's direct-peer capacity.
func (s Service) chargePINLimits(ctx context.Context, tx pgx.Tx, c operationClaims, peer netip.Addr) error {
	device := keyed(s.RateKey, "pin-device", c.Tenant+c.Device)
	address := keyed(s.RateKey, "pin-peer", peer.Unmap().WithZone("").String())
	deviceRecent, err := lockLimit(ctx, tx, "pin_device", device, "1 minute", 30)
	if err != nil {
		return err
	}
	peerRecent, err := lockLimit(ctx, tx, "pin_peer", address, "1 minute", 120)
	if err != nil {
		return err
	}
	if err := consumeLimit(ctx, tx, "pin_device", device, deviceRecent); err != nil {
		return err
	}
	return consumeLimit(ctx, tx, "pin_peer", address, peerRecent)
}

func recordPINDenial(ctx context.Context, tx pgx.Tx, c operationClaims, locked bool) {
	err := pgx.BeginFunc(ctx, tx, func(savepoint pgx.Tx) error {
		if err := audit.RecordOperatorLogin(ctx, savepoint, c.Tenant, c.ID, c.Member, c.Device, c.Outlet, "denied"); err != nil {
			return err
		}
		if locked {
			return audit.RecordOperatorLogin(ctx, savepoint, c.Tenant, c.ID, c.Member, c.Device, c.Outlet, "lockout")
		}
		return nil
	})
	if err != nil {
		slog.Warn("operator PIN denial audit failed")
	}
}

func (s Service) LoginPIN(ctx context.Context, tenant, device, proof, pin string, peer netip.Addr, target OperationTarget) (OperationalContext, string, error) {
	var result OperationalContext
	if !identity.ValidPIN(pin) || !peer.IsValid() || !validOperationTarget(target, s.PIN != nil) || target.Operation != "context.login" {
		return result, "", ErrInput
	}
	claims, err := s.verifiedPINOperation(ctx, tenant, device, proof, target)
	if err != nil {
		return result, "", err
	}
	// Preserve accounting after disconnect, with a hard deadline.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	attempt, release, err := s.admitPIN(work, claims, peer)
	if err != nil {
		return result, "", err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := s.PIN.Release(cleanup, tenant, target.Member, claims.ID); err != nil {
			slog.Warn("operator PIN lease cleanup failed")
		}
	}()
	matched, err := s.PIN.Match(tenant, target.Member, pin, attempt.PINAttempt)
	release()
	if err != nil {
		return result, "", err
	}
	var token string
	err = pgx.BeginFunc(work, s.DB, func(tx pgx.Tx) error {
		current, err := s.lockLoginScope(work, tx, claims, matched)
		if err != nil {
			return err
		}
		if current.Revision() != attempt.Revision() || current.epoch != attempt.epoch {
			return ErrProof
		}
		locked, err := s.PIN.Complete(work, tx, tenant, target.Member, claims.ID, matched)
		if errors.Is(err, identity.ErrPINIneligible) {
			return ErrProof
		}
		if err != nil {
			return err
		}
		if !matched {
			recordPINDenial(work, tx, claims, locked)
			return nil
		}

		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return err
		}
		token = base64.RawURLEncoding.EncodeToString(raw)
		digest := sha256.Sum256([]byte(token))
		if _, err = tx.Exec(work, `UPDATE hive.operational_contexts SET ended_at=clock_timestamp()
            WHERE tenant_id=$1 AND device_id=$2 AND ended_at IS NULL`, tenant, device); err != nil {
			return err
		}
		var revision int64
		err = tx.QueryRow(work, `UPDATE hive.devices SET context_revision=context_revision+1,last_outlet_id=COALESCE(last_outlet_id,$3)
          WHERE tenant_id=$1 AND id=$2 AND context_revision=$4 RETURNING context_revision`, tenant, device, target.Outlet, target.Revision).Scan(&revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		result = OperationalContext{Revision: revision, Member: target.Member, Outlet: target.Outlet}
		err = tx.QueryRow(work, `INSERT INTO hive.operational_contexts(tenant_id,device_id,member_id,outlet_id,digest,
            revision,device_revision,credential_revision,idle_expires_at,expires_at)
          VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()+interval '15 minutes',clock_timestamp()+interval '8 hours')
          RETURNING id,idle_expires_at,expires_at`,
			tenant, device, target.Member, target.Outlet, digest[:], revision, claims.DeviceRevision, current.Revision()).
			Scan(&result.ID, &result.IdleExpiresAt, &result.ExpiresAt)
		if err != nil {
			return err
		}
		return audit.RecordOperatorLogin(work, tx, tenant, claims.ID, target.Member, device, target.Outlet, "success")
	})
	if err != nil {
		return OperationalContext{}, "", err
	}
	if !matched {
		return OperationalContext{}, "", ErrProof
	}
	result.IdleExpiresAt = result.IdleExpiresAt.UTC()
	result.ExpiresAt = result.ExpiresAt.UTC()
	return result, token, nil
}
