package offline

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/sales"
)

type SealInput struct {
	Count int `json:"sale_count"`
}

func (s Service) Seal(ctx context.Context, tenant, device, lease, proof string, raw []byte, in SealInput) error {
	if in.Count < 0 || in.Count > 2147483647 {
		return ErrInput
	}
	return s.journal(ctx, tenant, device, lease, lease, "seal", proof, raw, func(tx pgx.Tx, l Lease, _ [32]byte, sealed *int, closed *time.Time) error {
		if sealed != nil && *sealed == in.Count {
			return nil
		}
		if closed != nil || sealed != nil {
			return ErrConflict
		}
		// A sealed report is device evidence. An Owner must review it before a
		// device can switch, even for a signed report with zero local rows.
		var complete bool
		err := tx.QueryRow(ctx, `SELECT count(*)=$3 AND COALESCE(max(sequence),0)=$3
		    FROM hive.offline_sales WHERE tenant_id=$1 AND lease_id=$2`, tenant, lease, in.Count).Scan(&complete)
		if err != nil {
			return err
		}
		if !complete {
			return ErrOutstanding
		}
		_, err = tx.Exec(ctx, `UPDATE hive.offline_leases SET sealed_count=$3,sealed_at=clock_timestamp()
		    WHERE tenant_id=$1 AND id=$2`, tenant, l.ID, in.Count)
		return err
	})
}

type JournalSummary struct {
	ID          string    `json:"id"`
	Device      string    `json:"device_id"`
	Outlet      string    `json:"outlet_id"`
	Operator    string    `json:"operator_id"`
	Expires     time.Time `json:"expires_at"`
	SealedCount *int      `json:"sealed_count"`
	Received    int       `json:"received_count"`
	Unresolved  int       `json:"unresolved_count"`
}

type ReviewItem struct {
	ID       string              `json:"id"`
	Lease    string              `json:"lease_id"`
	Outlet   string              `json:"outlet_id"`
	Operator string              `json:"operator_id"`
	Occurred time.Time           `json:"occurred_at"`
	Received time.Time           `json:"received_at"`
	Reason   string              `json:"reason"`
	Checkout sales.CheckoutInput `json:"checkout"`
	Quotes   []string            `json:"quote_amounts"`
}

type Review struct {
	Journals []JournalSummary `json:"journals"`
	Sales    []ReviewItem     `json:"sales"`
}

func (s Service) Review(ctx context.Context, tenant, token, afterJournal, afterSale string) (Review, error) {
	out := Review{Journals: []JournalSummary{}, Sales: []ReviewItem{}}
	if afterJournal != "" && !uuid.MatchString(afterJournal) || afterSale != "" && !uuid.MatchString(afterSale) {
		return out, ErrInput
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := authorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT l.id,l.device_id,l.outlet_id,l.operator_id,l.expires_at,l.sealed_count,
		    (SELECT count(*) FROM hive.offline_sales o WHERE o.tenant_id=l.tenant_id AND o.lease_id=l.id),
		    (SELECT count(*) FROM hive.offline_sales o WHERE o.tenant_id=l.tenant_id AND o.lease_id=l.id AND o.state='needs_review'
		     AND NOT EXISTS(SELECT 1 FROM hive.offline_sale_resolutions r WHERE r.tenant_id=o.tenant_id AND r.sale_id=o.id))
		    FROM hive.offline_leases l WHERE tenant_id=$1 AND closed_at IS NULL AND ($2='' OR id>NULLIF($2,'')::uuid)
		    ORDER BY id LIMIT 50`, tenant, afterJournal)
		if err != nil {
			return err
		}
		out.Journals, err = pgx.CollectRows(rows, pgx.RowToStructByPos[JournalSummary])
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT o.id,o.lease_id,l.outlet_id,l.operator_id,o.occurrence,o.received_at,o.reason,o.checkout,o.quoted_prices
		    FROM hive.offline_sales o JOIN hive.offline_leases l ON (l.tenant_id,l.id)=(o.tenant_id,o.lease_id)
		    WHERE o.tenant_id=$1 AND o.state='needs_review' AND ($2='' OR o.id>NULLIF($2,'')::uuid)
		    AND NOT EXISTS(SELECT 1 FROM hive.offline_sale_resolutions r WHERE r.tenant_id=o.tenant_id AND r.sale_id=o.id)
		    ORDER BY o.id LIMIT 50`, tenant, afterSale)
		if err != nil {
			return err
		}
		out.Sales, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ReviewItem])
		return err
	})
	return out, err
}

type Resolution struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	Reference string `json:"reference,omitempty"`
	Refund    string `json:"refund_amount,omitempty"`
}

func (s Service) Resolve(ctx context.Context, tenant, token, id string, in Resolution) error {
	in.Reason, in.Reference = strings.TrimSpace(in.Reason), strings.TrimSpace(in.Reference)
	if !uuid.MatchString(id) || len(in.Reason) == 0 || len(in.Reason) > 500 || len(in.Reference) > 128 ||
		in.Decision != "posted" && in.Decision != "manual_refund_recorded" || (in.Decision == "manual_refund_recorded") != (in.Reference != "") || (in.Decision == "manual_refund_recorded") != (in.Refund != "") || in.Refund != "" && !sales.ValidOfflineRefund(in.Refund) {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := authorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		var lease string
		err = tx.QueryRow(ctx, `SELECT lease_id FROM hive.offline_sales WHERE tenant_id=$1 AND id=$2 AND state='needs_review'`, tenant, id).Scan(&lease)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
		var l Lease
		if err = leaseScan(tx.QueryRow(ctx, `SELECT `+leaseFields+` FROM hive.offline_leases WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, lease), &l); err != nil {
			return err
		}
		var previous Resolution
		err = tx.QueryRow(ctx, `SELECT decision,reason,COALESCE(reference,''),COALESCE(refund_amount::text,'') FROM hive.offline_sale_resolutions
		    WHERE tenant_id=$1 AND sale_id=$2`, tenant, id).Scan(&previous.Decision, &previous.Reason, &previous.Reference, &previous.Refund)
		if err == nil {
			if previous.Decision != in.Decision || previous.Reason != in.Reason || previous.Reference != in.Reference || !sales.SameOfflineMoney(previous.Refund, in.Refund) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var input sales.CheckoutInput
		var occurred time.Time
		var quotes []string
		if err = tx.QueryRow(ctx, `SELECT checkout,occurrence,quoted_prices FROM hive.offline_sales WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&input, &occurred, &quotes); err != nil {
			return err
		}
		if in.Decision == "posted" {
			// A settlement posts now under the Owner's current capability.
			// Historical pricing must be proven from immutable Catalog quotes.
			if !l.includes(occurred) {
				return ErrConflict
			}
			scope := sales.Scope{Tenant: tenant, Outlet: l.Outlet, Actor: l.Operator, Device: l.Device}
			sale, err := s.salesAccess(tenant, actor, l.Outlet, true).CheckoutOffline(ctx, tx, scope, id, input, &occurred)
			if err != nil {
				return err
			}
			if !sales.MatchesOfflineQuotes(sale, quotes) {
				return ErrConflict
			}
		} else {
			if err = sales.LockOfflineSaleID(ctx, tx, tenant, id); err != nil {
				return err
			}
			if err = sales.VerifyOfflineRefundAmount(ctx, tx, tenant, input, quotes, in.Refund); err != nil {
				return err
			}
			exists, err := sales.OfflineOrderExists(ctx, tx, tenant, id)
			if err != nil {
				return err
			}
			if exists {
				return ErrConflict
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO hive.offline_sale_resolutions(tenant_id,sale_id,actor_id,decision,reason,reference,refund_amount)
		    VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,'')::numeric)`, tenant, id, actor, in.Decision, in.Reason, in.Reference, in.Refund)
		return err
	})
}

type CloseInput struct {
	Reason string `json:"reason"`
	Lost   bool   `json:"lost"`
}

func outstanding(ctx context.Context, tx pgx.Tx, tenant, lease string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.offline_sales o WHERE tenant_id=$1 AND lease_id=$2 AND state='needs_review'
	    AND NOT EXISTS(SELECT 1 FROM hive.offline_sale_resolutions r WHERE r.tenant_id=o.tenant_id AND r.sale_id=o.id))`, tenant, lease).Scan(&exists)
	return exists, err
}

func (s Service) Close(ctx context.Context, tenant, token, lease string, in CloseInput) error {
	in.Reason = strings.TrimSpace(in.Reason)
	if !uuid.MatchString(lease) || len(in.Reason) == 0 || len(in.Reason) > 500 {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := authorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		var device string
		var sealed *int
		var closed *time.Time
		err = tx.QueryRow(ctx, `SELECT device_id,sealed_count,closed_at FROM hive.offline_leases WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, lease).Scan(&device, &sealed, &closed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
		action := "lease.close"
		if in.Lost {
			action = "lease.lost"
		}
		if closed != nil {
			same, err := audit.HasOfflineControl(ctx, tx, tenant, lease, action, in.Reason)
			if err != nil {
				return err
			}
			if !same {
				return ErrConflict
			}
			return nil
		}
		busy, err := outstanding(ctx, tx, tenant, lease)
		if err != nil {
			return err
		}
		if busy {
			return ErrOutstanding
		}
		if in.Lost {
			revoked, err := s.Devices.JournalDeviceRevoked(ctx, tx, tenant, device)
			if err != nil {
				return err
			}
			if !revoked {
				return ErrOutstanding
			}
		} else if sealed == nil {
			return ErrOutstanding
		}
		if _, err = tx.Exec(ctx, `UPDATE hive.offline_leases SET closed_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2`, tenant, lease); err != nil {
			return err
		}
		return audit.RecordOfflineControl(ctx, tx, tenant, device, actor, lease, action, in.Reason, "")
	})
}

func (s Service) Switch(ctx context.Context, tenant, token, device, outlet string, expected int64) (int64, error) {
	if !uuid.MatchString(device) || !uuid.MatchString(outlet) || expected < 0 || expected == 9223372036854775807 {
		return 0, ErrInput
	}
	var revision int64
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := authorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		revision, err = s.Devices.SwitchOutlet(ctx, tx, tenant, device, actor, outlet, expected, func() error {
			var busy bool
			err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.offline_leases l WHERE tenant_id=$1 AND device_id=$2 AND
			    (closed_at IS NULL OR EXISTS(SELECT 1 FROM hive.offline_sales o WHERE o.tenant_id=l.tenant_id AND o.lease_id=l.id AND
			     o.state='needs_review' AND NOT EXISTS(SELECT 1 FROM hive.offline_sale_resolutions r WHERE r.tenant_id=o.tenant_id AND r.sale_id=o.id))))`, tenant, device).Scan(&busy)
			if err != nil {
				return err
			}
			if busy {
				return ErrOutstanding
			}
			return nil
		})
		return err
	})
	return revision, err
}

func authorizeOwner(ctx context.Context, tx pgx.Tx, tenant, token string) (string, error) {
	actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, token)
	if err != nil {
		return "", err
	}
	var key string
	err = tx.QueryRow(ctx, `SELECT id FROM hive.active_tenants WHERE id=$1 FOR SHARE`, tenant).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		err = identity.ErrForbidden
	}
	return actor, err
}

func (s Service) SwitchState(ctx context.Context, tenant, token, device string) (devices.OutletSwitchState, error) {
	var out devices.OutletSwitchState
	if !uuid.MatchString(device) {
		return out, ErrInput
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := authorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		var err error
		out, err = s.Devices.ReadOutletSwitch(ctx, tx, tenant, device)
		return err
	})
	return out, err
}
