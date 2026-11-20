package catalog

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

var decimal = regexp.MustCompile("^(0|[1-9][0-9]*)([.][0-9]+)?$")

type PriceInput struct {
	Revision *int64 `json:"revision"`
	Amount   string `json:"amount"`
}
type Price struct {
	ID       string    `json:"price_id"`
	Amount   string    `json:"amount"`
	Revision int64     `json:"revision"`
	Updated  time.Time `json:"updated_at"`
}

func (s Service) lockOutlet(ctx context.Context, tx pgx.Tx, tenant, outlet string) error {
	ok, err := s.LockOutlets(ctx, tx, tenant, []string{outlet})
	if err == nil && !ok {
		return ErrUnavailable
	}
	return err
}

func (s Service) PutPrice(ctx context.Context, tenant, token, key, sku, outlet string, in PriceInput) (int64, error) {
	if in.Revision == nil || *in.Revision < 0 || len(in.Amount) > 128 || !decimal.MatchString(in.Amount) {
		return 0, ErrInput
	}
	return s.write(ctx, tenant, token, key, "catalog.pricing", "price/"+outlet, sku, in, func(tx pgx.Tx, actor string) (int64, error) {
		if outlet != "" {
			if err := s.lockOutlet(ctx, tx, tenant, outlet); err != nil {
				return 0, err
			}
		}
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id FROM hive.catalog_skus WHERE tenant_id=$1 AND id=$2 FOR NO KEY UPDATE`, tenant, sku).Scan(&locked); err != nil {
			return 0, err
		}
		var revision int64
		err := tx.QueryRow(ctx, `INSERT INTO hive.catalog_prices(tenant_id,sku_id,outlet_id,actor_id,amount,revision)
            SELECT $1,$2,NULLIF($3,'')::uuid,$4,$5::numeric,$6::bigint+1 WHERE $6::bigint=(
            SELECT COALESCE(max(revision),0) FROM hive.catalog_prices WHERE tenant_id=$1 AND sku_id=$2 AND outlet_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid)
            RETURNING revision`, tenant, sku, outlet, actor, in.Amount, *in.Revision).Scan(&revision)
		return revision, err
	})
}

// SellingPrice is for an authorized consumer using its backend-derived outlet.
// The row lock orders the quote against both SKU deactivation and a new price version.
func (s Service) SellingPrice(ctx context.Context, tx pgx.Tx, tenant, outlet, sku string) (Price, error) {
	if err := s.CheckCapability(ctx, tx, tenant, "catalog.pricing"); err != nil {
		return Price{}, err
	}
	if err := s.lockOutlet(ctx, tx, tenant, outlet); err != nil {
		return Price{}, err
	}
	if _, err := SKUForOperation(ctx, tx, tenant, sku); err != nil {
		return Price{}, err
	}
	var p Price
	err := tx.QueryRow(ctx, `SELECT id,amount::text,revision,updated_at FROM hive.catalog_prices
        WHERE tenant_id=$1 AND sku_id=$2 AND (outlet_id=$3 OR outlet_id IS NULL)
        ORDER BY outlet_id NULLS LAST, revision DESC LIMIT 1`, tenant, sku, outlet).Scan(&p.ID, &p.Amount, &p.Revision, &p.Updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return Price{}, ErrUnavailable
	}
	return p, err
}

// PriceHistory exposes only selling-price versions to a live tenant administrator.
// before=0 starts with the latest version. Base and outlet versions have independent sequences.
func (s Service) PriceHistory(ctx context.Context, tenant, token, sku, outlet string, before int64) ([]Price, error) {
	var result []Price
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		if err := s.CheckCapability(ctx, tx, tenant, "catalog.pricing"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,amount::text,revision,updated_at FROM hive.catalog_prices
            WHERE tenant_id=$1 AND sku_id=$2 AND outlet_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid
            AND ($4::bigint=0 OR revision<$4) ORDER BY revision DESC LIMIT 50`, tenant, sku, outlet, before)
		if err != nil {
			return err
		}
		result, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Price])
		return err
	})
	return result, err
}
