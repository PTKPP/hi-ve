package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"hi-ve/backend/internal/identity"
)

func TestLogoutPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	tenant, owner, primary := ownerSessionFixture(t, db, "logout-owner-a")
	other, _, otherCookie := ownerSessionFixture(t, db, "logout-owner-b")
	browserB := &http.Cookie{Name: tenantCookie, Value: "logout-browser-b"}
	expired := &http.Cookie{Name: tenantCookie, Value: "logout-expired"}
	exec(`INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at)
		VALUES ($1,$3,$4,clock_timestamp()+interval '1 hour'),($2,$3,$4,clock_timestamp()-interval '1 hour')`,
		identity.Digest(browserB.Value), identity.Digest(expired.Value), tenant, owner)
	exec(`INSERT INTO hive.platform_principals(principal_id) VALUES($1)`, owner)
	platform := &http.Cookie{Name: platformCookie, Value: "logout-platform"}
	exec(`INSERT INTO hive.platform_sessions(digest,principal_id,expires_at)
		VALUES ($1,$3,clock_timestamp()+interval '1 hour'),($2,$3,clock_timestamp()+interval '1 hour')`,
		identity.Digest(platform.Value), identity.Digest("logout-platform-browser-b"), owner)
	google, signed := signedProvider(t)
	handler := API{Identity: identity.Store{DB: db}, Google: google, AppOrigin: appOrigin, AdminOrigin: adminOrigin}.Handler()
	call := func(method, path, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(handler, method, path, origin, "", nil, cookies...)
	}
	base := "/api/v1/app/tenants/" + tenant
	otherBase := "/api/v1/app/tenants/" + other
	logout, platformLogout := base+"/logout", "/api/v1/admin/logout"
	expectLogout := func(res *httptest.ResponseRecorder, cookieName string) {
		t.Helper()
		expectStatus(t, res, 204)
		if res.Body.Len() != 0 {
			t.Fatal("logout returned a response body")
		}
		cookies := res.Result().Cookies()
		if cookieName == "" {
			if len(cookies) != 0 {
				t.Fatal("logout no-op changed a cookie")
			}
			return
		}
		if len(cookies) != 1 || cookies[0].Name != cookieName || cookies[0].MaxAge >= 0 ||
			cookies[0].Path != "/" || !cookies[0].Secure || !cookies[0].HttpOnly {
			t.Fatalf("logout did not expire only the matching %s cookie", cookieName)
		}
	}
	expectStatus(t, call("POST", logout, adminOrigin, primary), 403)
	expectLogout(call("POST", logout, appOrigin), "")
	expectLogout(call("POST", logout, appOrigin, expired), "")
	expectLogout(call("POST", otherBase+"/logout", appOrigin, primary), "")
	expectLogout(call("POST", logout, appOrigin, &http.Cookie{Name: tenantCookie, Value: platform.Value}), "")
	expectLogout(call("POST", platformLogout, adminOrigin, &http.Cookie{Name: platformCookie, Value: primary.Value}), "")
	expectStatus(t, call("GET", base+"/session", appOrigin, primary), 200)

	exec(`CREATE FUNCTION hive.test_logout_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'fixture failure'; END $$;
	CREATE TRIGGER test_logout_audit_failure BEFORE INSERT ON hive.session_logout_audit
	FOR EACH ROW EXECUTE FUNCTION hive.test_logout_audit_failure()`)
	failure := call("POST", logout, appOrigin, primary)
	expectStatus(t, failure, 503)
	if len(failure.Result().Cookies()) != 0 {
		t.Fatal("audit failure still cleared the cookie")
	}
	expectStatus(t, call("GET", base+"/session", appOrigin, primary), 200)
	exec(`DROP TRIGGER test_logout_audit_failure ON hive.session_logout_audit`)

	results := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- call("POST", logout, appOrigin, primary) }()
	}
	first, second := <-results, <-results
	if len(first.Result().Cookies()) == 0 {
		first, second = second, first
	}
	expectLogout(first, tenantCookie)
	expectLogout(second, "")
	expectStatus(t, call("GET", base+"/session", appOrigin, primary), 401)
	expectStatus(t, call("GET", base+"/session", appOrigin, browserB), 200)
	expectStatus(t, call("GET", otherBase+"/session", appOrigin, otherCookie), 200)
	expectLogout(call("POST", platformLogout, adminOrigin, platform), platformCookie)
	expectLogout(call("POST", platformLogout, adminOrigin, platform), "")
	expectStatus(t, call("GET", base+"/session", appOrigin, browserB), 200)

	// Hold logout after its DELETE, then authenticate through the normal OIDC callback.
	exec(`CREATE FUNCTION hive.test_hold_logout() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN PERFORM pg_advisory_xact_lock(8031502); RETURN NEW; END $$;
	CREATE TRIGGER test_hold_logout BEFORE INSERT ON hive.session_logout_audit
	FOR EACH ROW EXECUTE FUNCTION hive.test_hold_logout()`)
	start := call("POST", base+"/login", appOrigin, browserB)
	expectStatus(t, start, 200)
	var payload map[string]string
	if err := json.Unmarshal(start.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	auth, err := url.Parse(payload["authorization_url"])
	if err != nil {
		t.Fatal(err)
	}
	q := auth.Query()
	callback := "/api/v1/oidc/google/callback?" + url.Values{
		"state": {q.Get("state")}, "code": {signed("logout-owner-a", q.Get("nonce"), "")},
	}.Encode()
	blocker, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(8031502)`); err != nil {
		t.Fatal(err)
	}
	go func() { results <- call("POST", logout, appOrigin, browserB) }()
	logoutPID := waitForBlocked(t, db, blocker.Conn().PgConn().PID())
	login := make(chan *httptest.ResponseRecorder, 1)
	go func() { login <- call("GET", callback, "", start.Result().Cookies()[0], browserB) }()
	waitForBlocked(t, db, logoutPID)
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expectLogout(<-results, tenantCookie)
	newLogin := <-login
	expectStatus(t, newLogin, 204)
	newCookie := newLogin.Result().Cookies()[1]
	expectLogout(call("POST", logout, appOrigin, browserB), "")
	expectStatus(t, call("GET", base+"/session", appOrigin, browserB), 401)
	expectStatus(t, call("GET", base+"/session", appOrigin, newCookie), 200)
	exec(`DROP TRIGGER test_hold_logout ON hive.session_logout_audit`)

	var tenantEvents, platformEvents, otherPlatformSessions int
	err = db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM hive.session_logout_audit WHERE scope='tenant' AND tenant_id=$1 AND actor_id=$2),
		(SELECT count(*) FROM hive.session_logout_audit WHERE scope='platform' AND tenant_id IS NULL AND actor_id=$2),
		(SELECT count(*) FROM hive.platform_sessions WHERE digest=$3)`, tenant, owner, identity.Digest("logout-platform-browser-b")).
		Scan(&tenantEvents, &platformEvents, &otherPlatformSessions)
	if err != nil || tenantEvents != 2 || platformEvents != 1 || otherPlatformSessions != 1 {
		t.Fatalf("unexpected scoped logout effects %d/%d/%d: %v", tenantEvents, platformEvents, otherPlatformSessions, err)
	}
}
