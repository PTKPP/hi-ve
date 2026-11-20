package transport

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/entitlements"
)

func TestEntitlementPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var tenant, other string
	for _, id := range []*string{&tenant, &other} {
		if err := db.QueryRow(ctx, `INSERT INTO hive.tenants(display_name,status) VALUES ('entitlement fixture','active') RETURNING id`).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	exec(`WITH principal AS (
		INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','entitlement-platform') RETURNING id
	), admin AS (INSERT INTO hive.platform_principals(principal_id) SELECT id FROM principal RETURNING principal_id)
	INSERT INTO hive.platform_sessions SELECT $1,principal_id,clock_timestamp()+interval '1 hour' FROM admin`, identity.Digest("entitlement-admin"))
	exec(`INSERT INTO hive.platform_permissions SELECT a.principal_id, p.permission
		FROM hive.platform_principals a JOIN hive.google_principals g ON g.id=a.principal_id
		CROSS JOIN (VALUES ('platform.entitlements.read'),('platform.entitlements.grant'),('platform.entitlements.revoke')) p(permission)
		WHERE g.subject='entitlement-platform'`)
	s := entitlements.Service{DB: db, FingerprintKey: bytes.Repeat([]byte{4}, 32)}
	h := API{Entitlements: s, AppOrigin: appOrigin, AdminOrigin: adminOrigin}.Handler()
	token := &http.Cookie{Name: platformCookie, Value: "entitlement-admin"}
	const capability = "catalog.products"
	path := "/api/v1/admin/tenants/" + tenant + "/entitlements/" + capability
	call := func(method, path, key string, input any) *httptest.ResponseRecorder {
		return accessRequest(h, method, path, adminOrigin, key, input, token)
	}
	now := time.Now().UTC()
	grant := func(revision int, effective, expiry time.Time) map[string]any {
		return map[string]any{
			"revision": revision, "effective_at": effective, "expires_at": expiry, "source_reference": "OPS-1",
		}
	}
	expectStatus(t, call("GET", path, "", nil), 404)
	expectStatus(t, accessRequest(h, "PUT", path, adminOrigin, "tenant-cannot-grant", grant(0, now, now.Add(time.Hour)),
		&http.Cookie{Name: tenantCookie, Value: token.Value}), 401)
	expectStatus(t, call("PUT", "/api/v1/admin/tenants/"+tenant+"/entitlements/platform.administration",
		"unlisted-capability", grant(0, now, now.Add(time.Hour))), 400)
	expectStatus(t, call("PUT", path, "invalid-time-window", grant(0, now, now)), 400)
	future := grant(0, now.Add(time.Hour), now.Add(2*time.Hour))
	const grantKey = "first-entitlement-key"
	results := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- call("PUT", path, grantKey, future) }()
	}
	a, b := <-results, <-results
	expectStatus(t, a, 200)
	expectStatus(t, b, 200)
	if a.Body.String() != b.Body.String() {
		t.Fatal("duplicate grant did not replay original result")
	}
	expectStatus(t, call("PUT", path, grantKey, grant(0, now, now.Add(time.Hour))), 409)
	if !errors.Is(s.Check(ctx, tenant, capability), entitlements.ErrDenied) {
		t.Fatal("scheduled grant allowed early access")
	}
	if !errors.Is(s.Check(ctx, other, capability), entitlements.ErrDenied) {
		t.Fatal("grant applied across tenants")
	}
	revoke := func(revision int) map[string]any {
		return map[string]any{"revision": revision, "reason_code": "administrative_revocation"}
	}
	expectStatus(t, call("POST", path+"/revoke", "stale-revoke-key", revoke(0)), 409)
	expectStatus(t, call("POST", path+"/revoke", "cancel-scheduled-grant", revoke(1)), 200)
	active := grant(2, now.Add(-time.Hour), now.Add(time.Hour))
	expectStatus(t, call("PUT", path, "grant-active-window", active), 200)

	consumer := func(tenant string, userAllowed, rollout bool) int {
		if !userAllowed || !rollout || s.Check(ctx, tenant, capability) != nil {
			return 403
		}
		return 200
	}
	if consumer(tenant, true, true) != 200 || consumer(tenant, false, true) != 403 ||
		consumer(tenant, true, false) != 403 || consumer(other, true, true) != 403 {
		t.Fatal("consumer did not require independent decisions")
	}
	// This disposable write holds only the target grant lock, giving revoke a defined ordering.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = s.CheckWrite(ctx, tx, tenant, capability); err != nil {
		t.Fatal(err)
	}
	go func() { results <- call("POST", path+"/revoke", "ordered-revocation", revoke(3)) }()
	waitForBlocked(t, db, tx.Conn().PgConn().PID())
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, <-results, 200)
	if consumer(tenant, true, true) != 403 {
		t.Fatal("revoke allowed new operation")
	}
	if replay := call("PUT", path, grantKey, future); replay.Body.String() != a.Body.String() {
		t.Fatal("old replay changed after revoke")
	}

	exec(`CREATE FUNCTION hive.test_entitlement_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'fixture failure'; END $$;
	CREATE TRIGGER test_entitlement_audit_failure BEFORE INSERT ON hive.entitlement_audit
	FOR EACH ROW EXECUTE FUNCTION hive.test_entitlement_audit_failure()`)
	expectStatus(t, call("PUT", path, "audit-failure-grant", grant(4, now.Add(-time.Hour), now.Add(time.Hour))), 503)
	var current, count, receipts int
	err = db.QueryRow(ctx, `SELECT (SELECT revision FROM hive.tenant_entitlements WHERE tenant_id=$1),
		(SELECT count(*) FROM hive.tenant_entitlement_history WHERE tenant_id=$1),
		(SELECT count(*) FROM hive.tenant_entitlement_receipts WHERE tenant_id=$1)`, tenant).Scan(&current, &count, &receipts)
	if err != nil || current != 4 || count != 4 || receipts != 4 {
		t.Fatalf("audit failure left partial state: %d/%d/%d: %v", current, count, receipts, err)
	}
	exec(`DROP TRIGGER test_entitlement_audit_failure ON hive.entitlement_audit`)
	expectStatus(t, call("PUT", path, "audit-failure-grant", grant(4, now.Add(-time.Hour), now.Add(time.Hour))), 200)
	exec(`UPDATE hive.tenant_entitlements SET expires_at=clock_timestamp() WHERE tenant_id=$1`, tenant)
	if !errors.Is(s.Check(ctx, tenant, capability), entitlements.ErrDenied) {
		t.Fatal("expiry left access open")
	}
}
