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
	Device   *string   `json:"device_id"`
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
	rows, err := tx.Query(ctx, `SELECT m.kind,m.document_id,m.line,m.sku_id,m.bucket,m.delta::text,p.actor_id,p.posted_at,p.device_id
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

type AdjustmentFilter struct {
	SKU, Reason, BeforeID string
	From, To, Before      *time.Time
}

type AdjustmentHistoryRow struct {
	ID             string    `json:"id"`
	SKU            string    `json:"sku_id"`
	Reason         string    `json:"reason"`
	State          string    `json:"state"`
	CreatedAt      time.Time `json:"created_at"`
	Requester      string    `json:"requester_id"`
	Approver       *string   `json:"approver_id"`
	DecisionReason *string   `json:"decision_reason"`
}

const AdjustmentReadPermission = "inventory.adjustment.read"

func (s Service) AdjustmentHistory(ctx context.Context, tx pgx.Tx, scope Scope, location string, f AdjustmentFilter) ([]AdjustmentHistoryRow, error) {
	if f.SKU != "" && !uuidPattern.MatchString(f.SKU) || len(f.Reason) > 255 ||
		(f.Before != nil && !uuidPattern.MatchString(f.BeforeID)) || (f.Before == nil && f.BeforeID != "") ||
		(f.From != nil && f.To != nil && !f.From.Before(*f.To)) {
		return nil, ErrInput
	}
	if err := s.authorized(ctx, tx, scope, AdjustmentReadPermission, location); err != nil {
		return nil, err
	}
	if _, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.payload->'item'->>'sku_id',d.payload->>'reason',
        CASE WHEN a.document_id IS NOT NULL THEN 'posted' WHEN r.document_id IS NOT NULL THEN 'rejected' ELSE 'draft' END,
        d.created_at,d.created_by,COALESCE(p.actor_id,r.actor_id),COALESCE(a.reason,r.reason)
        FROM hive.inventory_documents d
        LEFT JOIN hive.inventory_approvals a ON(a.tenant_id,a.kind,a.document_id)=(d.tenant_id,d.kind,d.id)
        LEFT JOIN hive.inventory_postings p ON(p.tenant_id,p.kind,p.document_id)=(a.tenant_id,a.kind,a.document_id)
        LEFT JOIN hive.inventory_rejections r ON(r.tenant_id,r.kind,r.document_id)=(d.tenant_id,d.kind,d.id)
        WHERE d.tenant_id=$1 AND d.location_id=$2 AND d.kind='adjustment'
        AND ($3='' OR d.payload->'item'->>'sku_id'=$3) AND ($4='' OR d.payload->>'reason'=$4)
        AND ($5::timestamptz IS NULL OR d.created_at >= $5) AND ($6::timestamptz IS NULL OR d.created_at < $6)
        AND ($7::timestamptz IS NULL OR (d.created_at,d.id) < ($7,NULLIF($8,'')::uuid))
        ORDER BY d.created_at DESC,d.id DESC LIMIT 50`, scope.Tenant, location, f.SKU, f.Reason, f.From, f.To, f.Before, f.BeforeID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[AdjustmentHistoryRow])
}
