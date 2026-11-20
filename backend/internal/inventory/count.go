package inventory

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/locations"
)

const (
	AdjustmentDraftPermission = "inventory.adjustment.create"
	CountDraftPermission      = "inventory.stock_count.create"
	CountObservePermission    = "inventory.stock_count.observe"
)

type Adjustment struct {
	Item     Measure
	Decrease bool
	Reason   string
}

func (s Service) DraftAdjustment(ctx context.Context, tx pgx.Tx, scope Scope, id, location string, body Adjustment) error {
	if strings.TrimSpace(body.Reason) == "" || len(body.Reason) > 255 {
		return ErrInput
	}
	if err := s.authorized(ctx, tx, scope, AdjustmentDraftPermission, location); err != nil {
		return err
	}
	if _, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false); err != nil {
		return err
	}
	if _, _, err := resolveMeasure(ctx, tx, scope.Tenant, body.Item); err != nil {
		return err
	}
	if err := ensureBatch(ctx, tx, scope.Tenant, body.Item); err != nil {
		return err
	}
	return createDocument(ctx, tx, scope, "adjustment", id, location, body)
}

type CountItem struct {
	SKU   string
	Batch string
}

// StartCount captures all requested saleable stock keys from one statement
// snapshot. The balance revision detects even a subsequent zero-net change.
func (s Service) StartCount(ctx context.Context, tx pgx.Tx, scope Scope, id, location string, items []CountItem) error {
	if !uuidPattern.MatchString(id) || len(items) == 0 || len(items) > 100 {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, CountDraftPermission, location); err != nil {
			return err
		}
		node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false)
		if err != nil {
			return err
		}
		skuIDs, batchIDs := make([]string, 0, len(items)), make([]string, 0, len(items))
		seen := make(map[CountItem]bool, len(items))
		for _, item := range items {
			if seen[item] || !uuidPattern.MatchString(item.SKU) || item.Batch != "" && !uuidPattern.MatchString(item.Batch) {
				return ErrInput
			}
			seen[item] = true
			sku, err := catalog.SKUForOperation(ctx, tx, scope.Tenant, item.SKU)
			if err != nil {
				return err
			}
			if sku.BatchTracked != (item.Batch != "") {
				return ErrInput
			}
			if err = ensureBatch(ctx, tx, scope.Tenant, Measure{SKU: item.SKU, Batch: item.Batch}); err != nil {
				return err
			}
			skuIDs, batchIDs = append(skuIDs, item.SKU), append(batchIDs, item.Batch)
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.inventory_stock_counts(tenant_id,id,location_id,actor_id)
            VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, scope.Tenant, id, location, scope.Actor)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var same bool
			err = tx.QueryRow(ctx, `SELECT c.location_id=$3 AND c.actor_id=$4 AND
                (SELECT count(*) FROM hive.inventory_count_lines l WHERE l.tenant_id=$1 AND l.count_id=$2)=cardinality($5::text[])
                AND NOT EXISTS(SELECT 1 FROM unnest($5::text[],$6::text[]) i(sku,batch) WHERE NOT EXISTS(
                    SELECT 1 FROM hive.inventory_count_lines l WHERE l.tenant_id=$1 AND l.count_id=$2 AND l.sku_id=i.sku::uuid
                    AND l.batch_id IS NOT DISTINCT FROM NULLIF(i.batch,'')::uuid))
                FROM hive.inventory_stock_counts c WHERE tenant_id=$1 AND id=$2`, scope.Tenant, id, location, scope.Actor, skuIDs, batchIDs).Scan(&same)
			if err == nil && !same {
				return ErrConflict
			}
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hive.inventory_count_lines(tenant_id,count_id,sku_id,batch_id,quantity,revision)
            SELECT $1,$2,i.sku::uuid,NULLIF(i.batch,'')::uuid,COALESCE(b.quantity,0),COALESCE(b.revision,0)
            FROM unnest($4::text[],$5::text[]) i(sku,batch)
            LEFT JOIN hive.inventory_balances b ON b.tenant_id=$1 AND b.location_id=$3 AND b.sku_id=i.sku::uuid
            AND b.batch_id IS NOT DISTINCT FROM NULLIF(i.batch,'')::uuid AND b.bucket='available'`, scope.Tenant, id, node, skuIDs, batchIDs)
		return err
	})
}

type CountVariance struct {
	Snapshot     string
	Physical     string
	Difference   string
	StockChanged bool
}

// RecordCount observes physical stock and returns variance. Observation is
// append-only and cannot apply an adjustment.
func (s Service) RecordCount(ctx context.Context, tx pgx.Tx, scope Scope, countID, observationID, location string, item Measure) (CountVariance, error) {
	var result CountVariance
	if !uuidPattern.MatchString(countID) || !uuidPattern.MatchString(observationID) {
		return result, ErrInput
	}
	err := pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, CountObservePermission, location); err != nil {
			return err
		}
		node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false)
		if err != nil {
			return err
		}
		quantity, _, err := measureInSKUUnit(ctx, tx, scope.Tenant, item)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.inventory_count_observations(tenant_id,id,count_id,actor_id,sku_id,batch_id,quantity)
            SELECT $1,$2,$3,$4,l.sku_id,l.batch_id,$8::numeric
            FROM hive.inventory_stock_counts c JOIN hive.inventory_count_lines l ON(l.tenant_id,l.count_id)=(c.tenant_id,c.id)
            WHERE c.tenant_id=$1 AND c.id=$3 AND c.location_id=$5 AND l.sku_id=$6 AND l.batch_id IS NOT DISTINCT FROM NULLIF($7,'')::uuid
            ON CONFLICT DO NOTHING`, scope.Tenant, observationID, countID, scope.Actor, location, item.SKU, item.Batch, quantity)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT l.quantity::text,o.quantity::text,(o.quantity-l.quantity)::text,
            COALESCE(b.revision,0)<>l.revision
            FROM hive.inventory_count_observations o
            JOIN hive.inventory_stock_counts c ON(c.tenant_id,c.id)=(o.tenant_id,o.count_id)
            JOIN hive.inventory_count_lines l ON(l.tenant_id,l.count_id,l.sku_id)=(o.tenant_id,o.count_id,o.sku_id)
                AND l.batch_id IS NOT DISTINCT FROM o.batch_id
            LEFT JOIN hive.inventory_balances b ON(b.tenant_id,b.sku_id)=(o.tenant_id,o.sku_id)
                AND b.batch_id IS NOT DISTINCT FROM o.batch_id AND b.bucket='available' AND b.location_id=$9
            WHERE o.tenant_id=$1 AND o.id=$2 AND o.count_id=$3 AND o.actor_id=$4 AND c.location_id=$5
                AND o.sku_id=$6 AND o.batch_id IS NOT DISTINCT FROM NULLIF($7,'')::uuid AND o.quantity=$8::numeric`,
			scope.Tenant, observationID, countID, scope.Actor, location, item.SKU, item.Batch, quantity, node).
			Scan(&result.Snapshot, &result.Physical, &result.Difference, &result.StockChanged)
		if errors.Is(err, pgx.ErrNoRows) && tag.RowsAffected() == 0 {
			return ErrConflict
		}
		return err
	})
	return result, err
}
