package sales

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type ReportWindow struct {
	Tenant, Outlet string
	From, To       time.Time
}
type ReportTotals struct {
	Gross      string `json:"gross"`
	Discount   string `json:"discount"`
	Returns    string `json:"returns"`
	Voids      string `json:"voids"`
	Net        string `json:"net"`
	Received   string `json:"received"`
	Pending    string `json:"pending"`
	RefundPaid string `json:"refund_recorded"`
}
type ReportGroup struct {
	Outlet     string `json:"outlet_id"`
	OutletName string `json:"outlet_name"`
	Method     string `json:"method"`
	ReportTotals
}
type ReportSale struct {
	ID      string    `json:"id"`
	Outlet  string    `json:"outlet_id"`
	Method  string    `json:"method"`
	At      time.Time `json:"created_at"`
	Payment string    `json:"payment_state"`
	ReportTotals
}

// The order date determines the cohort. Payment and reversal state is the
// committed state at the time of the read. Reversals never subtract cash
// until the existing refund confirmation is recorded.
const reportCohort = `WITH orders AS MATERIALIZED (
 SELECT o.tenant_id,o.id,o.outlet_id,o.outlet_name,o.created_at,o.method,o.total,
 c.sale_id IS NOT NULL OR o.method='cash' AS paid
 FROM hive.sales_orders o LEFT JOIN hive.sales_manual_confirmations c ON (c.tenant_id,c.sale_id)=(o.tenant_id,o.id)
 WHERE o.tenant_id=$1 AND o.created_at >= $2 AND o.created_at < $3
 `
const reportAmounts = `
), discounts AS (
 SELECT i.sale_id,sum(i.discount) discount FROM orders o JOIN hive.sales_items i
 ON (i.tenant_id,i.sale_id)=(o.tenant_id,o.id) GROUP BY i.sale_id
), reversals AS (
 SELECT r.sale_id, sum(p.refund) FILTER (WHERE r.kind='return') returned,
 sum(p.refund) FILTER (WHERE r.kind='void') voided,
 sum(p.refund) FILTER (WHERE f.reversal_id IS NOT NULL) refunded,
 bool_or(r.kind='void') void_exists
 FROM orders o JOIN hive.sales_reversal_requests r ON (r.tenant_id,r.sale_id)=(o.tenant_id,o.id)
 JOIN hive.sales_reversal_postings p ON (p.tenant_id,p.id)=(r.tenant_id,r.id)
 LEFT JOIN hive.sales_refund_confirmations f ON (f.tenant_id,f.reversal_id)=(p.tenant_id,p.id)
 GROUP BY r.sale_id
), amounts AS (
 SELECT o.*,
 CASE WHEN paid THEN total+COALESCE(d.discount,0) ELSE 0 END gross,
 CASE WHEN paid THEN COALESCE(d.discount,0) ELSE 0 END discount,
 CASE WHEN paid THEN COALESCE(r.returned,0) ELSE 0 END returned,
 CASE WHEN paid THEN COALESCE(r.voided,0) ELSE 0 END voided,
 CASE WHEN paid THEN total-COALESCE(r.returned,0)-COALESCE(r.voided,0) ELSE 0 END net,
 CASE WHEN paid THEN total ELSE 0 END received,
 CASE WHEN NOT paid AND NOT COALESCE(r.void_exists,false) THEN total ELSE 0 END pending,
 CASE WHEN paid THEN COALESCE(r.refunded,0) ELSE 0 END refunded,
 CASE WHEN o.method='cash' THEN 'cash_received' WHEN paid THEN 'manually_confirmed'
 WHEN COALESCE(r.void_exists,false) THEN 'voided' ELSE 'pending_manual_confirmation' END payment
 FROM orders o LEFT JOIN discounts d ON d.sale_id=o.id LEFT JOIN reversals r ON r.sale_id=o.id
)
`

func reportQuery(w ReportWindow) (string, []any) {
	args := []any{w.Tenant, w.From, w.To}
	query := reportCohort
	if w.Outlet != "" {
		query += " AND o.outlet_id=$4"
		args = append(args, w.Outlet)
	}
	return query, args
}

func ProjectTotals(ctx context.Context, tx pgx.Tx, w ReportWindow) (ReportTotals, []ReportGroup, error) {
	query, args := reportQuery(w)
	rows, err := tx.Query(ctx, query+reportAmounts+`
 SELECT COALESCE(a.outlet_id::text,''),COALESCE(l.name,''),COALESCE(a.method,''),
 COALESCE(sum(a.gross),0)::text,COALESCE(sum(a.discount),0)::text,COALESCE(sum(a.returned),0)::text,
 COALESCE(sum(a.voided),0)::text,COALESCE(sum(a.net),0)::text,COALESCE(sum(a.received),0)::text,
 COALESCE(sum(a.pending),0)::text,COALESCE(sum(a.refunded),0)::text
 FROM amounts a JOIN hive.locations l ON (l.tenant_id,l.id)=(a.tenant_id,a.outlet_id)
 GROUP BY GROUPING SETS ((),(a.outlet_id,l.name),(a.outlet_id,l.name,a.method)) ORDER BY 1,3`, args...)
	var total ReportTotals
	out := []ReportGroup{}
	if err != nil {
		return total, out, err
	}
	defer rows.Close()
	for rows.Next() {
		var g ReportGroup
		v := &g.ReportTotals
		if err = rows.Scan(&g.Outlet, &g.OutletName, &g.Method, &v.Gross, &v.Discount, &v.Returns, &v.Voids, &v.Net, &v.Received, &v.Pending, &v.RefundPaid); err != nil {
			return total, nil, err
		}
		if g.Outlet == "" {
			total = *v
		} else {
			out = append(out, g)
		}
	}
	return total, out, rows.Err()
}

func ProjectSales(ctx context.Context, tx pgx.Tx, w ReportWindow, before *time.Time, id string) ([]ReportSale, error) {
	query, args := reportQuery(w)
	n := len(args)
	if before != nil {
		if n == 3 {
			query += " AND (o.created_at,o.id)<($4,$5)"
		} else {
			query += " AND (o.created_at,o.id)<($5,$6)"
		}
		args = append(args, before, id)
	}
	rows, err := tx.Query(ctx, query+` ORDER BY o.created_at DESC,o.id DESC LIMIT 51`+reportAmounts+
		`SELECT id,outlet_id,method,created_at,payment,gross::text,discount::text,returned::text,voided::text,net::text,
	  received::text,pending::text,refunded::text FROM amounts ORDER BY created_at DESC,id DESC`, args...)
	if err != nil {
		return nil, err
	}
	out := []ReportSale{}
	defer rows.Close()
	for rows.Next() {
		var r ReportSale
		v := &r.ReportTotals
		if err = rows.Scan(&r.ID, &r.Outlet, &r.Method, &r.At, &r.Payment, &v.Gross, &v.Discount, &v.Returns, &v.Voids, &v.Net, &v.Received, &v.Pending, &v.RefundPaid); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
