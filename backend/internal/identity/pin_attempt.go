package identity

import (
	"context"
	"crypto/hmac"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrPINIneligible = errors.New("PIN credential not eligible")

type PINAttempt struct {
	revision       int64
	pepper         int
	salt, verifier []byte
}

func (s *PIN) LockCredential(ctx context.Context, tx pgx.Tx, tenant, member string) (PINAttempt, error) {
	var out PINAttempt
	err := tx.QueryRow(ctx, `SELECT revision,pepper_version,salt,verifier FROM hive.member_pin_credentials
        WHERE tenant_id=$1 AND member_id=$2 AND verifier IS NOT NULL FOR NO KEY UPDATE`, tenant, member).
		Scan(&out.revision, &out.pepper, &out.salt, &out.verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrPINIneligible
	}
	if err == nil && s.keys[out.pepper] == nil {
		return out, ErrPINIneligible
	}
	return out, err
}

func (s *PIN) Match(tenant, member, pin string, attempt PINAttempt) (bool, error) {
	hash, err := s.derive(tenant, member, pin, attempt.salt, attempt.pepper)
	return err == nil && hmac.Equal(hash, attempt.verifier), err
}

func (a PINAttempt) Revision() int64 { return a.revision }

func (s *PIN) Claim(ctx context.Context, tx pgx.Tx, tenant, member, id string, expires time.Time) error {
	tag, err := tx.Exec(ctx, `INSERT INTO hive.member_pin_attempt_state AS state(tenant_id,member_id,attempt_id,lease_until)
        SELECT $1,$2,$3,LEAST($4::timestamptz,clock_timestamp()+interval '10 seconds')
        WHERE $4>clock_timestamp()
        ON CONFLICT(tenant_id,member_id) DO UPDATE SET attempt_id=EXCLUDED.attempt_id,lease_until=EXCLUDED.lease_until,
          failures=CASE WHEN state.locked_until IS NULL THEN state.failures ELSE 0 END,locked_until=NULL
        WHERE (state.lease_until IS NULL OR state.lease_until<=clock_timestamp())
          AND (state.locked_until IS NULL OR state.locked_until<=clock_timestamp())`, tenant, member, id, expires)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	var locked bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(locked_until>clock_timestamp(),false) FROM hive.member_pin_attempt_state
        WHERE tenant_id=$1 AND member_id=$2`, tenant, member).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) || locked {
		return ErrPINIneligible
	}
	if err != nil {
		return err
	}
	return ErrPINBusy
}

func (s *PIN) Complete(ctx context.Context, tx pgx.Tx, tenant, member, id string, matched bool) (bool, error) {
	var locked bool
	err := tx.QueryRow(ctx, `UPDATE hive.member_pin_attempt_state
        SET failures=CASE WHEN $4::boolean THEN 0 ELSE failures+1 END,
            locked_until=CASE WHEN NOT $4 AND failures=4 THEN clock_timestamp()+interval '15 minutes' END,
            attempt_id=NULL,lease_until=NULL
        WHERE tenant_id=$1 AND member_id=$2 AND attempt_id=$3 AND lease_until>clock_timestamp()
        RETURNING locked_until IS NOT NULL`, tenant, member, id, matched).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrPINIneligible
	}
	return locked, err
}

func (s *PIN) Release(ctx context.Context, tenant, member, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE hive.member_pin_attempt_state SET attempt_id=NULL,lease_until=NULL
        WHERE tenant_id=$1 AND member_id=$2 AND attempt_id=$3`, tenant, member, id)
	return err
}

type StaffProfile struct {
	Member string `json:"member_id"`
	Name   string `json:"display_name"`
}

func (s *PIN) EligibleProfiles(ctx context.Context, tx pgx.Tx, tenant, outlet string, assigned []string) ([]StaffProfile, error) {
	versions := []int32{int32(s.current)}
	for version := range s.keys {
		if version != s.current {
			versions = append(versions, int32(version))
		}
	}
	rows, err := tx.Query(ctx, `SELECT p.member_id,p.display_name FROM hive.tenant_staff_profiles p
        JOIN hive.member_pin_credentials c USING(tenant_id,member_id)
        JOIN hive.tenant_memberships m ON m.tenant_id=p.tenant_id AND m.principal_id=p.member_id
        WHERE p.tenant_id=$1 AND p.member_id=ANY($2::text[]::uuid[]) AND m.active AND m.role<>'primary_owner'
          AND c.verifier IS NOT NULL AND c.pepper_version=ANY($4::int[])
          AND EXISTS (SELECT 1 FROM hive.tenant_location_role_assignments a
            JOIN hive.tenant_role_location_actions r USING(tenant_id,role_id)
            WHERE a.tenant_id=p.tenant_id AND a.member_id=p.member_id AND a.location_id=$3 AND r.action='devices.operators.use')
        ORDER BY p.member_id LIMIT 51`, tenant, assigned, outlet, versions)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[StaffProfile])
}

func (s *PIN) ContextCredentialActive(ctx context.Context, tx pgx.Tx, tenant, member string, revision int64) (bool, error) {
	var version int
	err := tx.QueryRow(ctx, `SELECT pepper_version FROM hive.member_pin_credentials WHERE tenant_id=$1 AND member_id=$2
       AND revision=$3 AND verifier IS NOT NULL`, tenant, member, revision).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && s.keys[version] != nil, err
}

func (s *PIN) TryDeviceWork(device string) (func(), error) {
	if _, loaded := s.deviceJobs.LoadOrStore(device, struct{}{}); loaded {
		return nil, ErrPINBusy
	}
	release, err := s.TryWork()
	if err != nil {
		s.deviceJobs.Delete(device)
		return nil, err
	}
	return func() { release(); s.deviceJobs.Delete(device) }, nil
}
