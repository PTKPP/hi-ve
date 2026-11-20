package transport

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/platform/entitlements"
)

func TestInventoryPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, actor, owner := ownerSessionFixture(t, db, "inventory-owner")
	other, otherActor, otherOwner := ownerSessionFixture(t, db, "inventory-other-owner")
	id := func(sql string, args ...any) string {
		t.Helper()
		var result string
		if err := db.QueryRow(ctx, sql, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	uuid := func() string { return id("SELECT gen_random_uuid()") }
	loc := func(tenant, code, kind string) string {
		return id(`INSERT INTO hive.locations(tenant_id,code,name,kind)
        VALUES($1,$2,$2,$3) RETURNING id`, tenant, code, kind)
	}
	outlet, warehouse, foreign := loc(tenant, "store", "outlet"), loc(tenant, "warehouse", "warehouse"), loc(other, "foreign", "outlet")
	destination := loc(tenant, "destination", "outlet")
	product := id(`INSERT INTO hive.catalog_products VALUES($1,gen_random_uuid(),'Madu','Pangan',1) RETURNING id`, tenant)
	sku := func(code, unit string) string {
		return id(`INSERT INTO hive.catalog_skus(tenant_id,id,product_id,code,variant,unit,active,batch_tracked,revision)
        VALUES($1,gen_random_uuid(),$2,$3,$3,$4,true,true,1) RETURNING id`, tenant, product, code, unit)
	}
	bulk, packed := sku("bulk", "g"), sku("packed", "pcs")
	batch := uuid()
	scope := inventory.Scope{Tenant: tenant, Actor: actor}
	denied := errors.New("inventory permission or capability denied")
	svc := inventory.Service{Authorize: func(_ context.Context, _ pgx.Tx, scope inventory.Scope, action, location string) error {
		if scope.Tenant == tenant && scope.Actor == actor && location != warehouse {
			return nil
		}
		return denied
	}}
	run := func(operation func(pgx.Tx) error) error { return pgx.BeginFunc(ctx, db, operation) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	receive := inventory.Receiving{Item: inventory.Measure{SKU: bulk, Batch: batch, Quantity: "1.000000000001", Unit: "kg"}, Lot: "lot-1", Origin: "supplier", ReceivedOn: "2026-01-01"}
	receipt := uuid()
	draft := func(tx pgx.Tx) error { return svc.DraftReceiving(ctx, tx, scope, receipt, outlet, receive) }
	post := func(tx pgx.Tx) error { return svc.PostReceiving(ctx, tx, scope, receipt, outlet) }
	must(run(draft))
	must(run(draft))
	if count := id(`SELECT count(*)::text FROM hive.inventory_movements WHERE tenant_id=$1`, tenant); count != "0" {
		t.Fatal("draft posted stock")
	}
	crossScope := inventory.Scope{Tenant: other, Actor: otherActor}
	if err := run(func(tx pgx.Tx) error { return svc.PostReceiving(ctx, tx, crossScope, receipt, outlet) }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	must(run(post))
	must(run(post))
	if err := run(func(tx pgx.Tx) error {
		return svc.PostSale(ctx, tx, scope, uuid(), foreign, []inventory.Measure{receive.Item})
	}); err == nil {
		t.Fatal("foreign outlet accepted")
	}
	if err := run(func(tx pgx.Tx) error {
		return svc.PostSale(ctx, tx, scope, uuid(), warehouse, []inventory.Measure{receive.Item})
	}); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	outputBatch := uuid()
	repackID := uuid()
	repack := inventory.Repacking{Source: inventory.Measure{SKU: bulk, Batch: batch, Quantity: "1", Unit: "g"},
		Output: inventory.Measure{SKU: packed, Batch: outputBatch, Quantity: "1", Unit: "pcs"}, Lot: "packed-1"}
	must(run(func(tx pgx.Tx) error { return svc.DraftRepacking(ctx, tx, scope, repackID, outlet, repack) }))
	must(run(func(tx pgx.Tx) error { return svc.PostRepacking(ctx, tx, scope, repackID, outlet) }))
	if source := id(`SELECT source_batch_id FROM hive.inventory_batch_lineage WHERE tenant_id=$1 AND output_batch_id=$2`, tenant, outputBatch); source != batch {
		t.Fatal(source)
	}

	stopped := repack
	stopped.Source.Quantity = "999999"
	stopped.Output.Batch = uuid()
	stoppedID := uuid()
	must(run(func(tx pgx.Tx) error { return svc.DraftRepacking(ctx, tx, scope, stoppedID, outlet, stopped) }))
	if err := run(func(tx pgx.Tx) error { return svc.PostRepacking(ctx, tx, scope, stoppedID, outlet) }); !errors.Is(err, inventory.ErrShortage) {
		t.Fatal(err)
	}
	if count := id(`SELECT count(*)::text FROM hive.inventory_batches WHERE tenant_id=$1 AND id=$2`, tenant, stopped.Output.Batch); count != "0" {
		t.Fatal("failed repack created a batch")
	}

	transferID := uuid()
	transfer := inventory.Transfer{Destination: destination, Item: inventory.Measure{SKU: bulk, Batch: batch, Quantity: "10", Unit: "g"}}
	must(run(func(tx pgx.Tx) error { return svc.DraftTransfer(ctx, tx, scope, transferID, outlet, transfer) }))
	must(run(func(tx pgx.Tx) error { return svc.DispatchTransfer(ctx, tx, scope, transferID, outlet) }))
	must(run(func(tx pgx.Tx) error { return svc.DispatchTransfer(ctx, tx, scope, transferID, outlet) }))
	if err := run(func(tx pgx.Tx) error {
		return svc.PostSale(ctx, tx, scope, uuid(), destination, []inventory.Measure{transfer.Item})
	}); !errors.Is(err, inventory.ErrShortage) {
		t.Fatal(err)
	}
	receiveTransfer := inventory.TransferReceipt{Transfer: transferID, Source: outlet, Destination: destination, Quantity: "8", Unit: "g"}
	receiveID := uuid()
	must(run(func(tx pgx.Tx) error { return svc.ReceiveTransfer(ctx, tx, scope, receiveID, receiveTransfer) }))
	must(run(func(tx pgx.Tx) error { return svc.ReceiveTransfer(ctx, tx, scope, receiveID, receiveTransfer) }))
	if summary := id(`SELECT remaining::text||'/'||received::text FROM hive.inventory_transfers WHERE tenant_id=$1 AND id=$2`, tenant, transferID); summary != "2/8" {
		t.Fatal(summary)
	}
	if err := run(func(tx pgx.Tx) error { return svc.ReceiveTransfer(ctx, tx, scope, uuid(), receiveTransfer) }); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
	receiveTransfer.Quantity, receiveTransfer.DiscrepancyReason = "2", "missing"
	discrepancyID := uuid()
	must(run(func(tx pgx.Tx) error { return svc.ReceiveTransfer(ctx, tx, scope, discrepancyID, receiveTransfer) }))
	if summary := id(`SELECT remaining::text||'/'||received::text||'/'||discrepancy::text FROM hive.inventory_transfers WHERE tenant_id=$1 AND id=$2`, tenant, transferID); summary != "0/8/2" {
		t.Fatal(summary)
	}
	countID := uuid()
	must(run(func(tx pgx.Tx) error {
		return svc.StartCount(ctx, tx, scope, countID, destination, []inventory.CountItem{{SKU: bulk, Batch: batch}})
	}))
	must(run(func(tx pgx.Tx) error {
		return svc.StartCount(ctx, tx, scope, countID, destination, []inventory.CountItem{{SKU: bulk, Batch: batch}})
	}))
	physical := inventory.Measure{SKU: bulk, Batch: batch, Quantity: "0", Unit: "g"}
	observation := uuid()
	observe := func(tx pgx.Tx) error {
		variance, err := svc.RecordCount(ctx, tx, scope, countID, observation, destination, physical)
		if err == nil && (variance.Snapshot != "8" || variance.Physical != "0" || variance.Difference != "-8" || variance.StockChanged) {
			t.Fatalf("variance %+v", variance)
		}
		return err
	}
	must(run(observe))
	must(run(observe))
	quickID := uuid()
	transfer.Quick = true
	must(run(func(tx pgx.Tx) error { return svc.DraftTransfer(ctx, tx, scope, quickID, outlet, transfer) }))
	must(run(func(tx pgx.Tx) error { return svc.DispatchTransfer(ctx, tx, scope, quickID, outlet) }))
	must(run(func(tx pgx.Tx) error {
		variance, err := svc.RecordCount(ctx, tx, scope, countID, observation, destination, physical)
		if err == nil && !variance.StockChanged {
			t.Fatal("stale snapshot accepted")
		}
		return err
	}))
	must(run(func(tx pgx.Tx) error {
		return svc.DraftAdjustment(ctx, tx, scope, uuid(), destination, inventory.Adjustment{Item: transfer.Item, Decrease: true, Reason: "audit difference"})
	}))

	// Roll back a successful debit with its enclosing Sales transaction.
	abort := errors.New("checkout failed")
	saleID := uuid()
	if err := run(func(tx pgx.Tx) error {
		if err := svc.PostSale(ctx, tx, scope, saleID, outlet, []inventory.Measure{repack.Output}); err != nil {
			return err
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatal(err)
	}
	result := make(chan error, 2)
	competitor := uuid()
	for _, document := range []string{saleID, competitor} {
		go func() {
			result <- run(func(tx pgx.Tx) error {
				return svc.PostSale(ctx, tx, scope, document, outlet, []inventory.Measure{repack.Output})
			})
		}()
	}
	a, b := <-result, <-result
	if !(a == nil && errors.Is(b, inventory.ErrShortage) || b == nil && errors.Is(a, inventory.ErrShortage)) {
		t.Fatalf("competing sale: %v/%v", a, b)
	}
	winner := id(`SELECT document_id FROM hive.inventory_postings WHERE tenant_id=$1 AND kind='sale'`, tenant)
	must(run(func(tx pgx.Tx) error {
		return svc.PostSale(ctx, tx, scope, winner, outlet, []inventory.Measure{repack.Output})
	}))
	if count := id(`SELECT count(*)::text FROM hive.inventory_movements WHERE tenant_id=$1 AND kind='sale'`, tenant); count != "1" {
		t.Fatal(count)
	}
	if err := run(func(tx pgx.Tx) error {
		return svc.PostSale(ctx, tx, scope, winner, outlet, []inventory.Measure{receive.Item})
	}); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
	if match := id(`SELECT bool_and(b.quantity=(SELECT sum(m.delta) FROM hive.inventory_movements m
        WHERE (m.tenant_id,m.location_id,m.sku_id,m.bucket)=(b.tenant_id,b.location_id,b.sku_id,b.bucket)
        AND m.batch_id IS NOT DISTINCT FROM b.batch_id))::text FROM hive.inventory_balances b WHERE tenant_id=$1`, tenant); match != "true" {
		t.Fatal(match)
	}
	for _, query := range []string{"UPDATE hive.inventory_movements SET delta=2", "DELETE FROM hive.inventory_postings", "TRUNCATE hive.inventory_movements"} {
		_, err := db.Exec(ctx, query)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatal(err)
		}
	}

	must(run(func(tx pgx.Tx) error {
		balances, err := svc.Stock(ctx, tx, scope, destination, bulk)
		if err != nil {
			return err
		}
		if len(balances) != 2 || balances[0].Bucket != "available" || balances[0].Quantity != "18" ||
			balances[1].Bucket != "discrepancy" || balances[1].Quantity != "2" {
			t.Fatalf("destination balance %+v", balances)
		}
		history, err := svc.BatchHistory(ctx, tx, scope, destination, batch, nil)
		if err != nil {
			return err
		}
		if len(history) != 3 || history[0].Document != quickID {
			t.Fatalf("destination history %+v", history)
		}
		page, err := svc.BatchHistory(ctx, tx, scope, destination, batch, &inventory.TraceCursor{
			PostedAt: history[0].PostedAt, Kind: history[0].Kind, Document: history[0].Document, Line: history[0].Line,
		})
		if err == nil && len(page) != 2 {
			t.Fatal("history cursor")
		}
		return err
	}))

	approval := inventory.CountApproval{Observations: []string{observation}, Reason: "reviewed physical count"}
	if err := run(func(tx pgx.Tx) error { return svc.ApproveCount(ctx, tx, scope, countID, destination, approval) }); !errors.Is(err, inventory.ErrStaleSnapshot) {
		t.Fatal(err)
	}
	freshCount, freshObservation := uuid(), uuid()
	must(run(func(tx pgx.Tx) error {
		return svc.StartCount(ctx, tx, scope, freshCount, destination, []inventory.CountItem{{SKU: bulk, Batch: batch}})
	}))
	physical.Quantity = "17"
	must(run(func(tx pgx.Tx) error {
		_, err := svc.RecordCount(ctx, tx, scope, freshCount, freshObservation, destination, physical)
		return err
	}))
	approval.Observations = []string{freshObservation}
	countApprove := func(tx pgx.Tx) error { return svc.ApproveCount(ctx, tx, scope, freshCount, destination, approval) }
	must(run(countApprove))
	must(run(countApprove))
	if qty := id(`SELECT quantity::text FROM hive.inventory_balances b JOIN hive.inventory_locations l ON(l.tenant_id,l.id)=(b.tenant_id,b.location_id)
        WHERE b.tenant_id=$1 AND l.location_id=$2 AND b.sku_id=$3 AND b.bucket='available'`, tenant, destination, bulk); qty != "17" {
		t.Fatal(qty)
	}
	resolve := func(tx pgx.Tx) error {
		return svc.ResolveDiscrepancy(ctx, tx, scope, discrepancyID, destination, inventory.DiscrepancyResolution{Reason: "confirmed found", Found: true})
	}
	must(run(resolve))
	must(run(resolve))
	if summary := id(`SELECT resolved_discrepancy::text FROM hive.inventory_transfers WHERE tenant_id=$1 AND id=$2`, tenant, transferID); summary != "2" {
		t.Fatal(summary)
	}

	correctionID := uuid()
	correction := inventory.Adjustment{Item: inventory.Measure{SKU: bulk, Batch: batch, Quantity: "1", Unit: "g"}, Decrease: true, Reason: "weighed again"}
	must(run(func(tx pgx.Tx) error {
		return svc.DraftAdjustment(ctx, tx, scope, correctionID, destination, correction)
	}))
	if err := run(func(tx pgx.Tx) error { return svc.ApproveAdjustment(ctx, tx, scope, correctionID, destination, "") }); !errors.Is(err, inventory.ErrInput) {
		t.Fatal(err)
	}
	correctionApprove := func(tx pgx.Tx) error {
		return svc.ApproveAdjustment(ctx, tx, scope, correctionID, destination, "owner verified")
	}
	approvals := make(chan error, 2)
	for range 2 {
		go func() { approvals <- run(correctionApprove) }()
	}
	must(<-approvals)
	must(<-approvals)
	if count := id(`SELECT count(*)::text FROM hive.inventory_approvals WHERE tenant_id=$1 AND document_id=$2 AND requester_id=$3 AND reason='owner verified'`, tenant, correctionID, actor); count != "1" {
		t.Fatal(count)
	}
	if count := id(`SELECT count(*)::text FROM hive.inventory_movements WHERE tenant_id=$1 AND document_id=$2`, tenant, correctionID); count != "1" {
		t.Fatal(count)
	}
	rejectedID := uuid()
	must(run(func(tx pgx.Tx) error { return svc.DraftAdjustment(ctx, tx, scope, rejectedID, destination, correction) }))
	must(run(func(tx pgx.Tx) error {
		return svc.RejectCorrection(ctx, tx, scope, rejectedID, destination, "measurement rejected", false)
	}))
	if err := run(func(tx pgx.Tx) error {
		return svc.ApproveAdjustment(ctx, tx, scope, rejectedID, destination, "would mutate")
	}); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}

	must(run(func(tx pgx.Tx) error {
		history, err := svc.AdjustmentHistory(ctx, tx, scope, destination, inventory.AdjustmentFilter{
			SKU: bulk, Reason: "weighed again",
		})
		if err != nil {
			return err
		}
		if len(history) != 2 || history[0].State != "rejected" || history[1].State != "posted" {
			t.Fatalf("adjustment history %+v", history)
		}
		return nil
	}))
	if consistent := id(`SELECT bool_and(b.quantity=COALESCE((SELECT sum(m.delta) FROM hive.inventory_movements m
        WHERE (m.tenant_id,m.location_id,m.sku_id,m.bucket)=(b.tenant_id,b.location_id,b.sku_id,b.bucket)
        AND m.batch_id IS NOT DISTINCT FROM b.batch_id),0))::text FROM hive.inventory_balances b WHERE tenant_id=$1`, tenant); consistent != "true" {
		t.Fatal("approval left mismatched ledger", consistent)
	}

	// Exercise the live Owner/API grant path without provisioning tenant data outside the fixture.
	app := API{InventoryDB: db, Devices: devices.Service{InventoryCommands: true}, Entitlements: entitlements.Service{DB: db}, AppOrigin: appOrigin, AdminOrigin: adminOrigin}.Handler()
	apiID := uuid()
	apiReceipt := receive
	apiReceipt.Item.Batch = uuid()
	path := "/api/v1/app/tenants/" + tenant + "/locations/" + warehouse + "/inventory/receiving/" + apiID
	expectStatus(t, accessRequest(app, "PUT", path, appOrigin, "", apiReceipt, otherOwner), 401)
	expectStatus(t, accessRequest(app, "PUT", path, appOrigin, "", apiReceipt, owner), 403)
	admin := entitlementAdminFixture(t, db, "inventory-platform")
	grantPath := "/api/v1/admin/tenants/" + tenant + "/entitlements/inventory.receiving"
	now := time.Now().UTC()
	expectStatus(t, accessRequest(app, "PUT", grantPath, adminOrigin, "inventory-receiving-grant", map[string]any{
		"revision": 0, "effective_at": now.Add(-time.Hour), "expires_at": now.Add(time.Hour), "source_reference": "inventory-test",
	}, admin), 200)
	badReceipt := apiReceipt
	badReceipt.Item.Quantity = "0"
	expectStatus(t, accessRequest(app, "PUT", path, appOrigin, "", badReceipt, owner), 400)
	expectStatus(t, accessRequest(app, "PUT", path, appOrigin, "", apiReceipt, owner), 204)
	expectStatus(t, accessRequest(app, "POST", path+"/post", appOrigin, "", nil, owner), 204)
	expectStatus(t, accessRequest(app, "POST", path+"/post", appOrigin, "", nil, owner), 204)
	expectStatus(t, accessRequest(app, "POST", grantPath+"/revoke", adminOrigin, "inventory-receiving-revoke", map[string]any{
		"revision": 1, "reason_code": "administrative_revocation",
	}, admin), 200)
	expectStatus(t, accessRequest(app, "POST", path+"/post", appOrigin, "", nil, owner), 403)
	adjustmentGrant := "/api/v1/admin/tenants/" + tenant + "/entitlements/inventory.adjustment"
	expectStatus(t, accessRequest(app, "PUT", adjustmentGrant, adminOrigin, "adjustment-history-grant", map[string]any{
		"revision": 0, "effective_at": now.Add(-time.Hour), "expires_at": now.Add(time.Hour), "source_reference": "inventory-test",
	}, admin), 200)
	historyPath := "/api/v1/app/tenants/" + tenant + "/locations/" + destination + "/inventory/adjustments/" + bulk + "/history?reason=weighed+again"
	expectStatus(t, accessRequest(app, "GET", historyPath, appOrigin, "", nil, otherOwner), 401)
	historyResponse := accessRequest(app, "GET", historyPath, appOrigin, "", nil, owner)
	expectStatus(t, historyResponse, 200)
	var history []inventory.AdjustmentHistoryRow
	if err := json.Unmarshal(historyResponse.Body.Bytes(), &history); err != nil || len(history) != 2 {
		t.Fatal(err, history)
	}

}
