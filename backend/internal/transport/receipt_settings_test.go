package transport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/settings"
)

func TestReceiptSettingsPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, _, owner := ownerSessionFixture(t, db, "receipt-owner")
	otherTenant, _, other := ownerSessionFixture(t, db, "receipt-other")
	service := settings.Service{DB: db, FingerprintKey: bytes.Repeat([]byte{4}, 32), AuthorizeOwner: identity.AuthorizeTenantOwner}
	h := API{Identity: identity.Store{DB: db}, Settings: service, AppOrigin: appOrigin}.Handler()
	base := "/api/v1/app/tenants/" + tenant
	path := base + "/settings/receipt"
	call := func(method, key string, body any, c *http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(h, method, path, appOrigin, key, body, c)
	}
	update := func(footer any, rev int) map[string]any {
		return map[string]any{"receipt_footer": footer, "expected_revision": rev}
	}
	read := func(w *httptest.ResponseRecorder) settings.Receipt {
		t.Helper()
		expectStatus(t, w, 200)
		var result settings.Receipt
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	empty := read(call("GET", "", nil, owner))
	if empty.Revision != 0 || empty.Footer != nil {
		t.Fatal("unexpected defaults")
	}
	expectStatus(t, call("GET", "", nil, nil), 401)
	expectStatus(t, call("GET", "", nil, other), 401)
	expectStatus(t, call("GET", "", nil, &http.Cookie{Name: operatorCookie, Value: owner.Value}), 401)
	for _, invalid := range []any{map[string]int{"expected_revision": 0}, update("", 0), update("   ", 0), update("x\ny", 0), update(strings.Repeat("é", 161), 0), update(42, 0), map[string]any{"receipt_footer": nil, "secret": "foo", "expected_revision": 0}} {
		expectStatus(t, call("PUT", "receipt-invalid-key", invalid, owner), 400)
	}
	const key = "receipt-set-initial"
	first := read(call("PUT", key, update("  Terima kasih  ", 0), owner))
	if first.Revision != 1 || first.Footer == nil || *first.Footer != "Terima kasih" {
		t.Fatal("normalization or initial revision failed")
	}
	expectStatus(t, call("PUT", key, update("Terima kasih", 0), owner), 200)
	expectStatus(t, call("PUT", key, update("Berbeda", 0), owner), 409)
	results := make(chan *httptest.ResponseRecorder, 2)
	go func() { results <- call("PUT", "receipt-race-one", update("A", 1), owner) }()
	go func() { results <- call("PUT", "receipt-race-two", update("B", 1), owner) }()
	a, b := <-results, <-results
	if a.Code == b.Code || a.Code+b.Code != 609 {
		t.Fatalf("revision race: %d %d", a.Code, b.Code)
	}
	exec := func(s string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, s, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE FUNCTION hive.test_receipt_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$;
 CREATE TRIGGER test_receipt_audit_fail BEFORE INSERT ON hive.receipt_settings_audit FOR EACH ROW EXECUTE FUNCTION hive.test_receipt_audit_fail()`)
	const clear = "receipt-clear-value"
	expectStatus(t, call("PUT", clear, update(nil, 2), owner), 503)
	previous := read(call("GET", "", nil, owner))
	if previous.Revision != 2 || previous.Footer == nil {
		t.Fatal("partial write")
	}
	exec(`DROP TRIGGER test_receipt_audit_fail ON hive.receipt_settings_audit`)
	cleared := read(call("PUT", clear, update(nil, 2), owner))
	if cleared.Revision != 3 || cleared.Footer != nil {
		t.Fatal("clear did not advance revision")
	}
	otherPath := "/api/v1/app/tenants/" + otherTenant + "/settings/receipt"
	if read(accessRequest(h, "GET", otherPath, appOrigin, "", nil, other)).Revision != 0 {
		t.Fatal("cross-tenant mutation")
	}
	var logs int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM hive.receipt_settings_audit WHERE tenant_id=$1 AND field_name='receipt_footer'`, tenant).Scan(&logs); err != nil || logs != 3 {
		t.Fatalf("receipt audit count %d, %v", logs, err)
	}
	expectStatus(t, accessRequest(h, "POST", base+"/logout", appOrigin, "", nil, owner), 204)
	expectStatus(t, call("PUT", clear, update(nil, 2), owner), 401)
}
