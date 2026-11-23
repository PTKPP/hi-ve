package inventory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/locations"
)

const (
	ReceivePermission = "inventory.receiving.post"
	RepackPermission  = "inventory.repacking.post"
	SalePermission    = "sales.checkout.post"
)

// resolveMeasure validates SKU eligibility and converts a strictly positive
// measurement to the immutable catalog base unit inside the posting transaction.
func resolveMeasure(ctx context.Context, tx pgx.Tx, tenant string, measure Measure) (string, bool, error) {
	quantity, tracked, err := measureInSKUUnit(ctx, tx, tenant, measure)
	if err != nil {
		return "", false, err
	}
	var positive bool
	if err = tx.QueryRow(ctx, `SELECT $1::numeric>0`, quantity).Scan(&positive); err != nil {
		return "", false, err
	}
	if !positive {
		return "", false, ErrInput
	}
	return quantity, tracked, nil
}

func measureInSKUUnit(ctx context.Context, tx pgx.Tx, tenant string, measure Measure) (string, bool, error) {
	if !uuidPattern.MatchString(measure.SKU) || (measure.Batch != "" && !uuidPattern.MatchString(measure.Batch)) ||
		len(measure.Quantity) > 100 || !decimalPattern.MatchString(measure.Quantity) {
		return "", false, ErrInput
	}
	sku, err := catalog.SKUForOperation(ctx, tx, tenant, measure.SKU)
	if err != nil {
		return "", false, err
	}
	if sku.BatchTracked != (measure.Batch != "") {
		return "", false, ErrInput
	}
	quantity, err := catalog.QuantityForSKU(ctx, tx, tenant, measure.SKU, measure.Quantity, measure.Unit)
	return quantity, sku.BatchTracked, err
}

func ensureBatch(ctx context.Context, tx pgx.Tx, tenant string, measure Measure) error {
	if measure.Batch == "" {
		return nil
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM hive.inventory_batches WHERE tenant_id=$1 AND sku_id=$2 AND id=$3`,
		tenant, measure.SKU, measure.Batch).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInput
	}
	return err
}

// PostSale debits available stock at one authorized, server-derived outlet.
// Sales owns the checkout and its outer transaction. A failed posting rolls
// back this operation's writes and must abort checkout in the caller.
func (s Service) PostSale(ctx context.Context, tx pgx.Tx, scope Scope, document, outlet string, items []Measure) error {
	if len(items) == 0 || len(items) > 100 {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, SalePermission, outlet); err != nil {
			return err
		}
		replay, err := claim(ctx, tx, scope, "sale", document, struct {
			Outlet string
			Items  []Measure
		}{outlet, items})
		if err != nil || replay {
			return err
		}
		node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, outlet, true)
		if err != nil {
			return err
		}
		moves := make([]movement, 0, len(items))
		for _, item := range items {
			quantity, _, err := resolveMeasure(ctx, tx, scope.Tenant, item)
			if err != nil {
				return err
			}
			if err = ensureBatch(ctx, tx, scope.Tenant, item); err != nil {
				return err
			}
			moves = append(moves, movement{stockKey{node, item.SKU, item.Batch, "available"}, "-" + quantity})
		}
		return apply(ctx, tx, scope.Tenant, "sale", document, moves)
	})
}

// ValidateSaleBatch verifies stock lineage for checkout, including orders still
// awaiting a manual payment confirmation.
func ValidateSaleBatch(ctx context.Context, tx pgx.Tx, tenant string, item Measure) error {
	return ensureBatch(ctx, tx, tenant, item)
}
