package sales

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/inventory"
)

type ManualConfirmation struct {
	Reference string `json:"reference"`
}

// ConfirmManual records the operator's attestation and posts stock atomically.
// The authoritative price/discount is the immutable checkout snapshot.
func (s Service) ConfirmManual(ctx context.Context, tx pgx.Tx, scope Scope, id string, in ManualConfirmation) (Sale, error) {
	var out Sale
	in.Reference = strings.TrimSpace(in.Reference)
	if !uuid.MatchString(id) || len(in.Reference) == 0 || len(in.Reference) > 128 || strings.ContainsFunc(in.Reference, unicode.IsControl) {
		return out, ErrInput
	}
	err := pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.allowed(ctx, tx, scope, ConfirmPermission); err != nil {
			return err
		}
		if err := s.allowed(ctx, tx, scope, CheckoutPermission); err != nil {
			return err
		}
		if err := lockDocument(ctx, tx, scope.Tenant, id); err != nil {
			return err
		}
		sale, err := load(ctx, tx, scope, id)
		if err != nil {
			return err
		}
		if sale.Method != "manual_non_cash" {
			return ErrConflict
		}
		if sale.ConfirmedAt != nil {
			var reference, actor string
			err = tx.QueryRow(ctx, `SELECT reference,actor_id FROM hive.sales_manual_confirmations WHERE tenant_id=$1 AND sale_id=$2`, scope.Tenant, id).Scan(&reference, &actor)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && (reference != in.Reference || actor != scope.Actor) {
				return ErrConflict
			}
			out = sale
			return err
		}
		items := make([]inventory.Measure, len(sale.Items))
		for i, item := range sale.Items {
			items[i] = inventory.Measure{SKU: item.SKU, Batch: item.Batch, Quantity: item.Quantity, Unit: item.Unit}
		}
		if err = s.postInventorySale(ctx, tx, scope, id, items); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.sales_manual_confirmations(tenant_id,sale_id,actor_id,device_id,reference)
            VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5)`, scope.Tenant, id, scope.Actor, scope.Device, in.Reference); err != nil {
			return err
		}
		out, err = load(ctx, tx, scope, id)
		return err
	})
	if err != nil {
		return Sale{}, err
	}
	return out, nil
}
