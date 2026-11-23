package sales

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"hi-ve/backend/internal/catalog"
)

type Summary struct {
	ID           string     `json:"id"`
	Outlet       string     `json:"outlet_id"`
	OutletName   string     `json:"outlet_name"`
	Actor        string     `json:"operator_id"`
	CreatedAt    time.Time  `json:"created_at"`
	Total        string     `json:"total"`
	Method       string     `json:"method"`
	PaymentState string     `json:"payment_state"`
	ConfirmedAt  *time.Time `json:"confirmed_at"`
}

type Sale struct {
	Summary
	Tendered      *string    `json:"tendered"`
	Change        *string    `json:"change"`
	ReceiptFooter *string    `json:"receipt_footer"`
	Reversals     []Reversal `json:"reversals,omitempty"`
	Items         []Item     `json:"items"`
}
type Item struct {
	Line          int    `json:"line"`
	SKU           string `json:"sku_id"`
	Batch         string `json:"batch_id,omitempty"`
	Name          string `json:"name"`
	Variant       string `json:"variant"`
	Unit          string `json:"unit"`
	Quantity      string `json:"quantity"`
	PriceID       string `json:"price_id"`
	PriceRevision int64  `json:"price_revision"`
	UnitPrice     string `json:"unit_price"`
	Discount      string `json:"discount"`
	Net           string `json:"net"`
}

const summaryColumns = `o.id,o.outlet_id,o.outlet_name,o.actor_id,o.created_at,o.total::text,o.method,
    CASE WHEN o.method='cash' THEN 'cash_received' WHEN c.sale_id IS NULL THEN 'pending_manual_confirmation'
        ELSE 'manually_confirmed' END,c.confirmed_at`
const orderJoin = ` FROM hive.sales_orders o LEFT JOIN hive.sales_manual_confirmations c
    ON (c.tenant_id,c.sale_id)=(o.tenant_id,o.id)`

func load(ctx context.Context, tx pgx.Tx, scope Scope, id string) (Sale, error) {
	var out Sale
	err := tx.QueryRow(ctx, `SELECT `+summaryColumns+`,o.tendered::text,o.change::text,o.receipt_footer`+orderJoin+
		` WHERE o.tenant_id=$1 AND o.outlet_id=$2 AND o.id=$3`, scope.Tenant, scope.Outlet, id).
		Scan(&out.ID, &out.Outlet, &out.OutletName, &out.Actor, &out.CreatedAt, &out.Total, &out.Method, &out.PaymentState,
			&out.ConfirmedAt, &out.Tendered, &out.Change, &out.ReceiptFooter)
	if errors.Is(err, pgx.ErrNoRows) {
		return Sale{}, ErrUnavailable
	}
	if err != nil {
		return Sale{}, err
	}
	rows, err := tx.Query(ctx, `SELECT line,sku_id,COALESCE(batch_id::text,''),name,variant,unit,quantity::text,
        price_id,price_revision,unit_price::text,discount::text,net::text FROM hive.sales_items
        WHERE tenant_id=$1 AND sale_id=$2 ORDER BY line`, scope.Tenant, id)
	if err != nil {
		return Sale{}, err
	}
	out.Items, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Item])
	return out, err
}

func (s Service) Detail(ctx context.Context, tx pgx.Tx, scope Scope, id string, receipt bool) (Sale, error) {
	if !uuid.MatchString(id) {
		return Sale{}, ErrInput
	}
	action := HistoryPermission
	if receipt {
		action = ReceiptPermission
	}
	if err := s.allowed(ctx, tx, scope, action); err != nil {
		return Sale{}, err
	}
	out, err := load(ctx, tx, scope, id)
	if err != nil || receipt {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT `+reversalProjection+reversalsJoin+` WHERE r.tenant_id=$1 AND o.outlet_id=$2 AND r.sale_id=$3
        ORDER BY r.requested_at,r.id`, scope.Tenant, scope.Outlet, id)
	if err != nil {
		return Sale{}, err
	}
	out.Reversals, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Reversal])
	return out, err
}

type HistoryFilter struct {
	ID, Operator, Total, BeforeID string
	From, To, Before              *time.Time
}

func (s Service) History(ctx context.Context, tx pgx.Tx, scope Scope, in HistoryFilter) ([]Summary, error) {
	if in.ID != "" && !uuid.MatchString(in.ID) || in.Operator != "" && !uuid.MatchString(in.Operator) ||
		in.Total != "" && !validMoney(in.Total) || (in.Before == nil) != (in.BeforeID == "") ||
		in.BeforeID != "" && !uuid.MatchString(in.BeforeID) || in.From != nil && in.To != nil && !in.From.Before(*in.To) {
		return nil, ErrInput
	}
	if err := s.allowed(ctx, tx, scope, HistoryPermission); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+summaryColumns+orderJoin+` WHERE o.tenant_id=$1 AND o.outlet_id=$2
        AND ($3='' OR o.id=NULLIF($3,'')::uuid) AND ($4='' OR o.actor_id=NULLIF($4,'')::uuid)
        AND ($5='' OR o.total=NULLIF($5,'')::numeric)
        AND ($6::timestamptz IS NULL OR o.created_at >= $6) AND ($7::timestamptz IS NULL OR o.created_at < $7)
        AND ($8::timestamptz IS NULL OR (o.created_at,o.id)<($8,NULLIF($9,'')::uuid))
        ORDER BY o.created_at DESC,o.id DESC LIMIT 50`, scope.Tenant, scope.Outlet, in.ID, in.Operator, in.Total, in.From, in.To, in.Before, in.BeforeID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Summary])
}

const SearchPermission = "sales.cashier.search"

func (s Service) Search(ctx context.Context, tx pgx.Tx, scope Scope, term string) ([]catalog.SearchItem, error) {
	if err := s.allowed(ctx, tx, scope, SearchPermission); err != nil {
		return nil, err
	}
	return catalog.SearchActiveSKUs(ctx, tx, scope.Tenant, term)
}
