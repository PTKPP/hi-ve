package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"

	"hi-ve/backend/internal/audit"
)

var (
	ErrPINBusy = errors.New("PIN verification capacity exhausted")
	ErrPINKey  = errors.New("PIN pepper unavailable")
	pinPattern = regexp.MustCompile(`^[0-9]{6}$`)
)

func ValidPIN(pin string) bool { return pinPattern.MatchString(pin) }

type PIN struct {
	db          *pgxpool.Pool
	fingerprint []byte
	current     int
	keys        map[int][]byte
	slots       chan struct{}
	deviceJobs  sync.Map
	endContexts func(context.Context, pgx.Tx, string, string) error
}

func NewPIN(db *pgxpool.Pool, fingerprint []byte, current int, currentKey []byte, previous int, previousKey []byte, end func(context.Context, pgx.Tx, string, string) error) (*PIN, error) {
	if db == nil || end == nil || current < 1 || len(currentKey) != 32 || len(fingerprint) < 32 ||
		hmac.Equal(fingerprint, currentKey) || (previous > 0 && (hmac.Equal(fingerprint, previousKey) || hmac.Equal(currentKey, previousKey))) ||
		previous < 0 || previous >= current || (previous == 0 && len(previousKey) != 0) || (previous > 0 && len(previousKey) != 32) {
		return nil, ErrPINKey
	}
	keys := map[int][]byte{current: append([]byte(nil), currentKey...)}
	if previous > 0 {
		keys[previous] = append([]byte(nil), previousKey...)
	}
	return &PIN{db: db, fingerprint: fingerprint, current: current, keys: keys, slots: make(chan struct{}, 2), endContexts: end}, nil
}

func (s *PIN) TryWork() (func(), error) {
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, nil
	default:
		return nil, ErrPINBusy
	}
}

func (s *PIN) derive(tenant, member, pin string, salt []byte, version int) ([]byte, error) {
	key, ok := s.keys[version]
	if !ok || len(salt) != 16 {
		return nil, ErrPINKey
	}
	// UUIDs, salt and the ASCII PIN have fixed widths.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("hive.pin.online.v1\x00" + tenant + member))
	mac.Write(salt)
	mac.Write([]byte(pin))
	return argon2.IDKey(mac.Sum(nil), salt, 2, 19456, 1, 32), nil
}

type PINStatus struct {
	Member     string `json:"member_id"`
	Configured bool   `json:"pin_configured"`
	Revision   int64  `json:"credential_revision"`
}

func (s *PIN) CreateProfile(ctx context.Context, tenant, token, key, name string) (PINStatus, error) {
	name = norm.NFC.String(strings.TrimSpace(name))
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 || strings.ContainsFunc(name, unicode.IsControl) {
		return PINStatus{}, ErrRBACInput
	}
	mac := hmac.New(sha256.New, s.fingerprint)
	mac.Write([]byte("staff-profile\x00" + name))
	fingerprint := mac.Sum(nil)
	var result PINStatus
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		actor, err := AuthorizeTenantOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO hive.staff_profile_receipts VALUES ($1,$2,$3,$4,gen_random_uuid())
            ON CONFLICT DO NOTHING RETURNING member_id`, tenant, actor, key, fingerprint).Scan(&result.Member)
		if errors.Is(err, pgx.ErrNoRows) {
			var previous []byte
			if err := tx.QueryRow(ctx, `SELECT fingerprint,member_id FROM hive.staff_profile_receipts
                WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key).Scan(&previous, &result.Member); err != nil {
				return err
			}
			if !hmac.Equal(previous, fingerprint) {
				return ErrRBACReplay
			}
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.identity_principals VALUES ($1)`, result.Member); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) VALUES($1,$2,'staff')`, tenant, result.Member); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.tenant_staff_profiles VALUES ($1,$2,$3)`, tenant, result.Member, name); err != nil {
			return err
		}
		return audit.RecordStaffPIN(ctx, tx, tenant, actor, result.Member, "profile.create", 0)
	})
	return result, err
}

func pinStatus(ctx context.Context, tx pgx.Tx, tenant, member string) (PINStatus, error) {
	result := PINStatus{Member: member}
	err := tx.QueryRow(ctx, `SELECT COALESCE(c.revision,0), COALESCE(c.verifier IS NOT NULL AND c.membership_revision=m.lifecycle_revision AND m.active,false)
        FROM hive.tenant_staff_profiles p
        JOIN hive.tenant_memberships m ON m.tenant_id=p.tenant_id AND m.principal_id=p.member_id
        LEFT JOIN hive.member_pin_credentials c ON c.tenant_id=p.tenant_id AND c.member_id=p.member_id
        WHERE p.tenant_id=$1 AND p.member_id=$2`, tenant, member).Scan(&result.Revision, &result.Configured)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrRBACTarget
	}
	return result, err
}

func (s *PIN) Status(ctx context.Context, tenant, member, token string) (PINStatus, error) {
	var result PINStatus
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := AuthorizeTenantOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		var err error
		result, err = pinStatus(ctx, tx, tenant, member)
		return err
	})
	return result, err
}

func (s *PIN) Set(ctx context.Context, tenant, member, token, pin string, revision *int64) (PINStatus, error) {
	if !ValidPIN(pin) || revision == nil || *revision < 0 {
		return PINStatus{}, ErrRBACInput
	}
	previous, err := s.Status(ctx, tenant, member, token)
	if err != nil {
		return previous, err
	}
	if previous.Revision != *revision {
		return previous, ErrRBACRevision
	}
	release, err := s.TryWork()
	if err != nil {
		return previous, err
	}
	salt := make([]byte, 16)
	_, err = rand.Read(salt)
	if err != nil {
		release()
		return previous, err
	}
	verifier, err := s.derive(tenant, member, pin, salt, s.current)
	release()
	if err != nil {
		return previous, err
	}
	var result PINStatus
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		actor, err := AuthorizeTenantOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		var active bool
		var lifecycle int64
		err = tx.QueryRow(ctx, `SELECT active,lifecycle_revision FROM hive.tenant_memberships
          WHERE tenant_id=$1 AND principal_id=$2 AND role<>'primary_owner' FOR NO KEY UPDATE`, tenant, member).Scan(&active, &lifecycle)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRBACTarget
		}
		if err != nil {
			return err
		}
		if !active {
			return ErrRBACConflict
		}
		previous, err = pinStatus(ctx, tx, tenant, member)
		if err != nil {
			return err
		}
		if previous.Revision != *revision {
			return ErrRBACRevision
		}
		result = PINStatus{Member: member, Configured: true, Revision: previous.Revision + 1}
		_, err = tx.Exec(ctx, `INSERT INTO hive.member_pin_credentials(tenant_id,member_id,revision,pepper_version,salt,verifier,membership_revision) VALUES($1,$2,$3,$4,$5,$6,$7)
          ON CONFLICT(tenant_id,member_id) DO UPDATE SET revision=EXCLUDED.revision,
          pepper_version=EXCLUDED.pepper_version,salt=EXCLUDED.salt,verifier=EXCLUDED.verifier,membership_revision=EXCLUDED.membership_revision`,
			tenant, member, result.Revision, s.current, salt, verifier, lifecycle)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2`, tenant, member); err != nil {
			return err
		}
		if err = s.endContexts(ctx, tx, tenant, member); err != nil {
			return err
		}
		action := "pin.set"
		if previous.Configured {
			action = "pin.reset"
		}
		return audit.RecordStaffPIN(ctx, tx, tenant, actor, member, action, result.Revision)
	})
	return result, err
}
