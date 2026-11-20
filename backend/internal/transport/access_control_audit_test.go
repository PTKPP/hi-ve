package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
)

func TestAccessControlAuditPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, ownerID, owner := ownerSessionFixture(t, db, "access-audit-owner")
	otherTenant, otherID, other := ownerSessionFixture(t, db, "access-audit-other")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	id := func(q string, args ...any) string {
		t.Helper()
		var value string
		if err := db.QueryRow(ctx, q, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	const roleName = "hive_access_audit_test_runtime"
	const password = "audit_fixture_only"
	exec(`CREATE ROLE hive_access_audit_test_runtime LOGIN PASSWORD 'audit_fixture_only' NOSUPERUSER NOCREATEDB NOCREATEROLE;
		GRANT USAGE ON SCHEMA hive TO hive_access_audit_test_runtime;
		GRANT SELECT ON hive.active_tenants,hive.tenant_sessions,hive.tenant_memberships,hive.locations TO hive_access_audit_test_runtime;
		GRANT UPDATE (expires_at) ON hive.tenant_sessions TO hive_access_audit_test_runtime;
		GRANT UPDATE (active) ON hive.tenant_memberships TO hive_access_audit_test_runtime;
		GRANT SELECT, INSERT ON hive.tenant_rbac_audit TO hive_access_audit_test_runtime`)
	config := db.Config()
	config.ConnConfig.User = roleName
	config.ConnConfig.Password = password
	runtime, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	if err = runtime.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	makeLocation := func(tenant, code string) string {
		return id(`INSERT INTO hive.locations(tenant_id,code,kind,name) VALUES ($1,$2,'outlet',$2) RETURNING id`, tenant, code)
	}
	first, unchanged, third, foreign := makeLocation(tenant, "access-first"), makeLocation(tenant, "access-unchanged"),
		makeLocation(tenant, "access-third"), makeLocation(otherTenant, "access-foreign")
	role := id("SELECT gen_random_uuid()")
	target := id("SELECT gen_random_uuid()")
	states := func(ids ...string) []map[string]string {
		items := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			items = append(items, map[string]string{"role_id": role, "location_id": id, "restricted_fixture": "must-not-leak"})
		}
		return items
	}
	const action = "membership.location_roles.replace"
	record := func(tenant, actor, kind string, rev int64, before, after any) {
		t.Helper()
		err := pgx.BeginFunc(ctx, runtime, func(tx pgx.Tx) error {
			return audit.RecordRBAC(ctx, tx, audit.RBACEvent{Tenant: tenant, Actor: actor, Target: target, Action: kind, Revision: rev, Before: before, After: after})
		})
		if err != nil {
			t.Fatalf("runtime producer INSERT failed: %v", err)
		}
	}
	record(otherTenant, otherID, action, 1, states(), states(foreign))
	record(tenant, ownerID, action, 1, states(), states(first))
	record(tenant, ownerID, action, 2, states(first, unchanged, foreign), states(third, unchanged))
	record(tenant, ownerID, "role.create", 3, nil, states(unchanged))
	exec(`UPDATE hive.locations SET status='inactive' WHERE tenant_id=$1 AND id=$2`, tenant, first)

	for _, query := range []string{
		`UPDATE hive.tenant_rbac_audit SET action=action WHERE tenant_id=$1`,
		`DELETE FROM hive.tenant_rbac_audit WHERE tenant_id=$1`,
	} {
		_, err := runtime.Exec(ctx, query, tenant)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("runtime unexpectedly allowed audit mutation: %v", err)
		}
	}

	const origin = "https://app.example.test"
	reader := audit.AccessControlReader{DB: runtime, CursorKey: bytes.Repeat([]byte{7}, 32), AuthorizeOwner: identity.AuthorizeTenantOwner, TenantLocationIDs: locations.TenantLocationIDs}
	h := API{AccessControlAudit: reader, AppOrigin: origin}.Handler()
	path := func(tenant string) string { return "/api/v1/app/tenants/" + tenant + "/audit/sensitive-events" }
	read := func(path string, cookie *http.Cookie, want int) audit.AccessControlPage {
		t.Helper()
		res := accessRequest(h, "GET", path, origin, "", nil, cookie)
		expectStatus(t, res, want)
		var page audit.AccessControlPage
		if want != 200 {
			return page
		}
		if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(res.Body.Bytes(), []byte("must-not-leak")) || bytes.Contains(res.Body.Bytes(), []byte("before_state")) ||
			bytes.Contains(res.Body.Bytes(), []byte("after_state")) || bytes.Contains(res.Body.Bytes(), []byte(role)) {
			t.Fatal("audit projection exposed internal state")
		}
		return page
	}
	page := read(path(tenant)+"?limit=1&category=access_control", owner, 200)
	if len(page.Events) != 1 || page.NextCursor == "" {
		t.Fatal("missing page or cursor")
	}
	e := page.Events[0]
	if e.ID == "" || e.Actor != ownerID || e.Target != target || e.Action != action || e.Revision != 2 || e.OccurredAt.IsZero() ||
		len(e.Locations) != 2 || !((e.Locations[0] == first && e.Locations[1] == third) || (e.Locations[0] == third && e.Locations[1] == first)) {
		t.Fatalf("unexpected audit projection %+v", e)
	}
	query := "?limit=1&cursor=" + url.QueryEscape(page.NextCursor)
	read(path(otherTenant)+query, other, 400)
	read(path(tenant)+query, other, 401)
	read(path(tenant)+"?category=cost", owner, 400)
	read(path(tenant)+"?cursor="+url.QueryEscape("9"+page.NextCursor), owner, 400)
	read(path(tenant)+"?limit=1&limit=2", owner, 400)
	const platformToken = "access-audit-platform"
	exec(`WITH principal AS (
        INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','access-audit-platform') RETURNING id
    ), admin AS (
        INSERT INTO hive.platform_principals(principal_id) SELECT id FROM principal RETURNING principal_id
    ) INSERT INTO hive.platform_sessions(digest,principal_id,expires_at)
      SELECT $1,principal_id,clock_timestamp()+interval '10 minutes' FROM admin`, identity.Digest(platformToken))
	for _, c := range []*http.Cookie{{Name: platformCookie, Value: platformToken}, {Name: operatorCookie, Value: owner.Value}} {
		read(path(tenant)+query, c, 401)
	}
	staff := id(`INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','access-audit-staff') RETURNING id`)
	exec(`INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) VALUES($1,$2,'staff')`, tenant, staff)
	for _, fixture := range []struct {
		token, kind, principal string
		code                   int
	}{
		{"access-audit-staff-google", "google", staff, 401}, {"access-audit-staff-pin", "device_pin", staff, 403},
		{"access-audit-owner-pin", "device_pin", ownerID, 401},
	} {
		exec(`INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at,auth_method)
			VALUES($1,$2,$3,clock_timestamp()+interval '10 minutes',$4)`, identity.Digest(fixture.token), tenant, fixture.principal, fixture.kind)
		read(path(tenant)+query, &http.Cookie{Name: tenantCookie, Value: fixture.token}, fixture.code)
	}
	// A concurrent append must not pull a higher revision into an existing page.
	record(tenant, ownerID, action, 4, states(), states(first))
	for range 2 {
		next := read(path(tenant)+query, owner, 200)
		if len(next.Events) != 1 || next.Events[0].Revision != 1 || next.NextCursor != "" {
			t.Fatal("unstable keyset or replay")
		}
	}
	expectStatus(t, accessRequest(h, "DELETE", path(tenant), origin, "", nil, owner), 405)
	expectStatus(t, accessRequest(h, "PUT", path(tenant), origin, "", nil, owner), 405)

	// If a concurrent revocation commits first, the authorization row lock
	// must recheck its predicate and reject even an otherwise valid cursor.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "DELETE FROM hive.tenant_sessions WHERE digest=$1", identity.Digest(owner.Value)); err != nil {
		t.Fatal(err)
	}
	pending := make(chan *httptest.ResponseRecorder, 1)
	go func() { pending <- accessRequest(h, "GET", path(tenant)+query, origin, "", nil, owner) }()
	waitForBlocked(t, db, tx.Conn().PgConn().PID())
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, <-pending, 401)
	read(path(tenant)+query, owner, 401)

	// A fresh Google session with a no-longer-active membership is also denied.
	exec(`INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at)
		VALUES($1,$2,$3,clock_timestamp()+interval '10 minutes')`, identity.Digest(owner.Value), tenant, ownerID)
	exec(`UPDATE hive.tenant_memberships SET active=false WHERE tenant_id=$1 AND principal_id=$2`, tenant, ownerID)
	read(path(tenant)+query, owner, 401)
}
