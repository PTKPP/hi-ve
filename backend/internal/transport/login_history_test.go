package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
)

func TestLoginHistoryPostgres(t *testing.T) {
	db := disposableDatabase(t)
	runtimeDB := businessRuntime(t, db)
	ctx := t.Context()
	tenant, owner, old := ownerSessionFixture(t, db, "history-owner")
	other, otherOwner, foreign := ownerSessionFixture(t, db, "history-other")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	readCount := func(q string, args ...any) int {
		t.Helper()
		var count int
		if err := db.QueryRow(ctx, q, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	const tenantCount = `SELECT count(*) FROM hive.online_login_history WHERE tenant_id=$1`
	sessionCount := func() int {
		return readCount(`SELECT count(*) FROM hive.tenant_sessions WHERE tenant_id=$1 AND principal_id=$2`, tenant, owner)
	}
	exec(`WITH p AS (
		INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','history-staff') RETURNING id
	), m AS (
		INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) SELECT $1,id,'staff' FROM p RETURNING tenant_id,principal_id
	) INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at,auth_method)
	SELECT $2::bytea,tenant_id,principal_id,clock_timestamp()+interval '1 hour','device_pin' FROM m
	UNION ALL SELECT $3::bytea,tenant_id,principal_id,clock_timestamp()+interval '1 hour','google' FROM m`,
		tenant, identity.Digest("history-staff-fixture"), identity.Digest("history-legacy-staff"))
	exec(`INSERT INTO hive.platform_principals(principal_id) VALUES ($1)`, owner)
	exec(`INSERT INTO hive.platform_sessions(digest,principal_id,expires_at)
		VALUES ($1,$2,clock_timestamp()+interval '1 hour')`, identity.Digest("history-platform"), owner)
	platform := &http.Cookie{Name: platformCookie, Value: "history-platform"}
	staff := &http.Cookie{Name: tenantCookie, Value: "history-staff-fixture"}
	legacy := &http.Cookie{Name: tenantCookie, Value: "history-legacy-staff"}

	google, signed := signedProvider(t)
	history := audit.LoginHistory{DB: runtimeDB, CursorKey: bytes.Repeat([]byte{2}, 32), AuthorizeOwner: identity.AuthorizeTenantOwner}
	h := API{LoginHistory: history, Google: google, Identity: identity.Store{DB: runtimeDB}, TenantRBAC: identity.RBAC{DB: runtimeDB},
		AdminOrigin: adminOrigin, AppOrigin: appOrigin}.Handler()
	based := func(id string) string { return "/api/v1/app/tenants/" + id }
	call := func(method, path, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(h, method, path, origin, "", nil, cookies...)
	}
	get := func(tenant, query string, c *http.Cookie) *httptest.ResponseRecorder {
		return call("GET", based(tenant)+"/login-history"+query, appOrigin, c)
	}
	ensureNoTenantCookie := func(w *httptest.ResponseRecorder) {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name == tenantCookie {
				t.Fatal("denied callback changed a tenant session cookie")
			}
		}
	}
	flow := func(tenant, subject string, invalid bool) (path string, browser *http.Cookie, event string) {
		t.Helper()
		start := call("POST", based(tenant)+"/login", appOrigin)
		expectStatus(t, start, 200)
		var body map[string]string
		if err := json.Unmarshal(start.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(body["authorization_url"])
		if err != nil {
			t.Fatal(err)
		}
		state, nonce := u.Query().Get("state"), u.Query().Get("nonce")
		code := signed(subject, nonce, "")
		if invalid {
			code = "invalid-proof"
		}
		if err = db.QueryRow(ctx, `SELECT event_id FROM hive.oidc_flows WHERE state_digest=$1`,
			identity.Digest(state)).Scan(&event); err != nil || event == "" {
			t.Fatalf("failed to load stable flow ID: %v", err)
		}
		return "/api/v1/oidc/google/callback?" + url.Values{"state": {state}, "code": {code}}.Encode(), start.Result().Cookies()[0], event
	}
	page := func(res *httptest.ResponseRecorder) audit.LoginPage {
		t.Helper()
		expectStatus(t, res, 200)
		var result audit.LoginPage
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || result.Events == nil {
			t.Fatalf("invalid login history page: %v", err)
		}
		return result
	}
	expectStatus(t, get(tenant, "", nil), 401)
	expectStatus(t, get(tenant, "", platform), 401)
	expectStatus(t, get(tenant, "", foreign), 401)
	expectStatus(t, get(tenant, "", staff), 403)
	expectStatus(t, get(tenant, "", legacy), 401)
	expectStatus(t, call("GET", based(tenant)+"/session", appOrigin, legacy), 401)
	expectStatus(t, call("GET", based(tenant)+"/roles", appOrigin, legacy), 401)
	for _, q := range []string{"?limit=101", "?limit=0", "?limit=1&limit=2", "?outcome=lockout", "?actor_id=invalid",
		"?from=invalid", "?to=2026-01-01T00:00:00Z&from=2026-01-02T00:00:00Z", "?cursor=forged"} {
		expectStatus(t, get(tenant, q, old), 400)
	}
	for _, f := range []struct {
		subject string
		invalid bool
		status  int
	}{{"history-owner", true, 401}, {"history-staff", false, 403}, {"history-other", false, 403}, {"history-unknown", false, 403}} {
		path, browser, _ := flow(tenant, f.subject, f.invalid)
		w := call("GET", path, "", browser, old)
		expectStatus(t, w, f.status)
		ensureNoTenantCookie(w)
	}
	expiredPath, expiredCookie, expiredID := flow(tenant, "history-owner", false)
	exec(`UPDATE hive.oidc_flows SET expires_at=clock_timestamp() WHERE event_id=$1`, expiredID)
	expectStatus(t, call("GET", expiredPath, "", expiredCookie, old), 401)
	if readCount(tenantCount, tenant) != 0 || sessionCount() != 1 {
		t.Fatal("unattributable login created history or a session")
	}

	exec(`CREATE FUNCTION hive.test_login_history_fail() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'fixture error must remain private'; END $$;
	CREATE TRIGGER test_login_history_fail BEFORE INSERT ON hive.online_login_history
	FOR EACH ROW EXECUTE FUNCTION hive.test_login_history_fail()`)
	path, browser, _ := flow(tenant, "history-owner", false)
	failure := call("GET", path, "", browser, old)
	expectStatus(t, failure, 503)
	ensureNoTenantCookie(failure)
	expectStatus(t, call("GET", based(tenant)+"/session", appOrigin, old), 200)
	if sessionCount() != 1 || readCount(tenantCount, tenant) != 0 {
		t.Fatal("audit failure did not roll back session creation/rotation")
	}
	exec(`UPDATE hive.tenant_memberships SET active=false WHERE tenant_id=$1 AND principal_id=$2`, tenant, owner)
	path, browser, _ = flow(tenant, "history-owner", false)
	var log bytes.Buffer
	oldLogger := slog.Default()
	func() {
		slog.SetDefault(slog.New(slog.NewJSONHandler(&log, nil)))
		defer slog.SetDefault(oldLogger)
		denied := call("GET", path, "", browser, old)
		expectStatus(t, denied, 403)
		ensureNoTenantCookie(denied)
	}()
	if !strings.Contains(log.String(), "denied tenant Google login event persistence failed") ||
		strings.Contains(log.String(), "fixture error") || strings.Contains(log.String(), tenant) {
		t.Fatal("expected sanitized denial-persistence signal")
	}
	exec(`DROP TRIGGER test_login_history_fail ON hive.online_login_history`)
	path, browser, deniedID := flow(tenant, "history-owner", false)
	expectStatus(t, call("GET", path, "", browser, old), 403)
	expectStatus(t, get(tenant, "", old), 401)
	exec(`UPDATE hive.tenant_memberships SET active=true WHERE tenant_id=$1 AND principal_id=$2`, tenant, owner)
	path, browser, eventID := flow(tenant, "history-owner", false)
	results := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- call("GET", path, "", browser, old) }()
	}
	first, second := <-results, <-results
	if first.Code != 204 {
		first, second = second, first
	}
	expectStatus(t, first, 204)
	expectStatus(t, second, 401)
	ensureNoTenantCookie(second)
	newSession := first.Result().Cookies()[1]
	expectStatus(t, get(tenant, "", old), 401)
	expectStatus(t, call("GET", based(tenant)+"/session", appOrigin, newSession), 200)
	if sessionCount() != 1 || readCount(tenantCount, tenant) != 2 {
		t.Fatal("concurrent callback created duplicate session or history")
	}
	firstPage := page(get(tenant, "?limit=1", newSession))
	if len(firstPage.Events) != 1 || firstPage.NextCursor == "" || firstPage.Events[0].ID != eventID ||
		firstPage.Events[0].Outcome != "success" || firstPage.Events[0].Actor != owner || firstPage.Events[0].Class != "tenant_owner_google" {
		t.Fatal("login event missing stable ID, actor, class or outcome")
	}
	cursor := "?cursor=" + url.QueryEscape(firstPage.NextCursor)
	next := page(get(tenant, "?limit=1&"+cursor[1:], newSession))
	if len(next.Events) != 1 || next.Events[0].ID != deniedID || next.Events[0].Outcome != "denied" || next.NextCursor != "" {
		t.Fatal("pagination or inactive Owner attribution failed")
	}
	expectStatus(t, get(other, cursor, foreign), 400)
	expectStatus(t, get(tenant, cursor+"x", newSession), 400)
	if len(page(get(other, "", foreign)).Events) != 0 || len(page(get(tenant, "?actor_id="+otherOwner, newSession)).Events) != 0 ||
		len(page(get(tenant, "?outcome=denied&actor_id="+owner, newSession)).Events) != 1 ||
		len(page(get(tenant, "?from="+url.QueryEscape(time.Now().Add(time.Hour).Format(time.RFC3339)), newSession)).Events) != 0 {
		t.Fatal("event filter escaped tenant or time scope")
	}
	if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		inserted, err := audit.RecordOwnerLogin(ctx, tx, eventID, tenant, owner, "success")
		if err != nil || inserted {
			return errors.New("exact replay did not deduplicate")
		}
		_, err = audit.RecordOwnerLogin(ctx, tx, eventID, tenant, owner, "denied")
		if err == nil {
			return errors.New("accepted conflicting replay")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	exec(`UPDATE hive.online_login_history SET received_at=statement_timestamp()-interval '168 hours' WHERE event_id=$1`, deniedID)
	exec(`UPDATE hive.online_login_history SET received_at=statement_timestamp()-interval '168 hours'+interval '5 seconds' WHERE event_id=$1`, eventID)
	if kept := page(get(tenant, "", newSession)); len(kept.Events) != 1 || kept.Events[0].ID != eventID {
		t.Fatal("168-hour visibility boundary failed")
	}
	exec(`INSERT INTO hive.online_login_history(event_id,tenant_id,actor_id,flow_class,outcome,received_at)
		SELECT gen_random_uuid(),$1,$2,'tenant_owner_google','success',statement_timestamp()-interval '169 hours'
		FROM generate_series(1,500)`, tenant, owner)
	exec(`INSERT INTO hive.session_logout_audit(scope,tenant_id,actor_id,occurred_at)
		VALUES ('tenant',$1,$2,clock_timestamp()-interval '168 hours')`, tenant, owner)
	if backlog, err := history.Cleanup(ctx); err != nil || !backlog {
		t.Fatalf("cleanup did not limit batch or signal backlog: %v", err)
	}
	if got := readCount(tenantCount, tenant); got != 2 {
		t.Fatalf("first cleanup batch removed %d events, want 500", 502-got)
	}
	if backlog, err := history.Cleanup(ctx); err != nil || backlog {
		t.Fatalf("bounded cleanup retry failed: %v", err)
	}
	if readCount(tenantCount, tenant) != 1 ||
		readCount(`SELECT count(*) FROM hive.session_logout_audit WHERE tenant_id=$1`, tenant) != 1 {
		t.Fatal("cleanup lost an unexpired login event or critical audit history")
	}

	// Concurrent Audit callers share the same durable event receipt.
	var duplicate string
	if err := db.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&duplicate); err != nil {
		t.Fatal(err)
	}
	type appendResult struct {
		insert bool
		err    error
	}
	appends := make(chan appendResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			var result appendResult
			result.err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
				inserted, err := audit.RecordOwnerLogin(ctx, tx, duplicate, tenant, owner, "success")
				result.insert = inserted
				return err
			})
			appends <- result
		}()
	}
	a, b := <-appends, <-appends
	if a.err != nil || b.err != nil || a.insert == b.insert || readCount(tenantCount, tenant) != 2 {
		t.Fatalf("concurrent history append failed to deduplicate: %v/%v", a.err, b.err)
	}

}
