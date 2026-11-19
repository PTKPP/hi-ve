package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"hi-ve/backend/internal/identity"
)

func TestTenantRBACPostgres(t *testing.T) {
	db := disposableDatabase(t)
	run := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	type tenantFixture struct{ tenant, owner, ownerToken string }
	newTenant := func(label string) tenantFixture {
		t.Helper()
		var f tenantFixture
		f.ownerToken = "rbac-owner-" + label
		err := db.QueryRow(t.Context(), `WITH tenant AS (
			INSERT INTO hive.tenants(display_name,status) VALUES ($1,'active') RETURNING id
		), candidate AS (
			INSERT INTO hive.owner_candidates(tenant_id,contact_email)
			SELECT id,'fixture@example.test' FROM tenant RETURNING tenant_id,id
		), principal AS (
			INSERT INTO hive.google_principals(issuer,subject)
			VALUES ('https://accounts.google.com',$2) RETURNING id
		), membership AS (
			INSERT INTO hive.tenant_memberships(tenant_id,principal_id,owner_id,role)
			SELECT tenant_id,principal.id,candidate.id,'primary_owner'
			FROM candidate CROSS JOIN principal RETURNING tenant_id,principal_id
		) INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at)
		SELECT $3,tenant_id,principal_id,statement_timestamp()+interval '1 hour' FROM membership
		RETURNING tenant_id,principal_id`, label, f.ownerToken, identity.Digest(f.ownerToken)).Scan(&f.tenant, &f.owner)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	newStaff := func(tenant, token string, active bool) string {
		t.Helper()
		var id string
		err := db.QueryRow(t.Context(), `WITH principal AS (
			INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com',$2) RETURNING id
		), membership AS (
			INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role,active)
			SELECT $1,id,'staff',$4 FROM principal RETURNING tenant_id,principal_id
		) INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at)
		SELECT $3,tenant_id,principal_id,statement_timestamp()+interval '1 hour' FROM membership
		RETURNING principal_id`, tenant, token, identity.Digest(token), active).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := newTenant("RBAC A"), newTenant("RBAC B")
	const staffToken = "rbac-staff-a"
	staff := newStaff(a.tenant, staffToken, true)
	foreignStaff := newStaff(b.tenant, "rbac-staff-b", true)
	inactive := newStaff(a.tenant, "rbac-staff-inactive", false)
	run(`INSERT INTO hive.platform_principals(principal_id) VALUES ($1)`, a.owner)
	run(`INSERT INTO hive.platform_sessions VALUES ($1,$2,statement_timestamp()+interval '1 hour')`,
		identity.Digest("rbac-platform-token"), a.owner)
	handler := API{TenantRBAC: identity.RBAC{DB: db, FingerprintKey: bytes.Repeat([]byte{3}, 32)}, AppOrigin: appOrigin, AdminOrigin: adminOrigin}.Handler()
	request := func(method, path, token, key string, body any) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Origin", appOrigin)
		r.Header.Set("Idempotency-Key", key)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: tenantCookie, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	aBase := "/api/v1/app/tenants/" + a.tenant
	bBase := "/api/v1/app/tenants/" + b.tenant
	aRoles, bRoles := aBase+"/roles", bBase+"/roles"
	memberRoles := func(id string) string { return aBase + "/members/" + id + "/roles" }
	roleInput := map[string]any{"key": "reader", "name": "Role reader", "actions": []string{"identity.roles.read"}}
	check(request("GET", aRoles, "", "", nil), 401)
	check(request("GET", aRoles, "rbac-platform-token", "", nil), 401)
	check(request("GET", bRoles, a.ownerToken, "", nil), 401)
	check(request("GET", aRoles, "rbac-staff-inactive", "", nil), 401)
	check(request("GET", aRoles, staffToken, "", nil), 403)
	check(request("POST", aRoles, staffToken, "staff-attempt-key", roleInput), 403)

	create := func(path, token, key string) identity.RoleResult {
		t.Helper()
		w := request("POST", path, token, key, roleInput)
		check(w, 201)
		var out identity.RoleResult
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	list := func(token string) identity.RoleList {
		t.Helper()
		w := request("GET", aRoles, token, "", nil)
		check(w, 200)
		var out identity.RoleList
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if initial := list(a.ownerToken); initial.Revision != 0 || len(initial.Roles) != 0 {
		t.Fatal("new tenant must start with empty roles at revision zero")
	}

	for _, invalid := range []map[string]any{
		{"key": "reader", "name": "Invalid", "actions": []string{"*"}},
		{"key": "reader", "name": "Invalid", "actions": []string{"inventory.stock.receive"}},
		{"key": "primary_owner", "name": "Owner", "actions": []string{}},
	} {
		check(request("POST", aRoles, a.ownerToken, "reject-invalid-key", invalid), 400)
	}
	const createKey = "idempotent-rbac-create"
	responses := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() { <-start; responses <- request("POST", aRoles, a.ownerToken, createKey, roleInput) }()
	}
	close(start)
	first, second := <-responses, <-responses
	check(first, 201)
	check(second, 201)
	if first.Body.String() != second.Body.String() {
		t.Fatal("parallel replay did not return the original role/revision")
	}
	var created identity.RoleResult
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 {
		t.Fatal("parallel create advanced revision more than once")
	}
	check(request("POST", aRoles, a.ownerToken, createKey, map[string]any{"key": "other", "name": "Different", "actions": []string{}}), 409)
	check(request("POST", aRoles, a.ownerToken, "duplicate-role-key", roleInput), 409)
	foreignRole := create(bRoles, b.ownerToken, createKey)
	assign := func(ids []string, revision int64) map[string]any {
		return map[string]any{"role_ids": ids, "revision": revision}
	}
	replace := func(name string, actions []string, revision int64) map[string]any {
		return map[string]any{"name": name, "actions": actions, "revision": revision}
	}
	check(request("PUT", aRoles+"/"+foreignRole.Role.ID, a.ownerToken, "", replace("Foreign", []string{}, 1)), 404)
	check(request("PUT", memberRoles(foreignStaff), a.ownerToken, "", assign([]string{created.Role.ID}, 1)), 404)
	check(request("PUT", memberRoles(staff), a.ownerToken, "", assign([]string{foreignRole.Role.ID}, 1)), 404)
	check(request("PUT", memberRoles(inactive), a.ownerToken, "", assign([]string{created.Role.ID}, 1)), 404)
	check(request("PUT", memberRoles(a.owner), a.ownerToken, "", assign([]string{created.Role.ID}, 1)), 403)
	_, err := db.Exec(t.Context(), `INSERT INTO hive.tenant_role_assignments VALUES ($1,$2,$3)`, a.tenant, staff, foreignRole.Role.ID)
	var fk *pgconn.PgError
	if !errors.As(err, &fk) || fk.Code != "23503" {
		t.Fatal("cross-tenant assignment did not fail its composite FK")
	}

	check(request("PUT", memberRoles(staff), a.ownerToken, "", assign([]string{created.Role.ID}, 1)), 200)
	if after := list(staffToken); after.Revision != 2 || len(after.Roles) != 1 || after.Roles[0].ID != created.Role.ID {
		t.Fatal("existing session did not see the newly granted same-tenant role")
	}
	check(request("PUT", memberRoles(staff), staffToken, "", assign([]string{}, 2)), 403)
	rolePath := aRoles + "/" + created.Role.ID
	check(request("PUT", rolePath, staffToken, "", replace("Escalated", []string{}, 2)), 403)
	check(request("PUT", rolePath, a.ownerToken, "", map[string]any{"name": "Missing revision", "actions": []string{}}), 400)

	run(`CREATE FUNCTION hive.test_rbac_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'fixture audit failure'; END $$;
		CREATE TRIGGER test_rbac_audit_failure BEFORE INSERT ON hive.tenant_rbac_audit
		FOR EACH ROW EXECUTE FUNCTION hive.test_rbac_audit_failure()`)
	check(request("PUT", rolePath, a.ownerToken, "", replace("Audit failure", []string{}, 2)), 503)
	if after := list(staffToken); after.Revision != 2 || after.Roles[0].Name != created.Role.Name {
		t.Fatal("failed audit left a changed revision or role")
	}
	check(request("POST", aRoles, a.ownerToken, "retry-after-audit", map[string]any{"key": "retry", "name": "Retry", "actions": []string{}}), 503)
	run(`DROP TRIGGER test_rbac_audit_failure ON hive.tenant_rbac_audit`)

	start = make(chan struct{})
	for _, name := range []string{"Concurrent A", "Concurrent B"} {
		go func() { <-start; responses <- request("PUT", rolePath, a.ownerToken, "", replace(name, []string{}, 2)) }()
	}
	close(start)
	first, second = <-responses, <-responses
	if first.Code == second.Code || first.Code+second.Code != 200+409 {
		t.Fatalf("parallel writes: %d/%d", first.Code, second.Code)
	}
	check(request("GET", aRoles, staffToken, "", nil), 403)
	check(request("PUT", memberRoles(staff), a.ownerToken, "", assign([]string{}, 2)), 409)
	check(request("PUT", rolePath, a.ownerToken, "", replace("Restored permission", []string{"identity.roles.read"}, 3)), 200)
	check(request("GET", aRoles, staffToken, "", nil), 200)
	check(request("PUT", memberRoles(staff), a.ownerToken, "", assign([]string{}, 4)), 200)
	check(request("GET", aRoles, staffToken, "", nil), 403)
	check(request("POST", aRoles, a.ownerToken, "retry-after-audit", map[string]any{"key": "retry", "name": "Retry", "actions": []string{}}), 201)
	original := create(aRoles, a.ownerToken, createKey)
	if original.Revision != created.Revision || original.Role.Name != created.Role.Name {
		t.Fatal("replay returned the modified role")
	}
	var revision, count, updated int
	err = db.QueryRow(t.Context(), `SELECT
		(SELECT revision FROM hive.tenant_rbac_revisions WHERE tenant_id=$1),
		count(*),
		count(*) FILTER (WHERE action='role.replace' AND before_state->>'name'='Role reader'
			AND after_state->>'name' IN ('Concurrent A','Concurrent B'))
		FROM hive.tenant_rbac_audit WHERE tenant_id=$1`, a.tenant).Scan(&revision, &count, &updated)
	if err != nil || revision != 6 || count != 6 || updated != 1 {
		t.Fatalf("unexpected revision/audit counts %d/%d/%d: %v", revision, count, updated, err)
	}
}
