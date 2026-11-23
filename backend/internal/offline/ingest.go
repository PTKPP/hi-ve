package offline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/sales"
)

type SaleInput struct {
	Sequence int                 `json:"sequence"`
	Occurred time.Time           `json:"occurred_at"`
	Checkout sales.CheckoutInput `json:"checkout"`
	Quotes   []string            `json:"quote_amounts"`
}

type LoginInput struct {
	Occurred time.Time `json:"occurred_at"`
	Outcome  string    `json:"outcome"`
}

type Receipt struct {
	ID       string    `json:"id"`
	Lease    string    `json:"lease_id"`
	State    string    `json:"state"`
	Reason   *string   `json:"reason"`
	Received time.Time `json:"received_at"`
}

func (s Service) journal(ctx context.Context, tenant, device, lease, id, kind, proof string, body []byte, act func(pgx.Tx, Lease, [32]byte, *int, *time.Time) error) error {
	if !uuid.MatchString(device) || !uuid.MatchString(tenant) || !uuid.MatchString(lease) || !uuid.MatchString(id) {
		return ErrInput
	}
	digest := sha256.Sum256(body)
	if err := s.Devices.VerifyJournalProof(ctx, tenant, device, "hive.offline."+kind+".v1", lease, id, digest, proof); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var l Lease
		var sealed *int
		var closed *time.Time
		err := leaseScan(tx.QueryRow(ctx, `SELECT `+leaseFields+`,sealed_count,closed_at FROM hive.offline_leases
		    WHERE tenant_id=$1 AND id=$2 AND device_id=$3 FOR UPDATE`, tenant, lease, device), &l, &sealed, &closed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
		return act(tx, l, digest, sealed, closed)
	})
}

func (s Service) IngestSale(ctx context.Context, tenant, device, lease, id, proof string, raw []byte, in SaleInput) (Receipt, error) {
	var out Receipt
	if in.Sequence < 1 || in.Sequence > 2147483647 || in.Occurred.IsZero() || !sales.ValidateOfflineCash(in.Checkout) || !sales.ValidateOfflineQuotes(in.Quotes, len(in.Checkout.Items)) {
		return out, ErrInput
	}
	err := s.journal(ctx, tenant, device, lease, id, "sale", proof, raw, func(tx pgx.Tx, l Lease, digest [32]byte, sealed *int, closed *time.Time) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('offline-sale:'||$1::text||':'||$2::text,0))`, tenant, id); err != nil {
			return err
		}
		var stored []byte
		err := tx.QueryRow(ctx, `SELECT o.id,o.lease_id,COALESCE(r.decision,o.state),
            CASE WHEN r.sale_id IS NULL THEN o.reason END,o.received_at,o.fingerprint FROM hive.offline_sales o
            LEFT JOIN hive.offline_sale_resolutions r ON (r.tenant_id,r.sale_id)=(o.tenant_id,o.id)
            WHERE o.tenant_id=$1 AND o.id=$2`, tenant, id).
			Scan(&out.ID, &out.Lease, &out.State, &out.Reason, &out.Received, &stored)
		if err == nil {
			if out.Lease != lease || !bytes.Equal(stored, digest[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var occupied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.offline_sales WHERE tenant_id=$1 AND lease_id=$2 AND sequence=$3)`, tenant, lease, in.Sequence).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return ErrConflict
		}
		reason, err := s.postingReason(ctx, tx, l, closed, sealed, in)
		if err != nil {
			return err
		}
		scope := sales.Scope{Tenant: tenant, Outlet: l.Outlet, Actor: l.Operator, Device: device}
		if reason == "" {
			// Use a savepoint so recognized business denials preserve the
			// envelope while infrastructure failures roll everything back.
			err = pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
				sale, err := s.salesAccess(tenant, l.Operator, l.Outlet, false).CheckoutOffline(ctx, tx, scope, id, in.Checkout, nil)
				if err == nil && !sales.MatchesOfflineQuotes(sale, in.Quotes) {
					return sales.ErrConflict
				}
				return err
			})
			reason = businessReason(err)
			if err != nil && reason == "" {
				return err
			}
		}
		if err = sales.LockOfflineSaleID(ctx, tx, tenant, id); err != nil {
			return err
		}
		state := "posted"
		if reason != "" {
			state = "needs_review"
		}
		body, err := json.Marshal(in.Checkout)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO hive.offline_sales(tenant_id,id,lease_id,sequence,occurrence,checkout,quoted_prices,fingerprint,state,reason)
		    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')) RETURNING id,lease_id,state,reason,received_at`,
			tenant, id, lease, in.Sequence, in.Occurred, body, in.Quotes, digest[:], state, reason).Scan(&out.ID, &out.Lease, &out.State, &out.Reason, &out.Received)
	})
	return out, err
}

func (s Service) postingReason(ctx context.Context, tx pgx.Tx, l Lease, closed *time.Time, sealed *int, in SaleInput) (string, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return "", err
	}
	reason, err := s.Devices.JournalPostingStatus(ctx, tx, l.Tenant, l.Device, l.Operator, l.Outlet, l.Context, l.DeviceRevision, l.ContextRevision, l.CredentialRevision, l.TenantEpoch)
	if err != nil || reason != "" {
		return reason, err
	}
	switch {
	case closed != nil || sealed != nil && in.Sequence > *sealed:
		return "journal_fenced", nil
	case !l.includes(in.Occurred) || in.Occurred.After(now) || !now.Before(l.Expires):
		return "lease_invalid", nil
	case !l.Discount && sales.UsesDiscount(in.Checkout):
		return "permission_changed", nil
	}
	rbac, err := identity.AccessRevision(ctx, tx, l.Tenant)
	if err != nil {
		return "", err
	}
	if rbac != l.RBAC {
		return "permission_changed", nil
	}
	for _, grant := range []struct {
		id       string
		revision int64
	}{{"sales.payments", l.Payments}, {"sales.transactions", l.Transactions}} {
		current, err := s.Entitlements.CheckWriteSnapshot(ctx, tx, l.Tenant, grant.id)
		if errors.Is(err, entitlements.ErrDenied) || err == nil && current.Revision != grant.revision {
			return "entitlement_unavailable", nil
		}
		if err != nil {
			return "", err
		}
	}

	return "", nil
}

func businessReason(err error) string {
	switch {
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrRBACTarget):
		return "permission_changed"
	case errors.Is(err, entitlements.ErrDenied):
		return "entitlement_unavailable"
	case errors.Is(err, inventory.ErrShortage):
		return "stock_shortage"
	case errors.Is(err, sales.ErrConflict), errors.Is(err, catalog.ErrUnavailable), errors.Is(err, locations.ErrOutletUnavailable):
		return "quote_or_scope_conflict"
	case errors.Is(err, sales.ErrInput), errors.Is(err, sales.ErrTender), errors.Is(err, catalog.ErrInput), errors.Is(err, inventory.ErrInput):
		return "invalid_checkout"
	default:
		return ""
	}
}

func (s Service) IngestLogin(ctx context.Context, tenant, device, lease, id, proof string, raw []byte, in LoginInput) error {
	if in.Occurred.IsZero() || in.Outcome != "success" && in.Outcome != "denied" && in.Outcome != "lockout" {
		return ErrInput
	}
	return s.journal(ctx, tenant, device, lease, id, "login", proof, raw, func(tx pgx.Tx, l Lease, d [32]byte, _ *int, _ *time.Time) error {
		if !l.includes(in.Occurred) {
			return ErrInput
		}
		return audit.RecordOfflineLogin(ctx, tx, tenant, lease, id, l.Operator, device, l.Outlet, in.Outcome, in.Occurred, d)
	})
}

func (s Service) ReservedSale(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.offline_sales WHERE tenant_id=$1 AND id=$2)`, tenant, id).Scan(&exists)
	if err == nil && exists {
		return sales.ErrConflict
	}
	return err
}
