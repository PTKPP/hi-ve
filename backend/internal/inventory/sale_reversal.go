package inventory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/locations"
)

const SaleReversalPermission = "sales.reversal.post"

type SaleRestock struct {
	SKU, Batch, Quantity string
	Saleable             bool
}

// PostSaleReversal restores quantities in the original stock units. Inventory
// verifies the original debits and cumulative restock while holding their posting lock.
func (s Service) PostSaleReversal(ctx context.Context, tx pgx.Tx, scope Scope, sale, reversal, outlet string, items []SaleRestock) error {
	if !uuidPattern.MatchString(sale) || len(items) == 0 || len(items) > 100 {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, SaleReversalPermission, outlet); err != nil {
			return err
		}
		node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, outlet, true)
		if err != nil {
			return err
		}
		var locked string
		err = tx.QueryRow(ctx, `SELECT document_id FROM hive.inventory_postings WHERE tenant_id=$1 AND kind='sale' AND document_id=$2 FOR UPDATE`, scope.Tenant, sale).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		const kind = "sale.reversal"
		replay, err := claim(ctx, tx, scope, kind, reversal, struct {
			Sale, Outlet string
			Items        []SaleRestock
		}{sale, outlet, items})
		if err != nil || replay {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.inventory_sale_reversals(tenant_id,reversal_id,sale_id) VALUES($1,$2,$3)`, scope.Tenant, reversal, sale); err != nil {
			return err
		}
		moves := make([]movement, 0, len(items))
		for _, item := range items {
			if !uuidPattern.MatchString(item.SKU) || item.Batch != "" && !uuidPattern.MatchString(item.Batch) ||
				len(item.Quantity) > 100 || !decimalPattern.MatchString(item.Quantity) {
				return ErrInput
			}
			bucket := "non_saleable"
			if item.Saleable {
				bucket = "available"
			}
			moves = append(moves, movement{stockKey{node, item.SKU, item.Batch, bucket}, item.Quantity})
		}
		// Check after appending, so duplicate lines for one stock key are counted too.
		if err = apply(ctx, tx, scope.Tenant, kind, reversal, moves); err != nil {
			return err
		}
		var exceeded bool
		err = tx.QueryRow(ctx, `WITH restored AS (
            SELECT m.sku_id,m.batch_id,sum(m.delta) quantity FROM hive.inventory_sale_reversals r
            JOIN hive.inventory_movements m ON (m.tenant_id,m.kind,m.document_id)=(r.tenant_id,r.kind,r.reversal_id)
            WHERE r.tenant_id=$1 AND r.sale_id=$2 AND m.location_id=$3 GROUP BY m.sku_id,m.batch_id
            ), debited AS (
            SELECT sku_id,batch_id,-sum(delta) quantity FROM hive.inventory_movements
            WHERE tenant_id=$1 AND kind='sale' AND document_id=$2 AND location_id=$3 AND bucket='available' GROUP BY sku_id,batch_id
            )
            SELECT EXISTS(SELECT 1 FROM restored r LEFT JOIN debited d ON d.sku_id=r.sku_id AND d.batch_id IS NOT DISTINCT FROM r.batch_id
                WHERE r.quantity>COALESCE(d.quantity,0))`, scope.Tenant, sale, node).Scan(&exceeded)
		if err == nil && exceeded {
			return ErrConflict
		}
		return err
	})
}
