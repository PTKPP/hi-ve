// Package offline coordinates evidence and reconciliation with the owning domains.
package offline

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/sales"
)

var (
	ErrInput       = errors.New("invalid offline request")
	ErrConflict    = errors.New("offline journal conflict")
	ErrOutstanding = errors.New("offline journal needs reconciliation")
	ErrUnavailable = errors.New("offline record unavailable")
	uuid           = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

type Service struct {
	DB           *pgxpool.Pool
	Devices      devices.Service
	Entitlements entitlements.Service
	Catalog      catalog.Service
	signingKey   ed25519.PrivateKey
}

func New(db *pgxpool.Pool, d devices.Service, e entitlements.Service, c catalog.Service, key []byte) Service {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("hive.offline.lease.signing.v1"))
	return Service{db, d, e, c, ed25519.NewKeyFromSeed(mac.Sum(nil))}
}

type Lease struct {
	ID                 string    `json:"id"`
	Tenant             string    `json:"tenant_id"`
	Device             string    `json:"device_id"`
	Outlet             string    `json:"outlet_id"`
	Operator           string    `json:"operator_id"`
	Context            string    `json:"context_id"`
	DeviceRevision     int64     `json:"device_revision"`
	ContextRevision    int64     `json:"context_revision"`
	CredentialRevision int64     `json:"credential_revision"`
	Discount           bool      `json:"discount_allowed"`
	TenantEpoch        int64     `json:"tenant_epoch"`
	RBAC               int64     `json:"rbac_revision"`
	Transactions       int64     `json:"transactions_revision"`
	Payments           int64     `json:"payments_revision"`
	Issued             time.Time `json:"issued_at"`
	Expires            time.Time `json:"expires_at"`
}

type SignedLease struct {
	Payload    string    `json:"payload"`
	Signature  string    `json:"signature"`
	PublicKey  string    `json:"public_key"`
	ServerTime time.Time `json:"server_time"`
}

const leaseFields = `id,tenant_id,device_id,outlet_id,operator_id,context_id,device_revision,context_revision,credential_revision,discount_allowed,tenant_epoch,rbac_revision,transactions_revision,payments_revision,issued_at,expires_at`

func leaseScan(row pgx.Row, l *Lease, extra ...any) error {
	return row.Scan(append([]any{&l.ID, &l.Tenant, &l.Device, &l.Outlet, &l.Operator, &l.Context, &l.DeviceRevision, &l.ContextRevision, &l.CredentialRevision, &l.Discount, &l.TenantEpoch, &l.RBAC, &l.Transactions, &l.Payments, &l.Issued, &l.Expires}, extra...)...)
}

func (s Service) salesAccess(tenant, actor, outlet string, owner bool) sales.Service {
	return sales.Service{Catalog: s.Catalog, Authorize: func(ctx context.Context, tx pgx.Tx, scope sales.Scope, action string) error {
		if scope.Tenant != tenant || scope.Outlet != outlet || (action != sales.CheckoutPermission && action != sales.DiscountPermission) {
			return identity.ErrForbidden
		}
		if !owner {
			if scope.Actor != actor {
				return identity.ErrForbidden
			}
			if err := identity.AuthorizeLocationGrant(ctx, tx, tenant, actor, outlet, action); err != nil {
				return err
			}
		}
		if action == sales.CheckoutPermission {
			if err := s.Entitlements.CheckWrite(ctx, tx, tenant, "sales.payments"); err != nil {
				return err
			}
		}
		return s.Entitlements.CheckWrite(ctx, tx, tenant, "sales.transactions")
	}}
}

func (s Service) Issue(ctx context.Context, tenant, device, proof, token string) (SignedLease, error) {
	var signed SignedLease
	var l Lease
	err := s.Devices.ReadContext(ctx, tenant, device, proof, token, func(tx pgx.Tx, current devices.OperationalContext) error {
		if err := identity.AuthorizeLocationGrant(ctx, tx, tenant, current.Member, current.Outlet, sales.CheckoutPermission); err != nil {
			return err
		}
		payments, err := s.Entitlements.CheckWriteSnapshot(ctx, tx, tenant, "sales.payments")
		if err != nil {
			return err
		}
		transactions, err := s.Entitlements.CheckWriteSnapshot(ctx, tx, tenant, "sales.transactions")
		if err != nil {
			return err
		}
		tenantEpoch, err := s.Devices.JournalTenantEpoch(ctx, tx, tenant)
		if err != nil {
			return err
		}
		policyRevision, err := identity.AccessRevision(ctx, tx, tenant)
		if err != nil {
			return err
		}
		revision, credential, err := s.Devices.JournalRevisions(ctx, tx, tenant, device, current)
		if err != nil {
			return err
		}
		discountErr := identity.AuthorizeLocationGrant(ctx, tx, tenant, current.Member, current.Outlet, sales.DiscountPermission)
		if discountErr != nil && !errors.Is(discountErr, identity.ErrForbidden) {
			return discountErr
		}
		if err = tx.QueryRow(ctx, `SELECT date_trunc('milliseconds',clock_timestamp())`).Scan(&signed.ServerTime); err != nil {
			return err
		}
		// Repeat issuance preserves the original deadline and never replaces an
		// unaccounted-for journal, including after its lease expires.
		_, err = tx.Exec(ctx, `INSERT INTO hive.offline_leases(tenant_id,device_id,outlet_id,operator_id,context_id,device_revision,
		     context_revision,credential_revision,discount_allowed,tenant_epoch,rbac_revision,transactions_revision,payments_revision,issued_at,expires_at)
		     VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		     ON CONFLICT (tenant_id,device_id) WHERE closed_at IS NULL DO NOTHING`,
			tenant, device, current.Outlet, current.Member, current.ID, revision, current.Revision, credential, discountErr == nil,
			tenantEpoch, policyRevision, transactions.Revision, payments.Revision, signed.ServerTime, leaseEnd(signed.ServerTime, payments.Expires, transactions.Expires))
		if err != nil {
			return err
		}
		var sealed *time.Time
		err = leaseScan(tx.QueryRow(ctx, `SELECT `+leaseFields+`,sealed_at FROM hive.offline_leases WHERE tenant_id=$1 AND device_id=$2 AND closed_at IS NULL`, tenant, device), &l, &sealed)
		if err != nil {
			return err
		}
		if sealed != nil || l.Context != current.ID || l.CredentialRevision != credential || l.TenantEpoch != tenantEpoch || l.RBAC != policyRevision || l.Payments != payments.Revision || l.Transactions != transactions.Revision || !signed.ServerTime.Before(l.Expires) {
			return ErrOutstanding
		}
		return nil
	})
	if err != nil {
		return signed, err
	}
	l.Issued, l.Expires = l.Issued.UTC(), l.Expires.UTC()
	payload, err := json.Marshal(l)
	if err != nil {
		return signed, err
	}
	signed.Payload = base64.RawURLEncoding.EncodeToString(payload)
	signed.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.signingKey, []byte("hive.offline.lease.v1."+signed.Payload)))
	signed.PublicKey = base64.RawURLEncoding.EncodeToString(s.signingKey.Public().(ed25519.PublicKey))
	return signed, nil
}

func (l Lease) includes(at time.Time) bool {
	return !at.Before(l.Issued) && at.Before(l.Expires)
}

func leaseEnd(issued time.Time, expires ...time.Time) time.Time {
	deadline := issued.Add(24 * time.Hour)
	for _, expiry := range expires {
		if expiry.Before(deadline) {
			deadline = expiry
		}
	}
	return deadline
}
