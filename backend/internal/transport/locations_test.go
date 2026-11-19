package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
)

func TestLocationsPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	tenant, owner, admin := ownerSessionFixture(t, db, "locations-owner-a")
	other, _, otherAdmin := ownerSessionFixture(t, db, "locations-owner-b")
	exec(`WITH principal AS (
		INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','locations-staff') RETURNING id
	), member AS (
		INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) SELECT $1,id,'staff' FROM principal
		RETURNING tenant_id,principal_id
	) INSERT INTO hive.tenant_sessions SELECT $2,tenant_id,principal_id,clock_timestamp()+interval '1 hour' FROM member`, tenant, identity.Digest("locations-staff"))
	exec(`INSERT INTO hive.platform_principals(principal_id) VALUES ($1)`, owner)
	exec(`INSERT INTO hive.platform_sessions VALUES ($1,$2,clock_timestamp()+interval '1 hour')`, identity.Digest("locations-platform"), owner)
	service := locations.Service{DB: db, FingerprintKey: bytes.Repeat([]byte{6}, 32), AuthorizeOwner: identity.AuthorizeTenantOwner}
	handler := API{Locations: service, AppOrigin: appOrigin, AdminOrigin: adminOrigin}.Handler()
	path := func(id string) string { return "/api/v1/app/tenants/" + id + "/locations" }
	call := func(method, tenant, key string, body any, c *http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(handler, method, path(tenant), appOrigin, key, body, c)
	}
	input := func(code, name, kind string) locations.CreateInput {
		return locations.CreateInput{Code: code, Name: name, Type: kind}
	}
	outlet := input(" Outlet-A ", "North", "outlet")
	expectStatus(t, call("GET", tenant, "", nil, nil), 401)
	expectStatus(t, call("GET", other, "", nil, admin), 401)
	expectStatus(t, call("GET", tenant, "", nil, &http.Cookie{Name: tenantCookie, Value: "locations-platform"}), 401)
	memberCookie := &http.Cookie{Name: tenantCookie, Value: "locations-staff"}
	expectStatus(t, call("GET", tenant, "", nil, memberCookie), 403)
	expectStatus(t, call("POST", tenant, "locations-staff-key", outlet, memberCookie), 403)
	for _, invalid := range []locations.CreateInput{input("bad code", "North", "outlet"), input("valid", "North", "office"), input("valid", " ", "warehouse")} {
		expectStatus(t, call("POST", tenant, "invalid-location-key", invalid, admin), 400)
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	const replayKey = "locations-idempotency"
	go func() { results <- call("POST", tenant, replayKey, outlet, admin) }()
	go func() { results <- call("POST", tenant, replayKey, input("outlet-a", " North ", "outlet"), admin) }()
	first, second := <-results, <-results
	expectStatus(t, first, 201)
	expectStatus(t, second, 201)
	if first.Body.String() != second.Body.String() {
		t.Fatal("normalized replay returned a different record")
	}
	decodeLocation := func(w *httptest.ResponseRecorder) locations.Location {
		t.Helper()
		var row locations.Location
		if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	created := decodeLocation(first)
	if created.Code != "outlet-a" || created.Type != "outlet" || created.Status != "active" {
		t.Fatal("incorrect outlet record")
	}
	expectStatus(t, call("POST", tenant, replayKey, input("outlet-a", "Conflicting", "outlet"), admin), 409)
	expectStatus(t, call("POST", tenant, "locations-duplicate-code", input("OUTLET-A", "North", "warehouse"), admin), 409)
	foreign := call("POST", other, replayKey, outlet, otherAdmin)
	expectStatus(t, foreign, 201)
	if decodeLocation(foreign).ID == created.ID {
		t.Fatal("different tenant reused a location ID")
	}

	warehouse := call("POST", tenant, "locations-warehouse-key", input("central", "Central", "warehouse"), admin)
	expectStatus(t, warehouse, 201)
	warehouseID := decodeLocation(warehouse).ID
	loaded, err := service.ResolveActiveOutlet(ctx, tenant, created.ID)
	if err != nil || loaded != created {
		t.Fatalf("outlet resolution drift: %v", err)
	}
	for _, target := range []struct{ tenant, id string }{{tenant, warehouseID}, {other, created.ID}, {tenant, decodeLocation(foreign).ID}} {
		if _, err = service.ResolveActiveOutlet(ctx, target.tenant, target.id); !errors.Is(err, locations.ErrOutletUnavailable) {
			t.Fatalf("invalid outlet became available: %v", err)
		}
	}
	list := call("GET", tenant, "", nil, admin)
	expectStatus(t, list, 200)
	var output struct {
		Locations []locations.Location `json:"locations"`
	}
	if err = json.Unmarshal(list.Body.Bytes(), &output); err != nil || len(output.Locations) != 2 || output.Locations[0].ID != warehouseID ||
		output.Locations[1].ID != created.ID {
		t.Fatalf("list ordering or scope failed: %v", err)
	}

	exec(`CREATE FUNCTION hive.test_location_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'fixture failure'; END $$;
	CREATE TRIGGER test_location_audit_failure BEFORE INSERT ON hive.location_create_audit
	FOR EACH ROW EXECUTE FUNCTION hive.test_location_audit_failure()`)
	const retry = "locations-audit-retry"
	retryLocation := input("retry-outlet", "Retry", "outlet")
	expectStatus(t, call("POST", tenant, retry, retryLocation, admin), 503)
	var records, receipts, audits int
	err = db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM hive.locations WHERE tenant_id=$1),
		(SELECT count(*) FROM hive.location_create_receipts WHERE tenant_id=$1),
		(SELECT count(*) FROM hive.location_create_audit WHERE tenant_id=$1)`, tenant).Scan(&records, &receipts, &audits)
	if err != nil || records != 2 || receipts != 2 || audits != 2 {
		t.Fatalf("location audit failure left partial writes: %d %d %d: %v", records, receipts, audits, err)
	}
	exec(`DROP TRIGGER test_location_audit_failure ON hive.location_create_audit`)
	expectStatus(t, call("POST", tenant, retry, retryLocation, admin), 201)
	for _, key := range []string{"location-race-key-first", "location-race-key-second"} {
		go func() { results <- call("POST", tenant, key, input("different-key-race", "Race", "outlet"), admin) }()
	}
	first, second = <-results, <-results
	if first.Code == second.Code || first.Code+second.Code != 201+409 {
		t.Fatalf("concurrent duplicate codes: %d/%d", first.Code, second.Code)
	}

	exec(`UPDATE hive.locations SET status='inactive' WHERE tenant_id=$1 AND id=$2`, tenant, created.ID)
	if _, err = service.ResolveActiveOutlet(ctx, tenant, created.ID); !errors.Is(err, locations.ErrOutletUnavailable) {
		t.Fatal("inactive outlet resolved")
	}
	expectStatus(t, call("POST", tenant, "locations-inactive-code", outlet, admin), 409)
	exec(`UPDATE hive.tenants SET status='suspended' WHERE id=$1`, tenant)
	expectStatus(t, call("GET", tenant, "", nil, admin), 401)
}
