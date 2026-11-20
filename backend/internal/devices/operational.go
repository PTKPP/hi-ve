package devices

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
)

type OperatorAssignment struct {
	Member string `json:"member_id"`
	Outlet string `json:"outlet_id"`
}
type AssignmentResult struct {
	Device      string               `json:"device_id"`
	Revision    int64                `json:"revision"`
	Assignments []OperatorAssignment `json:"assignments"`
}

func (s Service) ReplaceOperators(ctx context.Context, tenant, token, device, key string, expected int64, items []OperatorAssignment) (AssignmentResult, error) {
	result := AssignmentResult{Device: device}
	if items == nil || len(items) > 100 || expected < 1 {
		return result, ErrInput
	}
	items = slices.Clone(items)
	slices.SortFunc(items, func(a, b OperatorAssignment) int {
		if c := strings.Compare(a.Member, b.Member); c != 0 {
			return c
		}
		return strings.Compare(a.Outlet, b.Outlet)
	})
	outlets := make([]string, 0, len(items))
	for i, item := range items {
		if !outletID.MatchString(item.Member) || !outletID.MatchString(item.Outlet) || (i > 0 && item == items[i-1]) {
			return result, ErrInput
		}
		outlets = append(outlets, item.Outlet)
	}
	slices.Sort(outlets)
	outlets = slices.Compact(outlets)
	body, err := json.Marshal(items)
	if err != nil {
		return result, err
	}
	normalized, err := json.Marshal(struct {
		Expected    int64
		Assignments []OperatorAssignment
	}{expected, items})
	if err != nil {
		return result, err
	}
	fingerprint := keyed(s.RateKey, "operator-assignments", string(normalized))
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := s.AuthorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		// Replay first, even if an originally assigned member has since been deactivated.
		replay := func() (bool, error) {
			var previous []byte
			err := tx.QueryRow(ctx, `SELECT fingerprint,result FROM hive.device_assignment_receipts
				WHERE tenant_id=$1 AND device_id=$2 AND actor_id=$3 AND request_key=$4`, tenant, device, actor, key).Scan(&previous, &result)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			if err == nil && !hmac.Equal(previous, fingerprint) {
				err = ErrConflict
			}
			return true, err
		}
		if found, err := replay(); found || err != nil {
			return err
		}
		for i, item := range items {
			if i > 0 && items[i-1].Member == item.Member {
				continue
			}
			if err = s.LockOperatorMember(ctx, tx, tenant, item.Member); err != nil {
				return err
			}
		}
		var actual int64
		err = tx.QueryRow(ctx, `SELECT revision FROM hive.devices WHERE tenant_id=$1 AND id=$2
			AND revoked_at IS NULL FOR NO KEY UPDATE`, tenant, device).Scan(&actual)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
		if found, err := replay(); found || err != nil {
			return err
		}
		if actual != expected {
			return ErrConflict
		}
		if len(outlets) > 0 {
			ok, err := s.LockOutlets(ctx, tx, tenant, outlets)
			if err != nil {
				return err
			}
			if !ok {
				return ErrUnavailable
			}
			var count int
			err = tx.QueryRow(ctx, `SELECT count(*) FROM hive.device_outlets WHERE tenant_id=$1 AND device_id=$2
				AND outlet_id=ANY($3::text[]::uuid[])`, tenant, device, outlets).Scan(&count)
			if err != nil {
				return err
			}
			if count != len(outlets) {
				return ErrUnavailable
			}
		}
		if _, err = tx.Exec(ctx, `DELETE FROM hive.device_operator_assignments WHERE tenant_id=$1 AND device_id=$2`, tenant, device); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.device_operator_assignments(tenant_id,device_id,member_id,outlet_id)
			SELECT $1,$2,member_id,outlet_id FROM jsonb_to_recordset($3::jsonb) AS a(member_id uuid,outlet_id uuid)`, tenant, device, body); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE hive.operational_contexts SET ended_at=clock_timestamp() WHERE tenant_id=$1 AND device_id=$2 AND ended_at IS NULL`, tenant, device); err != nil {
			return err
		}
		result.Revision = actual + 1
		result.Assignments = items
		if _, err = tx.Exec(ctx, `UPDATE hive.devices SET revision=$3 WHERE tenant_id=$1 AND id=$2`, tenant, device, result.Revision); err != nil {
			return err
		}
		count := len(items)
		if err = audit.RecordDeviceContext(ctx, tx, tenant, device, actor, "assignments.replace", result.Revision, "", "", &count); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hive.device_assignment_receipts VALUES($1,$2,$3,$4,$5,$6)`, tenant, device, actor, key, fingerprint, result)
		return err
	})
	return result, err
}

// RemoveOperatorAccess runs inside the membership lifecycle transaction while it holds the member lock.
func (s Service) RemoveOperatorAccess(ctx context.Context, tx pgx.Tx, tenant, member string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM hive.device_operator_assignments WHERE tenant_id=$1 AND member_id=$2`, tenant, member); err != nil {
		return err
	}
	return s.EndMemberContexts(ctx, tx, tenant, member)
}

func (s Service) EndMemberContexts(ctx context.Context, tx pgx.Tx, tenant, member string) error {
	_, err := tx.Exec(ctx, `WITH targets AS (
        SELECT device_id,id FROM hive.operational_contexts
        WHERE tenant_id=$1 AND member_id=$2 AND ended_at IS NULL ORDER BY device_id,id FOR UPDATE
    ) UPDATE hive.operational_contexts c SET ended_at=clock_timestamp()
      FROM targets WHERE c.tenant_id=$1 AND c.device_id=targets.device_id AND c.id=targets.id`, tenant, member)
	return err
}

type OperationTarget struct {
	Operation string `json:"operation"`
	Outlet    string `json:"outlet_id,omitempty"`
	Member    string `json:"member_id,omitempty"`
	After     string `json:"page_after,omitempty"`
	ContextID string `json:"context_id,omitempty"`
	Revision  *int64 `json:"expected_revision,omitempty"`
}
type OperationChallenge struct {
	ID             string    `json:"challenge_id"`
	Nonce          string    `json:"nonce"`
	DeviceRevision int64     `json:"device_revision"`
	ExpiresAt      time.Time `json:"expires_at"`
}
type operationClaims struct {
	OperationChallenge
	Purpose string `json:"purpose"`
	Tenant  string `json:"tenant_id"`
	Device  string `json:"device_id"`
	OperationTarget
}

type bootstrapClaims struct {
	Purpose   string    `json:"purpose"`
	Tenant    string    `json:"tenant_id"`
	Device    string    `json:"device_id"`
	RequestID string    `json:"request_id"`
	IssuedAt  time.Time `json:"issued_at"`
	OperationTarget
}

const bootstrapLifetime = 30 * time.Second

func (s Service) StartOperation(ctx context.Context, tenant, device string, peer netip.Addr, target OperationTarget, proof string) (OperationChallenge, error) {
	var result OperationChallenge
	valid := validOperationTarget(target, s.PIN != nil)
	if !valid || !peer.IsValid() {
		return result, ErrInput
	}
	if proof == "" || len(proof) > 2048 {
		return result, ErrProof
	}
	// An MVCC lookup takes no device row lock. Unproven requests never start a
	// transaction that locks the device or touches either issuance quota.
	var public PublicKey
	var verifiedRevision int64
	err := s.DB.QueryRow(ctx, `SELECT d.public_key,d.revision FROM hive.devices d
		JOIN hive.active_tenants t ON t.id=d.tenant_id
		WHERE d.tenant_id=$1 AND d.id=$2 AND d.revoked_at IS NULL`, tenant, device).Scan(&public, &verifiedRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrProof
	}
	if err != nil {
		return result, err
	}
	var claims bootstrapClaims
	payload := signedPayload(public, proof)
	decoded := false
	if target.Operation == "profiles" || target.Operation == "context.login" {
		decoded = decodePINProof(payload, &claims)
	} else {
		decoded = json.Unmarshal(payload, &claims) == nil
	}
	if !decoded ||
		claims.Purpose != "hive.device.challenge.bootstrap.v1" || claims.Tenant != tenant || claims.Device != device ||
		!outletID.MatchString(claims.RequestID) || claims.IssuedAt.IsZero() ||
		!sameOperation(claims.OperationTarget, target) {
		return result, ErrProof
	}

	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return result, err
	}
	result.Nonce = base64.RawURLEncoding.EncodeToString(random)
	bootstrapExpiresAt := claims.IssuedAt.Add(bootstrapLifetime)
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		// The signed request ID is the replay identity, independent of the
		// encoding or malleability of the signature. Replays never reach the device lock.
		var first bool
		err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`,
			"device-bootstrap:"+tenant+":"+device+":"+claims.RequestID).Scan(&first)
		if err != nil {
			return err
		}
		if !first {
			return ErrProof
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.device_bootstrap_receipts(tenant_id,device_id,request_id,expires_at)
			SELECT $1,$2,$3,$5::timestamptz
			WHERE $4::timestamptz<=clock_timestamp()+interval '5 seconds'
			  AND $5::timestamptz>clock_timestamp()
			ON CONFLICT DO NOTHING`, tenant, device, claims.RequestID, claims.IssuedAt, bootstrapExpiresAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrProof
		}
		var currentKey PublicKey
		err = tx.QueryRow(ctx, `SELECT d.public_key,d.revision FROM hive.devices d JOIN hive.active_tenants t ON t.id=d.tenant_id
			WHERE d.tenant_id=$1 AND d.id=$2 AND d.revoked_at IS NULL FOR NO KEY UPDATE OF d FOR SHARE OF t`, tenant, device).Scan(&currentKey, &result.DeviceRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProof
		}
		if err != nil {
			return err
		}
		if currentKey != public || result.DeviceRevision != verifiedRevision {
			return ErrProof
		}
		if err = takeStartLimit(ctx, tx, "operation_peer", keyed(s.RateKey, "operation-peer", peer.Unmap().WithZone("").String()), "1 minute", 600); err != nil {
			return err
		}
		if err = takeStartLimit(ctx, tx, "operation_device", keyed(s.RateKey, "operation-device", tenant+device), "1 minute", 120); err != nil {
			return err
		}
		var outstanding int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM hive.device_operation_challenges
			WHERE tenant_id=$1 AND device_id=$2 AND expires_at>clock_timestamp()`, tenant, device).Scan(&outstanding)
		if err != nil {
			return err
		}
		if outstanding >= 32 {
			return ErrLimited
		}
		if target.Operation == "profiles" || target.Operation == "context.login" {
			err = tx.QueryRow(ctx, `INSERT INTO hive.device_operation_challenges
                (tenant_id,device_id,nonce,device_revision,operation,context_revision,outlet_id,member_id,page_after)
                SELECT $1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid
                WHERE $10::timestamptz>clock_timestamp() RETURNING id,expires_at`,
				tenant, device, result.Nonce, result.DeviceRevision, target.Operation, target.Revision, target.Outlet, target.Member, target.After, bootstrapExpiresAt).
				Scan(&result.ID, &result.ExpiresAt)
		} else {
			err = tx.QueryRow(ctx, `INSERT INTO hive.device_operation_challenges(tenant_id,device_id,nonce,device_revision,operation,context_id,context_revision)
			SELECT $1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7
			WHERE $8::timestamptz>clock_timestamp() RETURNING id,expires_at`,
				tenant, device, result.Nonce, result.DeviceRevision, target.Operation, target.ContextID, target.Revision, bootstrapExpiresAt).Scan(&result.ID, &result.ExpiresAt)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProof
		}
		return err
	})
	result.ExpiresAt = result.ExpiresAt.UTC()
	return result, err
}

func sameRevision(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

type OperationalState struct {
	Revision   int64   `json:"context_revision"`
	LastOutlet *string `json:"last_outlet_id"`
}
type OperationalContext struct {
	ID            string    `json:"context_id"`
	Member        string    `json:"member_id"`
	Outlet        string    `json:"outlet_id"`
	Revision      int64     `json:"revision"`
	IdleExpiresAt time.Time `json:"idle_expires_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (s Service) UseOperation(ctx context.Context, tenant, device, operation, proof, token string) (OperationalState, OperationalContext, error) {
	return s.useOperation(ctx, tenant, device, operation, proof, token, nil)
}

// ReadContext holds authorization, device, outlet and session locks through the reader.
// Only this read-only operation accepts a projection of the authorized context.
func (s Service) ReadContext(ctx context.Context, tenant, device, proof, token string, read func(pgx.Tx, OperationalContext) error) error {
	_, _, err := s.useOperation(ctx, tenant, device, "context.read", proof, token, read)
	return err
}

func (s Service) useOperation(ctx context.Context, tenant, device, operation, proof, token string, read func(pgx.Tx, OperationalContext) error) (OperationalState, OperationalContext, error) {
	var state OperationalState
	var current OperationalContext
	var claims operationClaims
	var key PublicKey
	err := s.DB.QueryRow(ctx, `SELECT d.public_key FROM hive.devices d JOIN hive.active_tenants t ON t.id=d.tenant_id
		WHERE d.tenant_id=$1 AND d.id=$2 AND d.revoked_at IS NULL`, tenant, device).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, current, ErrProof
	}
	if err != nil {
		return state, current, err
	}
	if json.Unmarshal(signedPayload(key, proof), &claims) != nil || claims.Purpose != "hive.device.operation.v1" ||
		claims.Tenant != tenant || claims.Device != device || claims.Operation != operation || !outletID.MatchString(claims.ID) {
		return state, current, ErrProof
	}
	if operation != "state" && (!outletID.MatchString(claims.ContextID) || claims.Revision == nil || token == "") {
		return state, current, ErrProof
	}
	digest := sha256.Sum256([]byte(token))
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if operation == "context.read" {
			err := tx.QueryRow(ctx, `SELECT member_id,outlet_id FROM hive.operational_contexts
				WHERE tenant_id=$1 AND device_id=$2 AND id=$3 AND digest=$4`, tenant, device, claims.ContextID, digest[:]).Scan(&current.Member, &current.Outlet)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProof
			}
			if err != nil {
				return err
			}
			if err = s.AuthorizeOperator(ctx, tx, tenant, current.Member, current.Outlet); err != nil {
				return err
			}
			if s.PINContextSchema {
				var revision *int64
				err = tx.QueryRow(ctx, `SELECT credential_revision FROM hive.operational_contexts
                    WHERE tenant_id=$1 AND device_id=$2 AND id=$3`, tenant, device, claims.ContextID).Scan(&revision)
				if err != nil {
					return err
				}
				if revision != nil {
					if s.PIN == nil {
						return ErrProof
					}
					valid, err := s.PIN.ContextCredentialActive(ctx, tx, tenant, current.Member, *revision)
					if err != nil {
						return err
					}
					if !valid {
						return ErrProof
					}
				}
			}
		}
		var revision int64
		err := tx.QueryRow(ctx, `SELECT d.revision,d.context_revision,d.last_outlet_id FROM hive.devices d
			JOIN hive.active_tenants t ON t.id=d.tenant_id WHERE d.tenant_id=$1 AND d.id=$2
			AND d.revoked_at IS NULL FOR SHARE OF d,t`, tenant, device).Scan(&revision, &state.Revision, &state.LastOutlet)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProof
		}
		if err != nil {
			return err
		}
		if revision != claims.DeviceRevision {
			return ErrProof
		}
		if operation == "context.read" {
			valid, err := s.LockOutlets(ctx, tx, tenant, []string{current.Outlet})
			if err != nil {
				return err
			}
			if !valid {
				return ErrProof
			}
		}
		if operation != "state" {
			var live bool
			err = tx.QueryRow(ctx, `SELECT id,member_id,outlet_id,revision,idle_expires_at,expires_at,
				ended_at IS NULL AND device_revision=$6 AND revision=$7 AND idle_expires_at>clock_timestamp() AND expires_at>clock_timestamp()
				FROM hive.operational_contexts WHERE tenant_id=$1 AND device_id=$2 AND id=$3 AND digest=$4 AND revision=$5 FOR UPDATE`,
				tenant, device, claims.ContextID, digest[:], claims.Revision, revision, state.Revision).
				Scan(&current.ID, &current.Member, &current.Outlet, &current.Revision, &current.IdleExpiresAt, &current.ExpiresAt, &live)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProof
			}
			if err != nil {
				return err
			}
			if operation == "context.read" {
				var assigned bool
				err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.device_operator_assignments
					WHERE tenant_id=$1 AND device_id=$2 AND outlet_id=$3 AND member_id=$4)`, tenant, device, current.Outlet, current.Member).Scan(&assigned)
				if err != nil {
					return err
				}
				if !live || !assigned || state.LastOutlet == nil || *state.LastOutlet != current.Outlet {
					return ErrProof
				}
			}
		}
		tag, err := tx.Exec(ctx, `DELETE FROM hive.device_operation_challenges WHERE tenant_id=$1 AND device_id=$2 AND id=$3
			AND nonce=$4 AND device_revision=$5 AND operation=$6 AND context_id IS NOT DISTINCT FROM NULLIF($7,'')::uuid
			AND context_revision IS NOT DISTINCT FROM $8::bigint AND expires_at=$9 AND expires_at>clock_timestamp()`,
			tenant, device, claims.ID, claims.Nonce, revision, operation, claims.ContextID, claims.Revision, claims.ExpiresAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrProof
		}
		switch operation {
		case "context.logout":
			tag, err = tx.Exec(ctx, `UPDATE hive.operational_contexts SET ended_at=clock_timestamp()
				WHERE tenant_id=$1 AND device_id=$2 AND id=$3 AND ended_at IS NULL`, tenant, device, current.ID)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			return audit.RecordDeviceContext(ctx, tx, tenant, device, current.Member, "context.logout", current.Revision, current.ID, current.Outlet, nil)
		case "context.read":
			err = tx.QueryRow(ctx, `UPDATE hive.operational_contexts SET idle_expires_at=LEAST(expires_at,clock_timestamp()+interval '15 minutes')
				WHERE tenant_id=$1 AND device_id=$2 AND id=$3 AND idle_expires_at>clock_timestamp() RETURNING idle_expires_at`,
				tenant, device, current.ID).Scan(&current.IdleExpiresAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProof
			}
			if err == nil && read != nil {
				return read(tx, current)
			}
			return err
		}
		return nil
	})
	current.ExpiresAt = current.ExpiresAt.UTC()
	current.IdleExpiresAt = current.IdleExpiresAt.UTC()
	return state, current, err
}

// EndOutletContexts runs after Locations locks the outlet for deactivation.
// Marking contexts ended prevents a later reactivation from reviving old sessions.
func (s Service) EndOutletContexts(ctx context.Context, tx pgx.Tx, tenant, outlet string) error {
	_, err := tx.Exec(ctx, `UPDATE hive.operational_contexts SET ended_at=clock_timestamp() WHERE tenant_id=$1 AND outlet_id=$2 AND ended_at IS NULL`, tenant, outlet)
	return err
}
