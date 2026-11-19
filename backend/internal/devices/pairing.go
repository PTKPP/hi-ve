package devices

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
)

var (
	ErrInput       = errors.New("invalid pairing request")
	ErrUnavailable = errors.New("device or pairing unavailable")
	ErrConflict    = errors.New("pairing state conflict")
	ErrLimited     = errors.New("pairing limited")
	ErrProof       = errors.New("invalid pairing proof")
	digits         = regexp.MustCompile(`^[0-9]{8}$`)
	outletID       = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

type Service struct {
	DB              *pgxpool.Pool
	CodeKey         []byte
	PreviousCodeKey []byte
	RateKey         []byte
	AuthorizeOwner  func(context.Context, pgx.Tx, string, string) (string, error)
	LockOutlets     func(context.Context, pgx.Tx, string, []string) (bool, error)
}

type Challenge struct {
	ID        string    `json:"pairing_id"`
	Code      string    `json:"code"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Device struct {
	ID       string `json:"device_id"`
	Revision int64  `json:"revision"`
}

func keyed(key []byte, purpose, value string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(purpose + "\x00" + value))
	return mac.Sum(nil)
}

func codeKeyID(key []byte) string {
	return hex.EncodeToString(keyed(key, "pairing-code-key-id", "")[:16])
}

func (s Service) challengeCode(version, id string) (string, error) {
	for _, key := range [][]byte{s.CodeKey, s.PreviousCodeKey} {
		if len(key) >= 32 && codeKeyID(key) == version {
			return fmt.Sprintf("%08d", binary.BigEndian.Uint64(keyed(key, "pairing-code", id))%100_000_000), nil
		}
	}
	return "", errors.New("pairing code key unavailable")
}

func (s Service) Start(ctx context.Context, peer netip.Addr, key string, public PublicKey) (Challenge, error) {
	if public.verificationKey() == nil || !peer.IsValid() {
		return Challenge{}, ErrInput
	}
	if s.DB == nil || len(s.CodeKey) < 32 || len(s.RateKey) < 32 {
		return Challenge{}, errors.New("device pairing unavailable")
	}
	jwk, err := json.Marshal(public)
	if err != nil {
		return Challenge{}, err
	}
	// RFC 7638 names and ordering for an EC public key.
	thumb := sha256.Sum256(jwk)
	var id [24]byte
	if _, err = rand.Read(id[:]); err != nil {
		return Challenge{}, err
	}
	var result Challenge
	var version string
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO hive.device_pairings (public_key,thumbprint,start_key,code_key_id,nonce,id)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (thumbprint,start_key) DO NOTHING
			RETURNING id,code_key_id,nonce,expires_at`, public, thumb[:], key, codeKeyID(s.CodeKey), rand.Text(), hex.EncodeToString(id[:])).
			Scan(&result.ID, &version, &result.Nonce, &result.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `SELECT id,code_key_id,nonce,expires_at FROM hive.device_pairings
				WHERE thumbprint=$1 AND start_key=$2 AND expires_at > clock_timestamp()`, thumb[:], key).
				Scan(&result.ID, &version, &result.Nonce, &result.ExpiresAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrUnavailable
			}
			return err
		}
		if err != nil {
			return err
		}
		if err = takeStartLimit(ctx, tx, "peer", keyed(s.RateKey, "pairing-peer", peer.Unmap().WithZone("").String()), "1 minute", 10); err != nil {
			return err
		}
		return takeStartLimit(ctx, tx, "key", thumb[:], "10 minutes", 3)
	})
	if err != nil {
		return Challenge{}, err
	}
	result.ExpiresAt = result.ExpiresAt.UTC()
	result.Code, err = s.challengeCode(version, result.ID)
	return result, err
}

// Lock the bucket before reading the server clock so waiters cannot reuse an old window.
func lockLimit(ctx context.Context, tx pgx.Tx, scope string, subject []byte, window string, max int) ([]time.Time, error) {
	_, err := tx.Exec(ctx, `INSERT INTO hive.device_pairing_limits AS l (scope,subject)
		VALUES ($1,$2) ON CONFLICT (scope,subject) DO UPDATE SET updated_at=l.updated_at`, scope, subject)
	if err != nil {
		return nil, err
	}
	var recent []time.Time
	err = tx.QueryRow(ctx, `SELECT ARRAY(
		SELECT attempted FROM unnest(attempts) AS attempted WHERE attempted>clock_timestamp()-$3::interval
	) FROM hive.device_pairing_limits WHERE scope=$1 AND subject=$2`, scope, subject, window).Scan(&recent)
	if err == nil && len(recent) >= max {
		return nil, ErrLimited
	}
	return recent, err
}

func consumeLimit(ctx context.Context, tx pgx.Tx, scope string, subject []byte, recent []time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE hive.device_pairing_limits SET attempts=array_append($3,clock_timestamp()),
		updated_at=clock_timestamp() WHERE scope=$1 AND subject=$2`, scope, subject, recent)
	return err
}

func takeStartLimit(ctx context.Context, tx pgx.Tx, scope string, subject []byte, window string, max int) error {
	recent, err := lockLimit(ctx, tx, scope, subject, window, max)
	if err != nil {
		return err
	}
	return consumeLimit(ctx, tx, scope, subject, recent)
}

type pairing struct {
	public                          PublicKey
	thumb, fingerprint, redeemProof []byte
	tenant, actor, version, nonce   string
	approvalKey, redeemKey          string
	expiry                          time.Time
	badCodes, badProofs             int
}

func lockPairing(ctx context.Context, tx pgx.Tx, id string) (pairing, error) {
	var p pairing
	err := tx.QueryRow(ctx, `SELECT public_key,thumbprint,COALESCE(tenant_id::text,''),COALESCE(actor_id::text,''),
		code_key_id,nonce,expires_at,COALESCE(approval_key,''),approval_fingerprint,COALESCE(redeem_key,''),redeem_proof,
		bad_codes,bad_proofs FROM hive.device_pairings WHERE id=$1 FOR NO KEY UPDATE`, id).
		Scan(&p.public, &p.thumb, &p.tenant, &p.actor, &p.version, &p.nonce, &p.expiry, &p.approvalKey, &p.fingerprint, &p.redeemKey,
			&p.redeemProof, &p.badCodes, &p.badProofs)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrUnavailable
	}
	return p, err
}

func requireLive(ctx context.Context, tx pgx.Tx, expiry time.Time) error {
	var live bool
	err := tx.QueryRow(ctx, `SELECT $1::timestamptz > clock_timestamp()`, expiry).Scan(&live)
	if err == nil && !live {
		return ErrUnavailable
	}
	return err
}

type Approval struct {
	Code    string   `json:"code"`
	Outlets []string `json:"outlet_ids"`
}

func (s Service) Approve(ctx context.Context, tenant, token, id, key string, input Approval) error {
	if s.DB == nil {
		return errors.New("device pairing unavailable")
	}
	if !digits.MatchString(input.Code) || len(input.Outlets) == 0 {
		return ErrInput
	}
	input.Outlets = slices.Clone(input.Outlets)
	slices.Sort(input.Outlets)
	for i, outlet := range input.Outlets {
		if !outletID.MatchString(outlet) || (i > 0 && input.Outlets[i-1] == outlet) {
			return ErrInput
		}
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	fingerprint := keyed(s.RateKey, "pairing-approval", string(payload))
	var rejected error
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := s.AuthorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		p, err := lockPairing(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.tenant != "" && p.tenant != tenant {
			return ErrUnavailable
		}
		if err = requireLive(ctx, tx, p.expiry); err != nil {
			return err
		}
		if p.tenant != "" {
			if p.actor == actor && p.approvalKey == key && hmac.Equal(p.fingerprint, fingerprint) {
				return nil
			}
			return ErrConflict
		}
		if p.badCodes >= 5 {
			return ErrLimited
		}
		subject := keyed(s.RateKey, "pairing-owner", tenant+"/"+actor)
		recent, err := lockLimit(ctx, tx, "owner", subject, "10 minutes", 5)
		if err != nil {
			return err
		}
		if err = requireLive(ctx, tx, p.expiry); err != nil {
			return err
		}
		code, err := s.challengeCode(p.version, id)
		if err != nil {
			return err
		}
		if !hmac.Equal([]byte(code), []byte(input.Code)) {
			rejected = ErrProof
			if err = consumeLimit(ctx, tx, "owner", subject, recent); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE hive.device_pairings SET bad_codes=bad_codes+1 WHERE id=$1`, id)
			return err
		}
		valid, err := s.LockOutlets(ctx, tx, tenant, input.Outlets)
		if err != nil {
			return err
		}
		if !valid {
			return ErrUnavailable
		}
		if err = requireLive(ctx, tx, p.expiry); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.device_pairings SET tenant_id=$2,actor_id=$3,approval_key=$4,approval_fingerprint=$5
			WHERE id=$1`, id, tenant, actor, key, fingerprint)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hive.device_pairing_outlets (pairing_id,tenant_id,outlet_id)
			SELECT $1,$2,outlet::uuid FROM unnest($3::text[]) AS outlet`, id, tenant, input.Outlets)
		if err != nil {
			return err
		}
		return audit.RecordDevicePairing(ctx, tx, "approved", tenant, actor, id, "")
	})
	if err != nil {
		return err
	}
	return rejected
}

func (s Service) Redeem(ctx context.Context, id, key, proof string) (Device, error) {
	if s.DB == nil {
		return Device{}, errors.New("device pairing unavailable")
	}
	var result Device
	var rejected error
	proofHash := sha256.Sum256([]byte(proof))
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		p, err := lockPairing(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = requireLive(ctx, tx, p.expiry); err != nil {
			return err
		}
		if p.badProofs >= 5 && p.redeemKey == "" {
			return ErrLimited
		}
		if !verifyProof(p.public, proof, id, p.nonce, key, p.expiry) {
			if p.redeemKey != "" {
				return ErrProof
			}
			rejected = ErrProof
			_, err = tx.Exec(ctx, `UPDATE hive.device_pairings SET bad_proofs=bad_proofs+1 WHERE id=$1`, id)
			return err
		}
		if p.tenant == "" {
			return ErrConflict
		}
		if p.redeemKey != "" {
			if p.redeemKey != key || !hmac.Equal(p.redeemProof, proofHash[:]) {
				return ErrConflict
			}
			err = tx.QueryRow(ctx, `SELECT id,revision FROM hive.devices
				WHERE tenant_id=$1 AND pairing_id=$2 AND revoked_at IS NULL FOR SHARE`, p.tenant, id).
				Scan(&result.ID, &result.Revision)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrUnavailable
			}
			if err != nil {
				return err
			}
			return requireLive(ctx, tx, p.expiry)
		}
		rows, err := tx.Query(ctx, `SELECT outlet_id::text FROM hive.device_pairing_outlets WHERE tenant_id=$1 AND pairing_id=$2`, p.tenant, id)
		if err != nil {
			return err
		}
		outlets, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		valid, err := s.LockOutlets(ctx, tx, p.tenant, outlets)
		if err != nil {
			return err
		}
		if !valid {
			return ErrUnavailable
		}
		err = tx.QueryRow(ctx, `INSERT INTO hive.devices (tenant_id,pairing_id,public_key,thumbprint)
			VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING id,revision`, p.tenant, id, p.public, p.thumb).
			Scan(&result.ID, &result.Revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if err = requireLive(ctx, tx, p.expiry); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hive.device_outlets (tenant_id,device_id,outlet_id)
			SELECT tenant_id,$2,outlet_id FROM hive.device_pairing_outlets WHERE tenant_id=$1 AND pairing_id=$3`,
			p.tenant, result.ID, id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.device_pairings SET redeem_key=$2,redeem_proof=$3 WHERE id=$1`, id, key, proofHash[:])
		if err != nil {
			return err
		}
		return audit.RecordDevicePairing(ctx, tx, "redeemed", p.tenant, p.actor, id, result.ID)
	})
	if err != nil {
		return Device{}, err
	}
	return result, rejected
}

func (s Service) Revoke(ctx context.Context, tenant, token, device, key string, expected int64) (Device, error) {
	if expected < 1 {
		return Device{}, ErrInput
	}
	if s.DB == nil {
		return Device{}, errors.New("device pairing unavailable")
	}
	result := Device{ID: device}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := s.AuthorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		var pairing string
		var revoked bool
		err = tx.QueryRow(ctx, `SELECT pairing_id,revision,revoked_at IS NOT NULL FROM hive.devices
			WHERE tenant_id=$1 AND id=$2 FOR NO KEY UPDATE`, tenant, device).Scan(&pairing, &result.Revision, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
		var previous int64
		err = tx.QueryRow(ctx, `SELECT expected_revision,result_revision FROM hive.device_revoke_receipts
			WHERE tenant_id=$1 AND device_id=$2 AND actor_id=$3 AND request_key=$4`, tenant, device, actor, key).
			Scan(&previous, &result.Revision)
		if err == nil {
			if previous != expected {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if result.Revision != expected || revoked {
			return ErrConflict
		}
		err = tx.QueryRow(ctx, `UPDATE hive.devices SET revoked_at=clock_timestamp(),revision=revision+1
			WHERE tenant_id=$1 AND id=$2 RETURNING revision`, tenant, device).Scan(&result.Revision)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hive.device_revoke_receipts
			(tenant_id,device_id,actor_id,request_key,expected_revision,result_revision) VALUES ($1,$2,$3,$4,$5,$6)`,
			tenant, device, actor, key, expected, result.Revision)
		if err != nil {
			return err
		}
		return audit.RecordDevicePairing(ctx, tx, "revoked", tenant, actor, pairing, device)
	})
	return result, err
}

func (s Service) Cleanup(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `WITH expired AS (
		SELECT id FROM hive.device_pairings WHERE expires_at<=statement_timestamp()
		ORDER BY expires_at,id LIMIT 500 FOR UPDATE SKIP LOCKED
	) DELETE FROM hive.device_pairings p USING expired e WHERE p.id=e.id`)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `WITH expired AS (
		SELECT scope,subject FROM hive.device_pairing_limits WHERE updated_at<=statement_timestamp()-interval '20 minutes'
		ORDER BY updated_at LIMIT 500 FOR UPDATE SKIP LOCKED
	) DELETE FROM hive.device_pairing_limits l USING expired e
	WHERE (l.scope,l.subject)=(e.scope,e.subject)`)
	return err
}
