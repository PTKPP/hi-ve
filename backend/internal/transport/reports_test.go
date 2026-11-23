package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/platform/provisioning"
	"hi-ve/backend/internal/realtime"
	"hi-ve/backend/internal/reports"
)

func TestReportsPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, owner, session := ownerSessionFixture(t, db, "reports-owner")
	foreign, foreignActor, foreignSession := ownerSessionFixture(t, db, "reports-foreign")
	admin := entitlementAdminFixture(t, db, "reports-platform")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	id := func(q string, args ...any) string {
		t.Helper()
		var out string
		must(db.QueryRow(ctx, q, args...).Scan(&out))
		return out
	}
	exec := func(q string, args ...any) { t.Helper(); _, err := db.Exec(ctx, q, args...); must(err) }
	caps := []string{"reports.dashboard", "reports.sales", "reports.inventory", "reports.stock_movements", "realtime.events", "realtime.notifications"}
	exec(`INSERT INTO hive.tenant_entitlements SELECT $1,cap,now()-interval '1 day',now()+interval '1 day','test',NULL,1 FROM unnest($2::text[]) cap`, tenant, caps)
	p := entitlements.Service{DB: db}
	a := API{AppOrigin: appOrigin, AdminOrigin: adminOrigin, Reports: reports.Service{DB: db, Entitlements: p},
		Realtime: realtime.Service{DB: db, Entitlements: p}, Provisioning: provisioning.Service{DB: db}}
	h := a.Handler()
	api := func(method, path string, in any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		return accessRequest(h, method, "/api/v1/app/tenants/"+tenant+path, appOrigin, "", in, cookie)
	}
	adminRead := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(h, "GET", "/api/v1/admin/tenants"+path, adminOrigin, "", nil, cookie)
	}
	expectStatus(t, adminRead("", session), 401)
	expectStatus(t, adminRead("", admin), 403)
	expectStatus(t, adminRead("/"+tenant+"/audit", admin), 403)
	adminID := id(`SELECT principal_id FROM hive.platform_sessions WHERE digest=$1`, identity.Digest(admin.Value))
	exec(`INSERT INTO hive.platform_permissions VALUES($1,'platform.tenants.read'),($1,'platform.audit.read')`, adminID)
	exec(`INSERT INTO hive.provisioning_audit(tenant_id,actor_id,action,outcome,verification_reference)
        VALUES($1,$2,'tenant.activate','succeeded','restricted-reference')`, tenant, adminID)
	expectStatus(t, adminRead("", admin), 200)
	exec(`INSERT INTO hive.entitlement_audit(tenant_id,capability_id,actor_id,action,previous_revision,revision,reason_code)
        VALUES($1,'reports.dashboard',$2,'grant',0,1,'internal-test-reason')`, tenant, adminID)
	auditPage := adminRead("/"+tenant+"/audit", admin)
	expectStatus(t, auditPage, 200)
	if !strings.Contains(auditPage.Body.String(), "tenant.activate") || !strings.Contains(auditPage.Body.String(), "entitlement.grant") ||
		strings.Contains(auditPage.Body.String(), "internal-test-reason") || strings.Contains(auditPage.Body.String(), "restricted-reference") {
		t.Fatal("incorrect platform audit projection")
	}

	loc := func(tenant, code string) string {
		return id(`INSERT INTO hive.locations(tenant_id,name,code,kind) VALUES($1,$2,$2,'outlet') RETURNING id`, tenant, code)
	}
	l1, l2, l3, fx := loc(tenant, "rpt-one"), loc(tenant, "rpt-two"), loc(tenant, "rpt-three"), loc(foreign, "rpt-foreign")
	order := func(tn, actor, location, amount, method string) string {
		return id(`INSERT INTO hive.sales_orders(tenant_id,id,actor_id,outlet_id,fingerprint,total,method,tendered,change,outlet_name)
		 VALUES($1,gen_random_uuid(),$2,$3,sha256('fixture'::bytea),$4,$5,
		 CASE WHEN $5='cash' THEN $4::numeric END,CASE WHEN $5='cash' THEN 0 END,'Outlet snapshot') RETURNING id`, tn, actor, location, amount, method)
	}
	first := order(tenant, owner, l1, "500000", "cash")
	order(tenant, owner, l2, "300000", "cash")
	order(tenant, owner, l3, "200000", "cash")
	order(tenant, owner, l2, "120000", "manual_non_cash")
	order(foreign, foreignActor, fx, "3000000", "cash")
	product := id(`INSERT INTO hive.catalog_products VALUES($1,gen_random_uuid(),'Produk','Kategori',1) RETURNING id`, tenant)
	sku := id(`INSERT INTO hive.catalog_skus(tenant_id,id,product_id,code,variant,unit,active,batch_tracked,revision)
		VALUES($1,gen_random_uuid(),$2,'rpt','Standar','pcs',true,false,1) RETURNING id`, tenant, product)
	price := id(`INSERT INTO hive.catalog_prices(tenant_id,sku_id,outlet_id,revision,amount,actor_id) VALUES($1,$2,$3,1,510000,$4) RETURNING id`, tenant, sku, l1, owner)
	exec(`INSERT INTO hive.sales_items(tenant_id,sale_id,line,sku_id,name,variant,unit,quantity,price_id,price_revision,unit_price,discount)
	 VALUES($1,$2,1,$3,'Produk','Standar','pcs',1,$4,1,510000,10000)`, tenant, first, sku, price)
	stockNode := id(`SELECT id FROM hive.inventory_locations WHERE tenant_id=$1 AND location_id=$2`, tenant, l1)
	transfer := id(`INSERT INTO hive.inventory_documents(tenant_id,kind,id,location_id,created_by,payload)
	 VALUES($1,'transfer',gen_random_uuid(),$2,$3,jsonb_build_object('destination_id',$4::text)) RETURNING id`, tenant, l1, owner, l2)
	exec(`INSERT INTO hive.inventory_postings(tenant_id,kind,document_id,actor_id,fingerprint) VALUES($1,'transfer',$2,$3,sha256('fixture'::bytea))`, tenant, transfer, owner)
	exec(`INSERT INTO hive.inventory_transfers(tenant_id,id,source_node,dispatched,remaining,received,discrepancy) VALUES($1,$2,$3,8,2,4,2)`, tenant, transfer, stockNode)
	exec(`INSERT INTO hive.inventory_balances(tenant_id,location_id,sku_id,bucket,quantity,revision) VALUES($1,$2,$3,'in_transit',2,1)`, tenant, stockNode, sku)
	exec(`INSERT INTO hive.inventory_movements VALUES($1,'transfer',$2,1,$3,$4,NULL,'in_transit',2)`, tenant, transfer, stockNode, sku)
	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	window := "?from=" + from.UTC().Format(time.RFC3339) + "&to=" + to.UTC().Format(time.RFC3339)
	call := func(path string) *httptest.ResponseRecorder { return api("GET", path, nil, session) }
	expectStatus(t, api("GET", "/reports/dashboard"+window, nil, foreignSession), 401)
	expectStatus(t, api("GET", "/reports/dashboard"+window, nil, admin), 401)
	assertDashboard := func(net, returns string) {
		res := call("/reports/dashboard" + window)
		expectStatus(t, res, 200)
		var d reports.Dashboard
		must(json.Unmarshal(res.Body.Bytes(), &d))
		if d.Total.Net != net || d.Total.Gross != "1010000" || d.Total.Discount != "10000" || d.Total.Received != "1000000" ||
			d.Total.Pending != "120000" || d.Total.Returns != returns || d.Updated.IsZero() || len(d.Groups) != 7 {
			t.Fatalf("dashboard %s", res.Body.String())
		}
	}
	assertDashboard("1000000", "0")
	bad := call("/reports/sales/" + first + "?location=" + l2)
	expectStatus(t, bad, 404)
	for _, test := range []struct{ path, want string }{
		{"/reports/inventory?location=" + l1, `"bucket":"in_transit"`},
		{"/reports/movements" + window + "&location=" + l1, `"kind":"transfer"`},
		{"/reports/transfers" + window + "&location=" + l2, `"discrepancy":"2"`},
		{"/reports/sales" + window + "&location=" + l2 + "&format=csv", "pending_manual_confirmation"},
		{"/reports/sales/" + first + "?location=" + l1, `"name":"Produk"`},
	} {
		res := call(test.path)
		expectStatus(t, res, 200)
		if !strings.Contains(res.Body.String(), test.want) {
			t.Fatalf("%s %s", test.path, res.Body.String())
		}
		for _, v := range []string{"hpp", "margin", "supplier_cost", "valuation", "fingerprint", "3000000"} {
			if strings.Contains(res.Body.String(), v) {
				t.Fatal("disclosed field or tenant", v)
			}
		}
	}
	expectStatus(t, api("PUT", "/realtime/preferences", map[string]bool{"sale_notifications": true}, session), 204)
	initial, err := a.Realtime.Feed(ctx, tenant, session.Value, 0)
	must(err)
	if len(initial.Events) != 4 || initial.Cursor != "4" || !initial.Notifications {
		t.Fatalf("initial %+v", initial)
	}
	tx, err := db.Begin(ctx)
	must(err)
	reversal := id("SELECT gen_random_uuid()")
	_, err = tx.Exec(ctx, `INSERT INTO hive.sales_reversal_requests(tenant_id,id,sale_id,kind,requester_id,reason,payload)
	 VALUES($1,$2,$3,'return',$4,'fixture','{}')`, tenant, reversal, first, owner)
	must(err)
	_, err = tx.Exec(ctx, `INSERT INTO hive.sales_reversal_postings(tenant_id,id,approver_id,reason,refund) VALUES($1,$2,$3,'fixture',25000)`, tenant, reversal, owner)
	must(err)
	notYet, err := a.Realtime.Feed(ctx, tenant, session.Value, 4)
	must(err)
	if len(notYet.Events) != 0 {
		t.Fatal("uncommitted event visible")
	}
	must(tx.Commit(ctx))
	assertDashboard("975000", "25000")
	tx, err = db.Begin(ctx)
	must(err)
	_, err = tx.Exec(ctx, `INSERT INTO hive.sales_refund_confirmations(tenant_id,reversal_id,actor_id,reference) VALUES($1,$2,$3,'fixture')`, tenant, reversal, owner)
	must(err)
	must(tx.Rollback(ctx))
	srv := httptest.NewServer(h)
	defer srv.Close()
	socketURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/app/tenants/" + tenant + "/realtime/socket?after=4"
	wsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	header := http.Header{"Origin": {appOrigin}, "Cookie": {session.Name + "=" + session.Value}}
	wrong := header.Clone()
	wrong.Set("Origin", adminOrigin)
	_, res, err := websocket.Dial(wsCtx, socketURL, &websocket.DialOptions{HTTPHeader: wrong})
	if err == nil || res == nil || res.StatusCode != 403 {
		t.Fatal("cross-origin accepted", res, err)
	}
	for range 2 {
		conn, _, err := websocket.Dial(wsCtx, socketURL, &websocket.DialOptions{HTTPHeader: header})
		must(err)
		var f realtime.Feed
		must(wsjson.Read(wsCtx, conn, &f))
		must(conn.CloseNow())
		if f.Cursor != "5" || len(f.Events) != 1 || f.Events[0].Kind != "sale.reversed" || f.Events[0].Outlet != l1 {
			t.Fatalf("replayed feed %+v", f)
		}
	}
	exec(`UPDATE hive.tenant_entitlements SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND capability_id='reports.dashboard'`, tenant)
	expectStatus(t, call("/reports/dashboard"+window), 403)
}
