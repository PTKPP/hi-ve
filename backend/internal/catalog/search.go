package catalog

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

type SearchItem struct {
	SKU          string  `json:"sku_id"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Variant      string  `json:"variant"`
	Barcode      *string `json:"barcode"`
	Unit         string  `json:"unit"`
	BatchTracked bool    `json:"batch_tracked"`
}

func SearchActiveSKUs(ctx context.Context, tx pgx.Tx, tenant, term string) ([]SearchItem, error) {
	term = strings.TrimSpace(term)
	if term == "" || len(term) > 128 {
		return nil, ErrInput
	}
	rows, err := tx.Query(ctx, `SELECT s.id,s.code,p.name,s.variant,s.barcode,s.unit,s.batch_tracked FROM hive.catalog_skus s
        JOIN hive.catalog_products p ON (p.tenant_id,p.id)=(s.tenant_id,s.product_id)
        JOIN hive.active_tenants t ON t.id=s.tenant_id
        WHERE s.tenant_id=$1 AND s.active AND
        (s.barcode=$2 OR strpos(s.code,lower($2))>0 OR strpos(lower(p.name),lower($2))>0)
        ORDER BY (s.barcode=$2) DESC NULLS LAST,s.code,s.id LIMIT 50`, tenant, term)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[SearchItem])
}
