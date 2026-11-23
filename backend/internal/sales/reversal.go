package sales

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/inventory"
)

const (
	ReturnRequestPermission = "sales.returns.create"
	ReturnApprovePermission = "sales.returns.approve"
	VoidRequestPermission   = "sales.voids.create"
	VoidApprovePermission   = "sales.voids.approve"
	RefundConfirmPermission = "sales.payments.refund_confirm"
)

var ErrSelfApproval = errors.New("sales self-approval requires Primary Owner")

// ApprovalRole is supplied by Identity in the same transaction.
type ApprovalRole func(context.Context, pgx.Tx, Scope) (bool, error)

type ReturnItem struct {
	Line     int    `json:"line"`
	Quantity string `json:"quantity"`
	Saleable *bool  `json:"saleable"`
}

type ReversalInput struct {
	Reason string       `json:"reason"`
	Items  []ReturnItem `json:"items,omitempty"`
}

type Reversal struct {
	ID                string     `json:"id"`
	SaleID            string     `json:"sale_id"`
	Kind              string     `json:"kind"`
	Requester         string     `json:"requester_id"`
	Reason            string     `json:"reason"`
	Approver          *string    `json:"approver_id"`
	ApprovalReason    *string    `json:"approval_reason"`
	RequestedAt       time.Time  `json:"requested_at"`
	PostedAt          *time.Time `json:"posted_at"`
	Refund            *string    `json:"refund"`
	RefundStatus      string     `json:"refund_status"`
	RefundMethod      string     `json:"refund_method"`
	RefundConfirmedAt *time.Time `json:"refund_confirmed_at"`
}

const reversalProjection = `r.id,r.sale_id,r.kind,r.requester_id,r.reason,p.approver_id,p.reason,
    r.requested_at,p.posted_at,p.refund::text,
    CASE WHEN p.id IS NULL THEN 'awaiting_approval' WHEN p.refund=0 THEN 'not_required'
    WHEN f.reversal_id IS NULL THEN 'pending_manual_refund' ELSE 'manually_recorded_completed' END,o.method,f.confirmed_at`
const reversalsJoin = ` FROM hive.sales_reversal_requests r JOIN hive.sales_orders o ON (o.tenant_id,o.id)=(r.tenant_id,r.sale_id)
    LEFT JOIN hive.sales_reversal_postings p ON (p.tenant_id,p.id)=(r.tenant_id,r.id)
    LEFT JOIN hive.sales_refund_confirmations f ON (f.tenant_id,f.reversal_id)=(p.tenant_id,p.id)`

func loadReversal(ctx context.Context, tx pgx.Tx, scope Scope, id string) (Reversal, error) {
	var out Reversal
	err := tx.QueryRow(ctx, `SELECT `+reversalProjection+reversalsJoin+` WHERE r.tenant_id=$1 AND o.outlet_id=$2 AND r.id=$3`,
		scope.Tenant, scope.Outlet, id).Scan(&out.ID, &out.SaleID, &out.Kind, &out.Requester, &out.Reason, &out.Approver,
		&out.ApprovalReason, &out.RequestedAt, &out.PostedAt, &out.Refund, &out.RefundStatus, &out.RefundMethod, &out.RefundConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reversal{}, ErrUnavailable
	}
	return out, err
}

func validText(s string, max int, required bool) bool {
	return len(s) <= max && (!required || strings.TrimSpace(s) != "") && !strings.ContainsFunc(s, unicode.IsControl)
}

func (s Service) RequestReversal(ctx context.Context, tx pgx.Tx, scope Scope, saleID, id string, void bool, in ReversalInput) (Reversal, error) {
	var out Reversal
	kind, action := "return", ReturnRequestPermission
	if void {
		kind, action = "void", VoidRequestPermission
	}
	if !uuid.MatchString(saleID) || !uuid.MatchString(id) || !validText(in.Reason, 255, true) ||
		void && len(in.Items) != 0 || !void && (len(in.Items) == 0 || len(in.Items) > 100) {
		return out, ErrInput
	}
	in.Items = slices.Clone(in.Items)
	slices.SortFunc(in.Items, func(a, b ReturnItem) int { return a.Line - b.Line })
	for i, item := range in.Items {
		if item.Line < 1 || item.Line > 100 || item.Saleable == nil || len(item.Quantity) > 100 ||
			!decimal.MatchString(item.Quantity) || strings.Trim(item.Quantity, "0.") == "" || i > 0 && item.Line == in.Items[i-1].Line {
			return out, ErrInput
		}
	}
	err := pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.allowed(ctx, tx, scope, action); err != nil {
			return err
		}
		if err := lockDocument(ctx, tx, scope.Tenant, saleID); err != nil {
			return err
		}
		sale, err := load(ctx, tx, scope, saleID)
		if err != nil {
			return err
		}
		if sale.PaymentState == "pending_manual_confirmation" {
			return ErrConflict
		}
		for _, item := range in.Items {
			if item.Line > len(sale.Items) {
				return ErrInput
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.sales_reversal_requests(tenant_id,id,sale_id,kind,requester_id,device_id,reason,payload)
            VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8) ON CONFLICT DO NOTHING`,
			scope.Tenant, id, saleID, kind, scope.Actor, scope.Device, in.Reason, in)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var same bool
			err = tx.QueryRow(ctx, `SELECT (sale_id,kind,requester_id,payload)=($3::uuid,$4::text,$5::uuid,$6::jsonb)
                FROM hive.sales_reversal_requests WHERE tenant_id=$1 AND id=$2`, scope.Tenant, id, saleID, kind, scope.Actor, in).Scan(&same)
			if err != nil {
				return err
			}
			if !same {
				return ErrConflict
			}
		}
		out, err = loadReversal(ctx, tx, scope, id)
		return err
	})
	return out, err
}

type reversalLine struct {
	ReturnItem
	Refund string
}

func (s Service) ApproveReversal(ctx context.Context, tx pgx.Tx, scope Scope, saleID, id, reason string, void bool) (Reversal, error) {
	var result Reversal
	kind, action := "return", ReturnApprovePermission
	if void {
		kind, action = "void", VoidApprovePermission
	}
	if !uuid.MatchString(saleID) || !uuid.MatchString(id) || !validText(reason, 255, false) {
		return result, ErrInput
	}
	err := pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.allowed(ctx, tx, scope, action); err != nil {
			return err
		}
		if err := lockDocument(ctx, tx, scope.Tenant, saleID); err != nil {
			return err
		}
		out, err := loadReversal(ctx, tx, scope, id)
		if err != nil {
			return err
		}
		if out.SaleID != saleID || out.Kind != kind {
			return ErrUnavailable
		}
		if out.Approver != nil {
			if *out.Approver != scope.Actor || *out.ApprovalReason != reason {
				return ErrConflict
			}
			result = out
			return nil
		}
		if out.Requester == scope.Actor {
			if s.IsPrimaryOwner == nil {
				return ErrSelfApproval
			}
			owner, err := s.IsPrimaryOwner(ctx, tx, scope)
			if err != nil {
				return err
			}
			if !owner {
				return ErrSelfApproval
			}
			if !validText(reason, 255, true) {
				return ErrInput
			}
		}
		var conflict bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.sales_reversal_requests r
            JOIN hive.sales_reversal_postings p USING(tenant_id,id)
            WHERE r.tenant_id=$1 AND r.sale_id=$2 AND ($3 OR r.kind='void'))`, scope.Tenant, saleID, void).Scan(&conflict)
		if err != nil {
			return err
		}
		if conflict {
			return ErrConflict
		}
		sale, err := load(ctx, tx, scope, saleID)
		if err != nil {
			return err
		}
		if sale.PaymentState == "pending_manual_confirmation" {
			return ErrConflict
		}
		var input ReversalInput
		if err = tx.QueryRow(ctx, `SELECT payload FROM hive.sales_reversal_requests WHERE tenant_id=$1 AND id=$2`, scope.Tenant, id).Scan(&input); err != nil {
			return err
		}
		if void {
			saleable := true
			for _, item := range sale.Items {
				input.Items = append(input.Items, ReturnItem{Line: item.Line, Quantity: item.Quantity, Saleable: &saleable})
			}
		}
		plan, err := planRefund(ctx, tx, scope.Tenant, saleID, input.Items)
		if err != nil {
			return err
		}
		amounts := make([]string, len(plan))
		restock := make([]inventory.SaleRestock, len(plan))
		for i, line := range plan {
			item := sale.Items[line.Line-1]
			amounts[i] = line.Refund
			restock[i] = inventory.SaleRestock{SKU: item.SKU, Batch: item.Batch, Quantity: line.Quantity, Saleable: *line.Saleable}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.sales_reversal_postings(tenant_id,id,approver_id,device_id,reason,refund)
            SELECT $1,$2,$3,NULLIF($4,'')::uuid,$5,sum(amount::numeric) FROM unnest($6::text[]) amount`,
			scope.Tenant, id, scope.Actor, scope.Device, reason, amounts); err != nil {
			return err
		}
		for _, line := range plan {
			if _, err = tx.Exec(ctx, `INSERT INTO hive.sales_reversal_items(tenant_id,reversal_id,sale_id,line,quantity,refund,saleable)
                VALUES($1,$2,$3,$4,$5::numeric,$6::numeric,$7)`, scope.Tenant, id, saleID, line.Line, line.Quantity, line.Refund, *line.Saleable); err != nil {
				return err
			}
		}
		stockScope := inventory.Scope{Tenant: scope.Tenant, Actor: scope.Actor, Device: scope.Device}
		inv := inventory.Service{Authorize: func(ctx context.Context, tx pgx.Tx, caller inventory.Scope, act, loc string) error {
			if caller != stockScope || act != inventory.SaleReversalPermission || loc != scope.Outlet {
				return ErrInput
			}
			return s.allowed(ctx, tx, scope, action)
		}}
		if err = inv.PostSaleReversal(ctx, tx, stockScope, saleID, id, scope.Outlet, restock); err != nil {
			return err
		}
		updated, err := loadReversal(ctx, tx, scope, id)
		if err == nil {
			result = updated
		}
		return err
	})
	if err != nil {
		return Reversal{}, err
	}
	return result, nil
}

// div uses integer quotient on exact numeric values. Refunds are the delta
// between the cumulative floor in minor units and refunds already posted.
func planRefund(ctx context.Context, tx pgx.Tx, tenant, id string, items []ReturnItem) ([]reversalLine, error) {
	out := make([]reversalLine, 0, len(items))
	for _, item := range items {
		var amount string
		var eligible bool
		err := tx.QueryRow(ctx, `SELECT
            (div((COALESCE(sum(r.quantity),0)+$4::numeric)*i.net*100,i.quantity)*0.01-COALESCE(sum(r.refund),0))::text,
            COALESCE(sum(r.quantity),0)+$4::numeric<=i.quantity
            FROM hive.sales_items i LEFT JOIN hive.sales_reversal_items r ON (r.tenant_id,r.sale_id,r.line)=(i.tenant_id,i.sale_id,i.line)
            WHERE i.tenant_id=$1 AND i.sale_id=$2 AND i.line=$3 GROUP BY i.quantity,i.net`,
			tenant, id, item.Line, item.Quantity).Scan(&amount, &eligible)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !eligible {
			return nil, ErrConflict
		}
		if err != nil {
			return nil, err
		}
		out = append(out, reversalLine{ReturnItem: item, Refund: amount})
	}
	return out, nil
}

func (s Service) ConfirmRefund(ctx context.Context, tx pgx.Tx, scope Scope, saleID, id, reference string) (Reversal, error) {
	var result Reversal
	if !uuid.MatchString(saleID) || !uuid.MatchString(id) || !validText(reference, 128, true) {
		return result, ErrInput
	}
	err := pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.allowed(ctx, tx, scope, RefundConfirmPermission); err != nil {
			return err
		}
		if err := lockDocument(ctx, tx, scope.Tenant, saleID); err != nil {
			return err
		}
		current, err := loadReversal(ctx, tx, scope, id)
		if err != nil {
			return err
		}
		if current.SaleID != saleID {
			return ErrUnavailable
		}
		if current.RefundStatus != "pending_manual_refund" && current.RefundStatus != "manually_recorded_completed" {
			return ErrConflict
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.sales_refund_confirmations(tenant_id,reversal_id,actor_id,device_id,reference)
            VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5) ON CONFLICT DO NOTHING`, scope.Tenant, id, scope.Actor, scope.Device, reference)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var same bool
			err = tx.QueryRow(ctx, `SELECT (actor_id,reference)=($3::uuid,$4::text) FROM hive.sales_refund_confirmations WHERE tenant_id=$1 AND reversal_id=$2`,
				scope.Tenant, id, scope.Actor, reference).Scan(&same)
			if err != nil {
				return err
			}
			if !same {
				return ErrConflict
			}
		}
		result, err = loadReversal(ctx, tx, scope, id)
		return err
	})
	return result, err
}
