package transport

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
)

func TestLocationGrantsPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, _, owner := ownerSessionFixture(t, db, "scoped-grant-owner")
	_, _, foreign := ownerSessionFixture(t, db, "scoped-grant-other")
	var member, role, outlet, warehouse string
	for _, seed := range []struct {
		q   string
		out *string
	}{
		{`INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','scoped-grant-staff') RETURNING id`, &member},
		{`INSERT INTO hive.tenant_roles(tenant_id,role_key,name) VALUES ($1,'scoped','Scoped') RETURNING id`, &role},
		{`INSERT INTO hive.locations(tenant_id,code,kind,name) VALUES ($1,'outlet','outlet','Outlet') RETURNING id`, &outlet},
		{`INSERT INTO hive.locations(tenant_id,code,kind,name) VALUES ($1,'warehouse','warehouse','Warehouse') RETURNING id`, &warehouse},
	} {
		args := []any{tenant}
		if seed.out == &member {
			args = nil
		}
		if err := db.QueryRow(ctx, seed.q, args...).Scan(seed.out); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) VALUES($1,$2,'staff')`, tenant, member)
	key := bytes.Repeat([]byte{3}, 32)
	svc := identity.RBAC{DB: db, FingerprintKey: key, LockOutlets: locations.LockActiveOutlets}
	h := API{TenantRBAC: svc, Memberships: identity.MembershipLifecycle{DB: db, FingerprintKey: key}, AppOrigin: appOrigin}.Handler()
	base := "/api/v1/app/tenants/" + tenant
	actionPath := base + "/roles/" + role + "/location-actions"
	assignPath := base + "/members/" + member + "/location-role-assignments"
	request := func(method, path, key string, body any, c *http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(h, method, path, appOrigin, key, body, c)
	}
	action := func(revision int, actions ...string) map[string]any {
		if actions == nil {
			actions = []string{}
		}
		return map[string]any{"expected_revision": revision, "actions": actions}
	}
	assign := func(revision int, id string) map[string]any {
		return map[string]any{"expected_revision": revision, "assignments": []identity.LocationAssignment{{Role: role, Location: id}}}
	}
	expectStatus(t, request("GET", actionPath, "", nil, owner), 200)
	expectStatus(t, request("GET", actionPath, "", nil, foreign), 401)
	expectStatus(t, request("PUT", actionPath, "scoped-bad-action", action(0, "*"), owner), 400)
	expectStatus(t, request("PUT", actionPath, "scoped-first-action", action(0, identity.DeviceOperatorUse), owner), 200)
	expectStatus(t, request("PUT", assignPath, "scoped-warehouse", assign(1, warehouse), owner), 404)
	expectStatus(t, request("PUT", assignPath, "scoped-outlet-key", assign(1, outlet), owner), 200)
	expectStatus(t, request("PUT", assignPath, "scoped-outlet-key", assign(1, outlet), owner), 200)
	forbid := func(id string) error {
		return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			return identity.AuthorizeLocationGrant(ctx, tx, tenant, member, id, identity.DeviceOperatorUse)
		})
	}
	if err := forbid(outlet); err != nil {
		t.Fatal(err)
	}
	if err := forbid(warehouse); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("wrong location: %v", err)
	}
	exec(`CREATE FUNCTION hive.test_scoped_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$;
 CREATE TRIGGER test_scoped_audit_fail BEFORE INSERT ON hive.tenant_rbac_audit FOR EACH ROW EXECUTE FUNCTION hive.test_scoped_audit_fail()`)
	expectStatus(t, request("PUT", actionPath, "scoped-revoke-key", action(2), owner), 503)
	exec(`DROP TRIGGER test_scoped_audit_fail ON hive.tenant_rbac_audit`)
	if err := forbid(outlet); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = identity.AuthorizeLocationGrant(ctx, tx, tenant, member, outlet, identity.DeviceOperatorUse); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request("PUT", actionPath, "scoped-revoke-key", action(2), owner) }()
	waitForBlocked(t, db, tx.Conn().PgConn().PID())
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, <-done, 200)
	if err := forbid(outlet); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("revoked grant: %v", err)
	}
	expectStatus(t, request("PUT", actionPath, "scoped-stale-action", action(2, identity.DeviceOperatorUse), owner), 409)
	expectStatus(t, request("POST", base+"/members/"+member+"/deactivate", "scoped-deactivate", map[string]any{"revision": 0, "reason_code": "administrative_action"}, owner), 200)
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM hive.tenant_location_role_assignments WHERE tenant_id=$1`, tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("scoped grants survived deactivate: %v %d", err, count)
	}
}
