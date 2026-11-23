package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/sales"
)

func TestSalesPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, owner, _ := ownerSessionFixture(t, db, "sales-owner")
	foreign, foreignActor, _ := ownerSessionFixture(t, db, "sales-foreign")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	id := func(q string, args ...any) string {
		t.Helper()
		var result string
		must(db.QueryRow(ctx, q, args...).Scan(&result))
		return result
	}
	uuid := func() string { return id("SELECT gen_random_uuid()") }
	exec := func(q string, args ...any) { t.Helper(); _, err := db.Exec(ctx, q, args...); must(err) }
	loc := func(tenant, code string) string {
		return id(`INSERT INTO hive.locations(tenant_id,code,name,kind) VALUES($1,$2,$2,'outlet') RETURNING id`, tenant, code)
	}
	outlet, otherOutlet, foreignOutlet := loc(tenant, "sales-outlet"), loc(tenant, "sales-other"), loc(foreign, "sales-outlet")
	staff := id(`WITH p AS (INSERT INTO hive.identity_principals(id) VALUES(gen_random_uuid()) RETURNING id)
        INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) SELECT $1,id,'staff' FROM p RETURNING principal_id`, tenant)
	ownerScope := sales.Scope{Tenant: tenant, Actor: owner, Outlet: outlet}
	staffScope := sales.Scope{Tenant: tenant, Actor: staff, Outlet: outlet}
	deny := errors.New("scope denied")
	run := func(fn func(pgx.Tx) error) error { return pgx.BeginFunc(ctx, db, fn) }
	allow := func(_ context.Context, _ pgx.Tx, scope sales.Scope, _ string) error {
		if scope.Tenant != tenant || scope.Outlet != outlet || scope.Actor != owner && scope.Actor != staff {
			return deny
		}
		return nil
	}
	s := sales.Service{
		Catalog:   catalog.Service{LockOutlets: locations.LockActiveOutlets, CheckCapability: func(context.Context, pgx.Tx, string, string) error { return nil }},
		Authorize: allow, IsPrimaryOwner: func(_ context.Context, _ pgx.Tx, scope sales.Scope) (bool, error) { return scope.Actor == owner, nil },
	}
	product := id(`INSERT INTO hive.catalog_products VALUES($1,gen_random_uuid(),'Madu','Pangan',1) RETURNING id`, tenant)
	sku := id(`INSERT INTO hive.catalog_skus(tenant_id,id,product_id,code,variant,unit,active,batch_tracked,revision)
        VALUES($1,gen_random_uuid(),$2,'madu','Curah','pcs',true,false,1) RETURNING id`, tenant, product)
	price := id(`INSERT INTO hive.catalog_prices(tenant_id,sku_id,outlet_id,revision,amount,actor_id)
        VALUES($1,$2,$3,1,0.04,$4) RETURNING id`, tenant, sku, outlet, owner)
	exec(`INSERT INTO hive.tenant_receipt_settings(tenant_id,receipt_footer,revision) VALUES($1,'Original footer',1)`, tenant)
	stock := inventory.Service{Authorize: func(context.Context, pgx.Tx, inventory.Scope, string, string) error { return nil }}
	stockScope := inventory.Scope{Tenant: tenant, Actor: owner}
	receive := uuid()
	draft := inventory.Receiving{Item: inventory.Measure{SKU: sku, Quantity: "4", Unit: "pcs"}, Origin: "supplier", ReceivedOn: "2026-01-01"}
	must(run(func(tx pgx.Tx) error {
		if err := stock.DraftReceiving(ctx, tx, stockScope, receive, outlet, draft); err != nil {
			return err
		}
		return stock.PostReceiving(ctx, tx, stockScope, receive, outlet)
	}))
	cash := "1.00"
	checkout := func(scope sales.Scope, doc string, in sales.CheckoutInput) (sales.Sale, error) {
		var result sales.Sale
		err := run(func(tx pgx.Tx) error { var err error; result, err = s.Checkout(ctx, tx, scope, doc, in); return err })
		return result, err
	}
	item := sales.CheckoutItem{Measure: inventory.Measure{SKU: sku, Quantity: "3", Unit: "pcs"}, PriceID: price, Discount: "0.02"}
	input := sales.CheckoutInput{Method: "cash", Tendered: &cash, Items: []sales.CheckoutItem{item}}
	doc := uuid()
	posted, err := checkout(staffScope, doc, input)
	must(err)
	if posted.Total != "0.10" || posted.Change == nil || *posted.Change != "0.90" || posted.ReceiptFooter == nil || *posted.ReceiptFooter != "Original footer" {
		t.Fatalf("checkout %+v", posted)
	}
	encoded, err := json.Marshal(posted)
	must(err)
	for _, field := range []string{`"hpp":`, `"cost":`, `"margin":`, `"valuation":`, `"fingerprint":`} {
		if bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("sales response leaked %s", field)
		}
	}
	empty := input
	small := "0.01"
	empty.Tendered = &small
	if _, err = checkout(staffScope, uuid(), empty); !errors.Is(err, sales.ErrTender) {
		t.Fatal(err)
	}
	if _, err = checkout(staffScope, doc, empty); !errors.Is(err, sales.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = checkout(sales.Scope{Tenant: foreign, Actor: foreignActor, Outlet: foreignOutlet}, uuid(), input); !errors.Is(err, deny) {
		t.Fatal(err)
	}
	one := item
	one.Quantity, one.Discount = "1", ""
	results := make(chan error, 2)
	for _, candidate := range []string{uuid(), uuid()} {
		go func() {
			_, err := checkout(staffScope, candidate, sales.CheckoutInput{Method: "cash", Tendered: &cash, Items: []sales.CheckoutItem{one}})
			results <- err
		}()
	}
	a, b := <-results, <-results
	if !(a == nil && errors.Is(b, inventory.ErrShortage) || b == nil && errors.Is(a, inventory.ErrShortage)) {
		t.Fatalf("stock race %v/%v", a, b)
	}
	exec(`UPDATE hive.tenant_receipt_settings SET receipt_footer='Different' WHERE tenant_id=$1`, tenant)
	newPrice := id(`INSERT INTO hive.catalog_prices(tenant_id,sku_id,outlet_id,revision,amount,actor_id) VALUES($1,$2,$3,2,9,$4) RETURNING id`, tenant, sku, outlet, owner)
	replay, err := checkout(staffScope, doc, input)
	must(err)
	if replay.Total != posted.Total || *replay.ReceiptFooter != *posted.ReceiptFooter || replay.Items[0].PriceID != price {
		t.Fatal("snapshot changed")
	}
	if _, err = checkout(staffScope, uuid(), input); !errors.Is(err, sales.ErrConflict) {
		t.Fatal(err)
	}

	saleable := true
	request := sales.ReversalInput{Reason: "barang dikembalikan", Items: []sales.ReturnItem{{Line: 1, Quantity: "1", Saleable: &saleable}}}
	requestReturn := func(reversal string, actor sales.Scope, void bool, req sales.ReversalInput) error {
		return run(func(tx pgx.Tx) error {
			_, err := s.RequestReversal(ctx, tx, actor, doc, reversal, void, req)
			return err
		})
	}
	approve := func(reversal, reason string, actor sales.Scope, void bool) (sales.Reversal, error) {
		var result sales.Reversal
		err := run(func(tx pgx.Tx) error {
			var err error
			result, err = s.ApproveReversal(ctx, tx, actor, doc, reversal, reason, void)
			return err
		})
		return result, err
	}
	first, second, third, overflow := uuid(), uuid(), uuid(), uuid()
	for _, r := range []string{first, second, third, overflow} {
		must(requestReturn(r, staffScope, false, request))
	}
	if _, err = approve(first, "self", staffScope, false); !errors.Is(err, sales.ErrSelfApproval) {
		t.Fatal(err)
	}
	for _, r := range []string{first, second} {
		go func() {
			result, err := approve(r, "", ownerScope, false)
			if err == nil && (result.Refund == nil || *result.Refund != "0.03") {
				err = errors.New("incorrect cumulative floor")
			}
			results <- err
		}()
	}
	must(<-results)
	must(<-results)
	for range 2 {
		go func() {
			result, err := approve(third, "", ownerScope, false)
			if err == nil && (result.Refund == nil || *result.Refund != "0.04") {
				err = errors.New("missing final remainder")
			}
			results <- err
		}()
	}
	must(<-results)
	must(<-results)
	if _, err = approve(overflow, "", ownerScope, false); !errors.Is(err, sales.ErrConflict) {
		t.Fatal(err)
	}
	if total := id(`SELECT sum(refund)::text FROM hive.sales_reversal_items WHERE tenant_id=$1 AND sale_id=$2`, tenant, doc); total != "0.10" {
		t.Fatal(total)
	}
	void := uuid()
	must(requestReturn(void, ownerScope, true, sales.ReversalInput{Reason: "correction"}))
	if _, err = approve(void, "owner reason", ownerScope, true); !errors.Is(err, sales.ErrConflict) {
		t.Fatal(err)
	}
	confirmRefund := func(ref string) error {
		return run(func(tx pgx.Tx) error {
			r, err := s.ConfirmRefund(ctx, tx, ownerScope, doc, first, ref)
			if err == nil && r.RefundStatus != "manually_recorded_completed" {
				t.Fatal(r.RefundStatus)
			}
			return err
		})
	}
	must(confirmRefund("transfer acknowledgement"))
	must(confirmRefund("transfer acknowledgement"))
	if err = confirmRefund("different reference"); !errors.Is(err, sales.ErrConflict) {
		t.Fatal(err)
	}

	one.PriceID = newPrice
	manual := uuid()
	pending, err := checkout(staffScope, manual, sales.CheckoutInput{Method: "manual_non_cash", Items: []sales.CheckoutItem{one}})
	must(err)
	if pending.PaymentState != "pending_manual_confirmation" {
		t.Fatal(pending.PaymentState)
	}
	if count := id(`SELECT count(*)::text FROM hive.inventory_postings WHERE tenant_id=$1 AND kind='sale' AND document_id=$2`, tenant, manual); count != "0" {
		t.Fatal(count)
	}
	for range 2 {
		go func() {
			results <- run(func(tx pgx.Tx) error {
				actual, err := s.ConfirmManual(ctx, tx, staffScope, manual, sales.ManualConfirmation{Reference: "manual receipt"})
				if err == nil && actual.PaymentState != "manually_confirmed" {
					return errors.New("wrong payment status")
				}
				return err
			})
		}()
	}
	must(<-results)
	must(<-results)

	// Fail the Inventory ledger insert to prove that financial reversal and approval roll back.
	manualVoid := uuid()
	must(run(func(tx pgx.Tx) error {
		_, err := s.RequestReversal(ctx, tx, ownerScope, manual, manualVoid, true, sales.ReversalInput{Reason: "wrong order"})
		return err
	}))
	manualApprove := func(reason string) error {
		return run(func(tx pgx.Tx) error {
			_, err := s.ApproveReversal(ctx, tx, ownerScope, manual, manualVoid, reason, true)
			return err
		})
	}
	if err = manualApprove(""); !errors.Is(err, sales.ErrInput) {
		t.Fatal(err)
	}
	exec(`ALTER TABLE hive.inventory_movements ADD CONSTRAINT fixture_sales_reversal CHECK(kind<>'sale.reversal') NOT VALID`)
	if err = manualApprove("recorded owner self-approval"); err == nil {
		t.Fatal("expected stock failure")
	}
	if count := id(`SELECT count(*)::text FROM hive.sales_reversal_postings WHERE tenant_id=$1 AND id=$2`, tenant, manualVoid); count != "0" {
		t.Fatal(count)
	}
	exec(`ALTER TABLE hive.inventory_movements DROP CONSTRAINT fixture_sales_reversal`)
	must(manualApprove("recorded owner self-approval"))
	must(manualApprove("recorded owner self-approval"))

	must(run(func(tx pgx.Tx) error {
		rows, err := s.History(ctx, tx, ownerScope, sales.HistoryFilter{Operator: staff, Total: "0.10"})
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].ID != doc {
			t.Fatalf("history %+v", rows)
		}
		rows, err = s.History(ctx, tx, ownerScope, sales.HistoryFilter{Before: &rows[0].CreatedAt, BeforeID: doc})
		if err != nil || len(rows) != 0 {
			t.Fatalf("history cursor %+v %v", rows, err)
		}
		detail, err := s.Detail(ctx, tx, ownerScope, doc, false)
		if err != nil || len(detail.Reversals) != 5 {
			t.Fatalf("detail %+v %v", detail.Reversals, err)
		}
		_, err = s.Detail(ctx, tx, sales.Scope{Tenant: tenant, Actor: owner, Outlet: otherOutlet}, doc, true)
		if !errors.Is(err, deny) {
			t.Fatal(err)
		}
		return nil
	}))
	_, err = db.Exec(ctx, `UPDATE hive.sales_orders SET total=0 WHERE tenant_id=$1`, tenant)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
		t.Fatal(err)
	}
}
