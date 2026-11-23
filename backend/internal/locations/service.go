package locations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
)

var (
	ErrInput             = errors.New("invalid location input")
	ErrDuplicate         = errors.New("location code already exists")
	ErrReplay            = errors.New("location idempotency conflict")
	ErrOutletUnavailable = errors.New("outlet unavailable")
	validCode            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

type Service struct {
	EndOutletContexts func(context.Context, pgx.Tx, string, string) error
	DB                *pgxpool.Pool
	FingerprintKey    []byte
	AuthorizeOwner    func(context.Context, pgx.Tx, string, string) (string, error)
}

type CreateInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type Location struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

func (s Service) Create(ctx context.Context, tenant, token, key string, input CreateInput) (Location, error) {
	input.Code, input.Name = strings.TrimSpace(input.Code), strings.TrimSpace(input.Name)
	if !validCode.MatchString(input.Code) || input.Name == "" || len(input.Name) > 128 || strings.ContainsFunc(input.Name, unicode.IsControl) ||
		(input.Type != "outlet" && input.Type != "warehouse") {
		return Location{}, ErrInput
	}
	input.Code = strings.ToLower(input.Code)
	payload, err := json.Marshal(input)
	if err != nil {
		return Location{}, err
	}
	mac := hmac.New(sha256.New, s.FingerprintKey)
	mac.Write(payload)
	fingerprint := mac.Sum(nil)
	var result Location
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := s.AuthorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.location_create_receipts
			(tenant_id,actor_id,request_key,fingerprint) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
			tenant, actor, key, fingerprint)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var previous []byte
			err = tx.QueryRow(ctx, `SELECT fingerprint,result FROM hive.location_create_receipts
				WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key).Scan(&previous, &result)
			if err != nil {
				return err
			}
			if !hmac.Equal(previous, fingerprint) {
				return ErrReplay
			}
			return nil
		}
		err = tx.QueryRow(ctx, `INSERT INTO hive.locations(tenant_id,code,name,kind)
			VALUES ($1,$2,$3,$4) RETURNING id,code,name,kind,status`, tenant, input.Code, input.Name, input.Type).
			Scan(&result.ID, &result.Code, &result.Name, &result.Type, &result.Status)
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.ConstraintName == "locations_tenant_code_unique" {
			return ErrDuplicate
		}
		if err != nil {
			return err
		}
		if err = audit.RecordLocationCreate(ctx, tx, tenant, result.ID, actor, result.Type); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.location_create_receipts SET result=$4
			WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key, result)
		return err
	})
	return result, err
}

func (s Service) List(ctx context.Context, tenant, token string) ([]Location, error) {
	result := []Location{}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,code,name,kind,status FROM hive.locations WHERE tenant_id=$1 ORDER BY code`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row Location
			if err = rows.Scan(&row.ID, &row.Code, &row.Name, &row.Type, &row.Status); err != nil {
				return err
			}
			result = append(result, row)
		}
		return rows.Err()
	})
	return result, err
}

// ResolveActiveOutlet resolves authoritative resource identity. Callers enforce user permissions separately.
func (s Service) ResolveActiveOutlet(ctx context.Context, tenant, id string) (Location, error) {
	var outlet Location
	err := s.DB.QueryRow(ctx, `SELECT l.id,l.code,l.name,l.kind,l.status FROM hive.locations l
		JOIN hive.active_tenants t ON t.id=l.tenant_id WHERE l.tenant_id=$1 AND l.id=$2 AND l.kind='outlet' AND l.status='active'`, tenant, id).
		Scan(&outlet.ID, &outlet.Code, &outlet.Name, &outlet.Type, &outlet.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Location{}, ErrOutletUnavailable
	}
	return outlet, err
}

// LockActiveOutlets checks authoritative tenant/type/status through commit.
func LockActiveOutlets(ctx context.Context, tx pgx.Tx, tenant string, ids []string) (bool, error) {
	return lockActiveLocations(ctx, tx, tenant, ids, true)
}

func LockActiveInventoryLocations(ctx context.Context, tx pgx.Tx, tenant string, ids []string) (bool, error) {
	return lockActiveLocations(ctx, tx, tenant, ids, false)
}

func lockActiveLocations(ctx context.Context, tx pgx.Tx, tenant string, ids []string, outletOnly bool) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT l.id FROM hive.locations l
		JOIN hive.active_tenants t ON t.id=l.tenant_id
		WHERE l.tenant_id=$1 AND l.id=ANY($2::text[]::uuid[]) AND (NOT $3 OR l.kind='outlet') AND l.status='active'
		ORDER BY l.id FOR SHARE OF l,t`, tenant, ids, outletOnly)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	return count == len(ids) && count > 0, rows.Err()
}

// TenantLocationIDs resolves historical location references for an already
// authorized tenant-wide reader. Inactive locations remain in tenant history.
func TenantLocationIDs(ctx context.Context, tx pgx.Tx, tenant string, ids []string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM hive.locations WHERE tenant_id=$1 AND id=ANY($2::text[]::uuid[])`, tenant, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// OutletLabel returns the historical display label to snapshot while keeping an
// active outlet and tenant eligible for posting through commit.
func OutletLabel(ctx context.Context, tx pgx.Tx, tenant, outlet string) (string, error) {
	var label string
	err := tx.QueryRow(ctx, `SELECT l.name FROM hive.locations l JOIN hive.active_tenants t ON t.id=l.tenant_id
        WHERE l.tenant_id=$1 AND l.id=$2 AND l.kind='outlet' AND l.status='active' FOR SHARE OF l,t`, tenant, outlet).Scan(&label)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrOutletUnavailable
	}
	return label, err
}
