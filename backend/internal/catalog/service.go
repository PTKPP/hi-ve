package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInput = errors.New("invalid catalog input")
var ErrConflict = errors.New("catalog revision, identity or idempotency conflict")
var ErrUnavailable = errors.New("catalog resource unavailable")
var codePattern = regexp.MustCompile("^[a-z0-9][a-z0-9_-]{0,63}$")

type Service struct {
	CheckCapability func(context.Context, pgx.Tx, string, string) error
	DB              *pgxpool.Pool
	AuthorizeOwner  func(context.Context, pgx.Tx, string, string) (string, error)
	LockOutlets     func(context.Context, pgx.Tx, string, []string) (bool, error)
}

type ProductInput struct {
	Revision *int64 `json:"revision"`
	Name     string `json:"name"`
	Category string `json:"category"`
}
type SKUInput struct {
	Revision     *int64  `json:"revision"`
	Product      string  `json:"product_id"`
	Code         string  `json:"code"`
	Variant      string  `json:"variant"`
	Barcode      *string `json:"barcode"`
	Unit         string  `json:"unit"`
	Active       *bool   `json:"active"`
	BatchTracked *bool   `json:"batch_tracked"`
}

func label(s string) bool {
	return strings.TrimSpace(s) == s && len(s) > 0 && len(s) <= 128 && !strings.ContainsFunc(s, unicode.IsControl)
}

func (s Service) withOwner(ctx context.Context, tenant, token string, apply func(pgx.Tx, string) (int64, error)) (int64, error) {
	var revision int64
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := s.AuthorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		revision, err = apply(tx, actor)
		return err
	})
	return revision, err
}

// write records a mutation in the same transaction as the caller's live authorization.
func (s Service) write(ctx context.Context, tx pgx.Tx, tenant, actor, key, capability, operation, id string, body any, fn func(pgx.Tx, string) (int64, error)) (int64, error) {
	payload, err := json.Marshal([]any{operation, id, body})
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(payload)
	if err = s.CheckCapability(ctx, tx, tenant, capability); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO hive.catalog_write_receipts(tenant_id,actor_id,request_key,fingerprint) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, actor, key, digest[:])
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		var previous []byte
		var revision int64
		err = tx.QueryRow(ctx, `SELECT fingerprint, revision FROM hive.catalog_write_receipts WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key).Scan(&previous, &revision)
		if err == nil && !bytes.Equal(previous, digest[:]) {
			return 0, ErrConflict
		}
		return revision, err
	}
	revision, err := fn(tx, actor)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || errors.As(err, &pgerr) && (pgerr.Code == "23505" || pgerr.Code == "23503") {
			return 0, ErrConflict
		}
		if errors.As(err, &pgerr) && pgerr.Code == "22P02" {
			return 0, ErrInput
		}
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE hive.catalog_write_receipts SET revision=$4 WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key, revision)
	return revision, err
}

func (s Service) PutProduct(ctx context.Context, tenant, token, key, id string, in ProductInput) (int64, error) {
	return s.withOwner(ctx, tenant, token, func(tx pgx.Tx, actor string) (int64, error) {
		return s.PutProductTx(ctx, tx, tenant, actor, key, id, in)
	})
}

func (s Service) PutProductTx(ctx context.Context, tx pgx.Tx, tenant, actor, key, id string, in ProductInput) (int64, error) {
	if in.Revision == nil || *in.Revision < 0 || !label(in.Name) || !label(in.Category) {
		return 0, ErrInput
	}
	return s.write(ctx, tx, tenant, actor, key, "catalog.products", "product", id, in, func(tx pgx.Tx, _ string) (int64, error) {
		var rev int64
		if *in.Revision == 0 {
			err := tx.QueryRow(ctx, `INSERT INTO hive.catalog_products(tenant_id,id,name,category,revision) VALUES($1,$2,$3,$4,1) RETURNING revision`, tenant, id, in.Name, in.Category).Scan(&rev)
			return rev, err
		}
		err := tx.QueryRow(ctx, `UPDATE hive.catalog_products SET name=$3,category=$4,revision=revision+1 WHERE tenant_id=$1 AND id=$2 AND revision=$5 RETURNING revision`, tenant, id, in.Name, in.Category, *in.Revision).Scan(&rev)
		return rev, err
	})
}

func (s Service) PutSKU(ctx context.Context, tenant, token, key, id string, in SKUInput) (int64, error) {
	return s.withOwner(ctx, tenant, token, func(tx pgx.Tx, actor string) (int64, error) { return s.PutSKUTx(ctx, tx, tenant, actor, key, id, in) })
}

func (s Service) PutSKUTx(ctx context.Context, tx pgx.Tx, tenant, actor, key, id string, in SKUInput) (int64, error) {
	if in.Revision == nil || *in.Revision < 0 || !codePattern.MatchString(in.Code) || !label(in.Variant) || in.Barcode != nil && !label(*in.Barcode) || in.Active == nil || in.BatchTracked == nil {
		return 0, ErrInput
	}
	return s.write(ctx, tx, tenant, actor, key, "catalog.products", "sku", id, in, func(tx pgx.Tx, _ string) (int64, error) {
		var rev int64
		if *in.Revision == 0 {
			err := tx.QueryRow(ctx, `INSERT INTO hive.catalog_skus(tenant_id,id,product_id,code,variant,barcode,unit,active,batch_tracked,revision)
                VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,1) RETURNING revision`, tenant, id, in.Product, in.Code, in.Variant, in.Barcode, in.Unit, *in.Active, *in.BatchTracked).Scan(&rev)
			return rev, err
		}
		// Unit, batch tracking and parent are stable for existing inventory and sale references.
		err := tx.QueryRow(ctx, `UPDATE hive.catalog_skus SET code=$4,variant=$5,barcode=$6,active=$8,revision=revision+1
            WHERE tenant_id=$1 AND id=$2 AND product_id=$3 AND unit=$7 AND batch_tracked=$9 AND revision=$10 RETURNING revision`, tenant, id, in.Product, in.Code, in.Variant, in.Barcode, in.Unit, *in.Active, *in.BatchTracked, *in.Revision).Scan(&rev)
		return rev, err
	})
}

type Product struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Revision int64  `json:"revision"`
}

// Products and ProductSKUs include inactive master data for authorized management.
func (s Service) Products(ctx context.Context, tenant, token, after string) ([]Product, error) {
	var products []Product
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		if err := s.CheckCapability(ctx, tx, tenant, "catalog.products"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,name,category,revision FROM hive.catalog_products WHERE tenant_id=$1
            AND ($2='' OR id>NULLIF($2,'')::uuid) ORDER BY id LIMIT 50`, tenant, after)
		if err != nil {
			return err
		}
		products, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Product])
		return err
	})
	return products, err
}

type ProductVariant struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Variant string `json:"variant"`
	Active  bool   `json:"active"`
}

func (s Service) ProductSKUs(ctx context.Context, tenant, token, product, after string) ([]ProductVariant, error) {
	var variants []ProductVariant
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		if err := s.CheckCapability(ctx, tx, tenant, "catalog.products"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,code,variant,active FROM hive.catalog_skus WHERE tenant_id=$1 AND product_id=$2
            AND ($3='' OR id>NULLIF($3,'')::uuid) ORDER BY id LIMIT 50`, tenant, product, after)
		if err != nil {
			return err
		}
		variants, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ProductVariant])
		return err
	})
	return variants, err
}
