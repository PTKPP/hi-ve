package sales

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/locations"
)

// CheckoutACK is a minimal projection of an already-committed cash checkout.
type CheckoutACK struct {
	ID       string    `json:"id"`
	Outlet   string    `json:"outlet_id"`
	Actor    string    `json:"operator_id"`
	State    string    `json:"payment_state"`
	PostedAt time.Time `json:"posted_at"`
}

// AcknowledgeCash verifies the exact original document for the same live
// operator and device. This path cannot create a sale or post stock.
func (s Service) AcknowledgeCash(ctx context.Context, tx pgx.Tx, scope Scope, id string, in CheckoutInput) (CheckoutACK, error) {
	var ack CheckoutACK
	if !uuid.MatchString(id) || !uuid.MatchString(scope.Device) || in.Method != "cash" || !validCheckout(in) {
		return ack, ErrInput
	}
	if err := s.authorizeCheckout(ctx, tx, scope, in); err != nil {
		return ack, err
	}
	if _, err := locations.OutletLabel(ctx, tx, scope.Tenant, scope.Outlet); err != nil {
		return ack, err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return ack, err
	}
	expected := sha256.Sum256(body)
	var stored []byte
	err = tx.QueryRow(ctx, `SELECT id,outlet_id,actor_id,'cash_received',created_at,fingerprint
        FROM hive.sales_orders WHERE tenant_id=$1 AND outlet_id=$2 AND id=$3 AND actor_id=$4 AND device_id=$5
        AND method='cash'`, scope.Tenant, scope.Outlet, id, scope.Actor, scope.Device).
		Scan(&ack.ID, &ack.Outlet, &ack.Actor, &ack.State, &ack.PostedAt, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckoutACK{}, ErrUnavailable
	}
	if err != nil {
		return CheckoutACK{}, err
	}
	if !bytes.Equal(stored, expected[:]) {
		return CheckoutACK{}, ErrConflict
	}
	return ack, nil
}
