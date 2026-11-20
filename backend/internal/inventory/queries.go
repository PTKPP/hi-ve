package inventory

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/locations"
)

const BalancePermission = "inventory.balance.read"
const TracePermission = "inventory.traceability.read"

type Balance struct {
	Batch    *string `json:"batch_id"`
	Bucket   string  `json:"bucket"`
	Quantity string  `json:"quantity"`
	Revision int64   `json:"revision"`
}

// Stock reads all buckets for the requested SKU and stock location.
func (s Service) Stock(ctx context.Context, tx pgx.Tx, scope Scope, location, sku string) ([]Balance, error) {
	if !uuidPattern.MatchString(sku) {
		return nil, ErrInput
	}
	if err := s.authorized(ctx, tx, scope, BalancePermission, location); err != nil {
		return nil, err
	}
	node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT batch_id,bucket,quantity::text,revision FROM hive.inventory_balances
        WHERE tenant_id=$1 AND location_id=$2 AND sku_id=$3 ORDER BY batch_id,bucket`, scope.Tenant, node, sku)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Balance])
}

type Movement struct {
	Kind     string    `json:"kind"`
	Document string    `json:"document_id"`
	Line     int32     `json:"line"`
	SKU      string    `json:"sku_id"`
	Bucket   string    `json:"bucket"`
	Delta    string    `json:"delta"`
	Actor    string    `json:"actor_id"`
	PostedAt time.Time `json:"posted_at"`
}

type TraceCursor struct {
	PostedAt time.Time
	Kind     string
	Document string
	Line     int32
}

// BatchHistory is a bounded ledger projection scoped to the authorized location.
func (s Service) BatchHistory(ctx context.Context, tx pgx.Tx, scope Scope, location, batch string, before *TraceCursor) ([]Movement, error) {
	if !uuidPattern.MatchString(batch) {
		return nil, ErrInput
	}
	if err := s.authorized(ctx, tx, scope, TracePermission, location); err != nil {
		return nil, err
	}
	node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false)
	if err != nil {
		return nil, err
	}
	var cursorTime *time.Time
	var kind, doc string
	var line int32
	if before != nil {
		if before.PostedAt.IsZero() || !uuidPattern.MatchString(before.Document) || before.Line < 1 {
			return nil, ErrInput
		}
		cursorTime, kind, doc, line = &before.PostedAt, before.Kind, before.Document, before.Line
	}
	rows, err := tx.Query(ctx, `SELECT m.kind,m.document_id,m.line,m.sku_id,m.bucket,m.delta::text,p.actor_id,p.posted_at
        FROM hive.inventory_movements m JOIN hive.inventory_postings p USING(tenant_id,kind,document_id)
        WHERE m.tenant_id=$1 AND m.location_id=$2 AND m.batch_id=$3
            AND ($4::timestamptz IS NULL OR (p.posted_at,m.kind,m.document_id,m.line) <
                ($4,$5,NULLIF($6,'')::uuid,$7))
        ORDER BY p.posted_at DESC,m.kind DESC,m.document_id DESC,m.line DESC LIMIT 50`,
		scope.Tenant, node, batch, cursorTime, kind, doc, line)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Movement])
}
