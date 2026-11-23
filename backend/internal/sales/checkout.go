// Package sales owns order snapshots, recorded payments and checkout.
package sales

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/settings"
)

var (
	ErrInput       = errors.New("invalid sales input")
	ErrConflict    = errors.New("sales document conflict")
	ErrUnavailable = errors.New("sales document unavailable")
	ErrTender      = errors.New("insufficient cash tendered")
	uuid           = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	decimal        = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
)

const (
	CheckoutPermission = inventory.SalePermission
	DiscountPermission = "sales.transactions.discount"
	ConfirmPermission  = "sales.payments.confirm"
	HistoryPermission  = "sales.history.read"
	ReceiptPermission  = "sales.receipts.read"
)

type Scope struct{ Tenant, Outlet, Actor, Device string }
type Authorize func(context.Context, pgx.Tx, Scope, string) error

type Service struct {
	Catalog        catalog.Service
	Authorize      Authorize
	IsPrimaryOwner ApprovalRole
	OfflineFence   func(context.Context, pgx.Tx, string, string) error
}

type CheckoutItem struct {
	inventory.Measure
	PriceID  string `json:"price_id"`
	Discount string `json:"discount"`
}
type CheckoutInput struct {
	Items    []CheckoutItem `json:"items"`
	Method   string         `json:"method"`
	Tendered *string        `json:"tendered"`
}

// IDR has two ISO 4217 minor-unit digits. Prices can have more precision,
// but final payable line values and tender must be representable in minor units.
func centAmount(s string) bool {
	_, fraction, ok := strings.Cut(s, ".")
	return !ok || len(strings.TrimRight(fraction, "0")) <= 2
}

func validMoney(s string) bool { return len(s) <= 128 && decimal.MatchString(s) }

func (s Service) allowed(ctx context.Context, tx pgx.Tx, scope Scope, action string) error {
	if s.Authorize == nil || !uuid.MatchString(scope.Tenant) || !uuid.MatchString(scope.Outlet) || !uuid.MatchString(scope.Actor) ||
		(scope.Device != "" && !uuid.MatchString(scope.Device)) {
		return ErrInput
	}
	return s.Authorize(ctx, tx, scope, action)
}

func validCheckout(in CheckoutInput) bool {
	if len(in.Items) == 0 || len(in.Items) > 100 {
		return false
	}
	switch in.Method {
	case "cash":
		if in.Tendered == nil || !validMoney(*in.Tendered) || !centAmount(*in.Tendered) {
			return false
		}
	case "manual_non_cash":
		if in.Tendered != nil {
			return false
		}
	default:
		return false
	}
	for _, item := range in.Items {
		if !uuid.MatchString(item.SKU) || !uuid.MatchString(item.PriceID) ||
			(item.Batch != "" && !uuid.MatchString(item.Batch)) || len(item.Quantity) > 96 || !decimal.MatchString(item.Quantity) ||
			(item.Discount != "" && !validMoney(item.Discount)) {
			return false
		}
	}
	return true
}

// Lock order: authorization -> immutable sale ID -> outlet -> SKU in ID order
// -> stock balance keys owned by Inventory. Slot collisions only serialize.
func lockDocument(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('sales:'||$1::text||':'||$2::text,0))`, tenant, id)
	return err
}

func (s Service) Checkout(ctx context.Context, tx pgx.Tx, scope Scope, id string, in CheckoutInput) (Sale, error) {
	return s.checkout(ctx, tx, scope, id, in, false, nil)
}

func LockOfflineSaleID(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	return lockDocument(ctx, tx, tenant, id)
}

func (s Service) CheckoutOffline(ctx context.Context, tx pgx.Tx, scope Scope, id string, in CheckoutInput, historical *time.Time) (Sale, error) {
	if in.Method != "cash" {
		return Sale{}, ErrInput
	}
	return s.checkout(ctx, tx, scope, id, in, true, historical)
}

func (s Service) checkout(ctx context.Context, tx pgx.Tx, scope Scope, id string, in CheckoutInput, fresh bool, historical *time.Time) (Sale, error) {
	var result Sale
	if !uuid.MatchString(id) || !validCheckout(in) {
		return result, ErrInput
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return result, err
	}
	hash := sha256.Sum256(payload)
	err = pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorizeCheckout(ctx, tx, scope, in); err != nil {
			return err
		}
		if err := lockDocument(ctx, tx, scope.Tenant, id); err != nil {
			return err
		}
		if !fresh && s.OfflineFence != nil {
			if err := s.OfflineFence(ctx, tx, scope.Tenant, id); err != nil {
				return err
			}
		}
		var outlet, actor string
		var previous []byte
		err := tx.QueryRow(ctx, `SELECT outlet_id,actor_id,fingerprint FROM hive.sales_orders WHERE tenant_id=$1 AND id=$2`,
			scope.Tenant, id).Scan(&outlet, &actor, &previous)
		switch {
		case err == nil:
			if fresh || actor != scope.Actor || outlet != scope.Outlet || !bytes.Equal(previous, hash[:]) {
				return ErrConflict
			}
			result, err = load(ctx, tx, scope, id)
			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		outletLabel, err := locations.OutletLabel(ctx, tx, scope.Tenant, scope.Outlet)
		if err != nil {
			return err
		}
		footer, err := settings.ReceiptFooter(ctx, tx, scope.Tenant)
		if err != nil {
			return err
		}
		plans, err := s.priceItems(ctx, tx, scope, in.Items, historical)
		if err != nil {
			return err
		}
		nets := make([]string, len(plans))
		measures := make([]inventory.Measure, len(plans))
		for i, plan := range plans {
			nets[i] = plan.Net
			measures[i] = inventory.Measure{SKU: plan.SKU, Batch: plan.Batch, Quantity: plan.Quantity, Unit: plan.Unit}
		}
		var eligible bool
		var total string
		var change *string
		err = tx.QueryRow(ctx, `SELECT sum(n::numeric)::text,
            CASE WHEN $2::numeric IS NULL THEN NULL ELSE ($2::numeric-sum(n::numeric))::text END,
            $2::numeric IS NULL OR $2::numeric>=sum(n::numeric) FROM unnest($1::text[]) n`, nets, in.Tendered).
			Scan(&total, &change, &eligible)
		if err != nil {
			return err
		}
		if !eligible {
			return ErrTender
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.sales_orders(tenant_id,id,outlet_id,actor_id,device_id,fingerprint,total,
            method,tendered,change,receipt_footer,outlet_name) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7::numeric,$8,$9::numeric,$10::numeric,$11,$12)`,
			scope.Tenant, id, scope.Outlet, scope.Actor, scope.Device, hash[:], total, in.Method, in.Tendered, change, footer, outletLabel); err != nil {
			return err
		}
		for _, plan := range plans {
			if _, err = tx.Exec(ctx, `INSERT INTO hive.sales_items(tenant_id,sale_id,line,sku_id,batch_id,name,variant,unit,quantity,price_id,price_revision,unit_price,discount)
                VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,$8,$9::numeric,$10,$11,$12::numeric,$13::numeric)`,
				scope.Tenant, id, plan.Line, plan.SKU, plan.Batch, plan.Name, plan.Variant, plan.Unit, plan.Quantity, plan.PriceID, plan.PriceRevision, plan.UnitPrice, plan.Discount); err != nil {
				return err
			}
		}
		if in.Method == "cash" {
			if err = s.postInventorySale(ctx, tx, scope, id, measures); err != nil {
				return err
			}
		}
		result, err = load(ctx, tx, scope, id)
		return err
	})
	if err != nil {
		return Sale{}, err
	}
	return result, nil
}

func (s Service) priceItems(ctx context.Context, tx pgx.Tx, scope Scope, items []CheckoutItem, historical *time.Time) ([]Item, error) {
	// Acquire Catalog guards in stable SKU order while retaining caller line numbers.
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return strings.Compare(items[a].SKU, items[b].SKU) })
	plans := make([]Item, len(items))
	for _, i := range order {
		item := items[i]
		quote, err := s.Catalog.SellingPriceAt(ctx, tx, scope.Tenant, scope.Outlet, item.SKU, historical)
		if err != nil {
			return nil, err
		}
		if quote.ID != item.PriceID {
			return nil, ErrConflict
		}
		sku, err := catalog.SKUForOperation(ctx, tx, scope.Tenant, item.SKU)
		if err != nil {
			return nil, err
		}
		if sku.BatchTracked != (item.Batch != "") {
			return nil, ErrInput
		}
		if err = inventory.ValidateSaleBatch(ctx, tx, scope.Tenant, item.Measure); err != nil {
			return nil, err
		}
		quantity, err := catalog.QuantityForSKU(ctx, tx, scope.Tenant, item.SKU, item.Quantity, item.Unit)
		if err != nil {
			return nil, err
		}
		discount := item.Discount
		if discount == "" {
			discount = "0"
		}
		var net string
		var valid bool
		err = tx.QueryRow(ctx, `SELECT ($1::numeric*$2::numeric-$3::numeric)::text,
            $1::numeric>0 AND $1::numeric*$2::numeric >= $3::numeric
           AND $1::numeric*$2::numeric-$3::numeric=trunc($1::numeric*$2::numeric-$3::numeric,2)`, quantity, quote.Amount, discount).Scan(&net, &valid)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, ErrInput
		}
		plans[i] = Item{
			Line: i + 1, SKU: item.SKU, Batch: item.Batch, Name: sku.Name, Variant: sku.Variant, Unit: sku.Unit, Quantity: quantity,
			PriceID: quote.ID, PriceRevision: quote.Revision, UnitPrice: quote.Amount, Discount: discount, Net: net,
		}
	}
	return plans, nil
}

func (s Service) postInventorySale(ctx context.Context, tx pgx.Tx, scope Scope, id string, measures []inventory.Measure) error {
	stockScope := inventory.Scope{Tenant: scope.Tenant, Actor: scope.Actor, Device: scope.Device}
	inv := inventory.Service{Authorize: func(ctx context.Context, tx pgx.Tx, caller inventory.Scope, action, location string) error {
		if caller != stockScope || action != inventory.SalePermission || location != scope.Outlet {
			return ErrInput
		}
		return s.allowed(ctx, tx, scope, CheckoutPermission)
	}}
	return inv.PostSale(ctx, tx, stockScope, id, scope.Outlet, measures)
}

func (s Service) authorizeCheckout(ctx context.Context, tx pgx.Tx, scope Scope, in CheckoutInput) error {
	if err := s.allowed(ctx, tx, scope, CheckoutPermission); err != nil {
		return err
	}
	for _, item := range in.Items {
		if strings.Trim(item.Discount, "0.") != "" {
			if err := s.allowed(ctx, tx, scope, DiscountPermission); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// ValidateOfflineCash validates evidence before it is admitted to the journal.
func ValidateOfflineCash(in CheckoutInput) bool { return in.Method == "cash" && validCheckout(in) }

func UsesDiscount(in CheckoutInput) bool {
	for _, item := range in.Items {
		if strings.Trim(item.Discount, "0.") != "" {
			return true
		}
	}
	return false
}

func ValidateOfflineQuotes(prices []string, count int) bool {
	if len(prices) != count {
		return false
	}
	for _, price := range prices {
		if !validMoney(price) {
			return false
		}
	}
	return true
}

func SameOfflineMoney(a, b string) bool {
	normalize := func(value string) string {
		if strings.Contains(value, ".") {
			return strings.TrimRight(strings.TrimRight(value, "0"), ".")
		}
		return value
	}
	return normalize(a) == normalize(b)
}

func MatchesOfflineQuotes(sale Sale, prices []string) bool {
	if len(sale.Items) != len(prices) {
		return false
	}
	for i, item := range sale.Items {
		if !SameOfflineMoney(item.UnitPrice, prices[i]) {
			return false
		}
	}
	return true
}

func ValidOfflineRefund(value string) bool { return validMoney(value) && centAmount(value) }

// VerifyOfflineRefundAmount bounds a recorded manual refund by the actual
// signed checkout amount, after the original line discounts and SKU units.
func VerifyOfflineRefundAmount(ctx context.Context, tx pgx.Tx, tenant string, in CheckoutInput, prices []string, refund string) error {
	if !ValidateOfflineCash(in) || !ValidateOfflineQuotes(prices, len(in.Items)) || !ValidOfflineRefund(refund) {
		return ErrInput
	}
	quantities, discounts := make([]string, len(in.Items)), make([]string, len(in.Items))
	for i, item := range in.Items {
		quantity, err := catalog.QuantityForEvidence(ctx, tx, tenant, item.SKU, item.Quantity, item.Unit)
		if err != nil {
			return err
		}
		quantities[i] = quantity
		discounts[i] = item.Discount
		if discounts[i] == "" {
			discounts[i] = "0"
		}
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT $4::numeric>0 AND $4::numeric<=$5::numeric AND $4::numeric<=sum(net)
        AND bool_and(net>=0 AND net=trunc(net,2)) FROM (
        SELECT q::numeric*p::numeric-d::numeric AS net FROM unnest($1::text[],$2::text[],$3::text[]) AS lines(q,p,d)
        ) priced`, quantities, prices, discounts, refund, *in.Tendered).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrConflict
	}
	return nil
}

// OfflineOrderExists lets reconciliation distinguish an unposted checkout from
// a real order, which must instead be handled by the Sales reversal workflow.
func OfflineOrderExists(ctx context.Context, tx pgx.Tx, tenant, id string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.sales_orders WHERE tenant_id=$1 AND id=$2)`, tenant, id).Scan(&exists)
	return exists, err
}
