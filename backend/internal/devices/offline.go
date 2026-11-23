package devices

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
)

// VerifyJournalProof authenticates evidence only. It deliberately works after
// revoke or suspend and never establishes a live operable device session.
func (s Service) VerifyJournalProof(ctx context.Context, tenant, device, purpose, lease, id string, digest [32]byte, proof string) error {
	var key PublicKey
	err := s.DB.QueryRow(ctx, `SELECT public_key FROM hive.devices WHERE tenant_id=$1 AND id=$2`, tenant, device).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProof
	}
	if err != nil {
		return err
	}
	var claims struct {
		Purpose string `json:"purpose"`
		Tenant  string `json:"tenant_id"`
		Device  string `json:"device_id"`
		Lease   string `json:"lease_id"`
		ID      string `json:"id"`
		Digest  string `json:"digest"`
	}
	if json.Unmarshal(signedPayload(key, proof), &claims) != nil ||
		claims.Purpose != purpose || claims.Tenant != tenant || claims.Device != device ||
		claims.Lease != lease || claims.ID != id || claims.Digest != hex.EncodeToString(digest[:]) {
		return ErrProof
	}
	return nil
}

// JournalPostingStatus verifies current membership, assignment and credential,
// while fencing device lifecycle and context rotation for the caller's tx.
func (s Service) JournalPostingStatus(ctx context.Context, tx pgx.Tx, tenant, device, operator, outlet, session string, revision, epoch, credential, tenantEpoch int64) (string, error) {
	eligible := true
	if err := s.AuthorizeOperator(ctx, tx, tenant, operator, outlet); err != nil {
		if !errors.Is(err, identity.ErrForbidden) && !errors.Is(err, identity.ErrRBACTarget) {
			return "", err
		}
		eligible = false
	}
	if eligible {
		var err error
		eligible, err = s.PIN.ContextCredentialActive(ctx, tx, tenant, operator, credential)
		if err != nil {
			return "", err
		}
	}
	var active, revoked, matches, assigned bool
	var currentTenantEpoch int64
	err := tx.QueryRow(ctx, `SELECT t.status='active',d.revoked_at IS NOT NULL,t.authorization_epoch,
	    d.revision=$4 AND d.context_revision=$5 AND COALESCE(d.last_outlet_id=$3,false),
	    EXISTS(SELECT 1 FROM hive.device_operator_assignments WHERE tenant_id=$1 AND device_id=$2 AND outlet_id=$3 AND member_id=$6)
	    AND EXISTS(SELECT 1 FROM hive.operational_contexts WHERE tenant_id=$1 AND device_id=$2 AND id=$7 AND ended_at IS NULL)
	    FROM hive.devices d JOIN hive.tenants t ON t.id=d.tenant_id WHERE d.tenant_id=$1 AND d.id=$2
	    FOR SHARE OF d,t`, tenant, device, outlet, revision, epoch, operator, session).Scan(&active, &revoked, &currentTenantEpoch, &matches, &assigned)
	if err != nil {
		return "", err
	}
	switch {
	case !active || currentTenantEpoch != tenantEpoch:
		return "tenant_suspended", nil
	case revoked:
		return "device_revoked", nil
	case !matches || !assigned || !eligible:
		return "authorization_changed", nil
	}
	return "", nil
}

func (s Service) JournalRevisions(ctx context.Context, tx pgx.Tx, tenant, device string, session OperationalContext) (int64, int64, error) {
	var deviceRevision, pinRevision int64
	err := tx.QueryRow(ctx, `SELECT device_revision,credential_revision FROM hive.operational_contexts
        WHERE tenant_id=$1 AND device_id=$2 AND id=$3 AND ended_at IS NULL AND credential_revision IS NOT NULL`, tenant, device, session.ID).Scan(&deviceRevision, &pinRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrProof
	}
	return deviceRevision, pinRevision, err
}

// SwitchOutlet is an Owner command against an active installation.
func (s Service) SwitchOutlet(ctx context.Context, tx pgx.Tx, tenant, device, actor, outlet string, expected int64, safe func() error) (int64, error) {
	var revision int64
	var current *string
	err := tx.QueryRow(ctx, `SELECT context_revision,last_outlet_id FROM hive.devices WHERE tenant_id=$1 AND id=$2
	    AND revoked_at IS NULL FOR NO KEY UPDATE`, tenant, device).Scan(&revision, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrProof
	}
	if err != nil {
		return 0, err
	}
	if current != nil && *current == outlet && revision == expected+1 {
		return revision, nil
	}
	if revision != expected {
		return 0, ErrConflict
	}
	if err = safe(); err != nil {
		return 0, err
	}
	active, err := s.LockOutlets(ctx, tx, tenant, []string{outlet})
	if err != nil {
		return 0, err
	}
	if !active {
		return 0, ErrProof
	}
	var assigned bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.device_outlets WHERE tenant_id=$1 AND device_id=$2 AND outlet_id=$3)`, tenant, device, outlet).Scan(&assigned)
	if err != nil {
		return 0, err
	}
	if !assigned {
		return 0, ErrProof
	}
	if _, err = tx.Exec(ctx, `UPDATE hive.operational_contexts SET ended_at=clock_timestamp() WHERE tenant_id=$1 AND device_id=$2 AND ended_at IS NULL`, tenant, device); err != nil {
		return 0, err
	}
	err = tx.QueryRow(ctx, `UPDATE hive.devices SET context_revision=context_revision+1,last_outlet_id=$3
	    WHERE tenant_id=$1 AND id=$2 RETURNING context_revision`, tenant, device, outlet).Scan(&revision)
	if err == nil {
		err = audit.RecordOfflineControl(ctx, tx, tenant, device, actor, "", "outlet.switch", "", outlet)
	}
	return revision, err
}

func (s Service) JournalDeviceRevoked(ctx context.Context, tx pgx.Tx, tenant, device string) (bool, error) {
	var revoked bool
	err := tx.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM hive.devices WHERE tenant_id=$1 AND id=$2`, tenant, device).Scan(&revoked)
	return revoked, err
}

type OutletSwitchState struct {
	DeviceRevision int64    `json:"device_revision"`
	Revoked        bool     `json:"revoked"`
	Revision       int64    `json:"revision"`
	Outlet         *string  `json:"outlet_id"`
	Assignments    []string `json:"assigned_outlets"`
}

func (s Service) ReadOutletSwitch(ctx context.Context, tx pgx.Tx, tenant, device string) (OutletSwitchState, error) {
	var out OutletSwitchState
	err := tx.QueryRow(ctx, `SELECT d.revision,d.revoked_at IS NOT NULL,d.context_revision,d.last_outlet_id,ARRAY(SELECT outlet_id::text FROM hive.device_outlets
	    WHERE tenant_id=$1 AND device_id=$2 ORDER BY outlet_id) FROM hive.devices d
	    WHERE d.tenant_id=$1 AND d.id=$2`, tenant, device).Scan(&out.DeviceRevision, &out.Revoked, &out.Revision, &out.Outlet, &out.Assignments)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrProof
	}
	return out, err
}

func (s Service) JournalTenantEpoch(ctx context.Context, tx pgx.Tx, tenant string) (int64, error) {
	var epoch int64
	err := tx.QueryRow(ctx, `SELECT authorization_epoch FROM hive.tenants WHERE id=$1 AND status='active' FOR SHARE`, tenant).Scan(&epoch)
	return epoch, err
}
