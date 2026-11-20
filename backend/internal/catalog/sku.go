package catalog

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type SKU struct {
	ProductRevision int64   `json:"product_revision"`
	ID              string  `json:"id"`
	Product         string  `json:"product_id"`
	Code            string  `json:"code"`
	Variant         string  `json:"variant"`
	Barcode         *string `json:"barcode"`
	Unit            string  `json:"unit"`
	Active          bool    `json:"active"`
	BatchTracked    bool    `json:"batch_tracked"`
	Revision        int64   `json:"revision"`
	Name            string  `json:"name"`
	Category        string  `json:"category"`
}

// SKUForOperation keeps the authoritative SKU eligible through the caller's transaction.
func SKUForOperation(ctx context.Context, tx pgx.Tx, tenant, id string) (SKU, error) {
	return readSKU(ctx, tx, tenant, id, true)
}
func readSKU(ctx context.Context, tx pgx.Tx, tenant, id string, activeOnly bool) (SKU, error) {
	var row SKU
	err := tx.QueryRow(ctx, `SELECT s.id,s.product_id,s.code,s.variant,s.barcode,s.unit,s.active,s.batch_tracked,s.revision,p.name,p.category,p.revision
        FROM hive.catalog_skus s JOIN hive.catalog_products p ON (p.tenant_id,p.id)=(s.tenant_id,s.product_id)
        JOIN hive.active_tenants t ON t.id=s.tenant_id
        WHERE s.tenant_id=$1 AND s.id=$2 AND (NOT $3 OR s.active) FOR SHARE OF s,p,t`, tenant, id, activeOnly).
		Scan(&row.ID, &row.Product, &row.Code, &row.Variant, &row.Barcode, &row.Unit, &row.Active, &row.BatchTracked, &row.Revision, &row.Name, &row.Category, &row.ProductRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return SKU{}, ErrUnavailable
	}
	return row, err
}

func (s Service) ProductSKU(ctx context.Context, tenant, token, id string) (SKU, error) {
	var row SKU
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		if err := s.CheckCapability(ctx, tx, tenant, "catalog.products"); err != nil {
			return err
		}
		var err error
		row, err = readSKU(ctx, tx, tenant, id, false)
		return err
	})
	return row, err
}

// QuantityForSKU converts a measured decimal to the SKU unit within a protected posting.
// Inventory owns the sign and posting rules for the resulting quantity.
func QuantityForSKU(ctx context.Context, tx pgx.Tx, tenant, sku, quantity, from string) (string, error) {
	row, err := SKUForOperation(ctx, tx, tenant, sku)
	if err != nil {
		return "", err
	}
	if len(quantity) > 128 || !decimal.MatchString(strings.TrimPrefix(quantity, "-")) {
		return "", ErrInput
	}
	var value string
	err = tx.QueryRow(ctx, `SELECT hive.catalog_quantity($1::numeric,$2,$3)::text`, quantity, from, row.Unit).Scan(&value)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && (pgerr.Code == "22023" || pgerr.Code == "22P02") {
		return "", ErrInput
	}
	return value, err
}
