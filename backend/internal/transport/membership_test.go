package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"hi-ve/backend/internal/identity"
)

func TestMembershipPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	newTenant := func(label string) (string, string) {
		t.Helper()
		var tenant, owner string
		err := db.QueryRow(ctx, `WITH tenant AS (
			INSERT INTO hive.tenants(display_name,status) VALUES ($1,'active') RETURNING id
		), candidate AS (
			INSERT INTO hive.owner_candidates(tenant_id,contact_email) SELECT id,'fixture@example.test' FROM tenant RETURNING tenant_id,id
		), principal AS (
			INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com',$1) RETURNING id
		), member AS (
			INSERT INTO hive.tenant_memberships(tenant_id,principal_id,owner_id,role)
			SELECT tenant_id,principal.id,candidate.id,'primary_owner' FROM candidate CROSS JOIN principal
			RETURNING tenant_id,principal_id
		) INSERT INTO hive.tenant_sessions SELECT $2,tenant_id,principal_id,clock_timestamp()+interval '1 hour' FROM member
		RETURNING tenant_id,principal_id`, label, identity.Digest(label)).Scan(&tenant, &owner)
		if err != nil {
			t.Fatal(err)
		}
		return tenant, owner
	}
	tenant, owner := newTenant("membership-owner")
	other, otherOwner := newTenant("membership-other-owner")
	var staff, role string
	if err := db.QueryRow(ctx, `INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','membership-staff') RETURNING id`).Scan(&staff); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) VALUES ($1,$3,'staff'),($2,$3,'staff')`, tenant, other, staff)
	exec(`INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at,auth_method)
		VALUES ($1,$4,$6,clock_timestamp()+interval '1 hour','device_pin'),
		($2,$4,$6,clock_timestamp()+interval '1 hour','device_pin'),
		($3,$5,$6,clock_timestamp()+interval '1 hour','device_pin'),
		($7,$4,$6,clock_timestamp()+interval '1 hour','device_pin'),
		($8,$4,$6,clock_timestamp()+interval '1 hour','device_pin')`,
		identity.Digest("staff-browser-a"), identity.Digest("staff-browser-b"), identity.Digest("staff-other-tenant"), tenant, other, staff,
		identity.Digest("staff-browser-c"), identity.Digest("staff-browser-rotation"))
	if err := db.QueryRow(ctx, `INSERT INTO hive.tenant_roles(tenant_id,role_key,name) VALUES ($1,'reader','Reader') RETURNING id`, tenant).Scan(&role); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hive.tenant_role_actions VALUES($1,$2,'identity.roles.read')`, tenant, role)
	exec(`INSERT INTO hive.tenant_role_assignments VALUES($1,$2,$3)`, tenant, staff, role)
	exec(`INSERT INTO hive.platform_principals(principal_id) VALUES($1)`, owner)
	exec(`INSERT INTO hive.platform_sessions VALUES($1,$2,clock_timestamp()+interval '1 hour')`, identity.Digest("membership-platform"), owner)

	google, signed := signedProvider(t)
	key := bytes.Repeat([]byte{5}, 32)
	h := API{Identity: identity.Store{DB: db}, Memberships: identity.MembershipLifecycle{DB: db, FingerprintKey: key},
		TenantRBAC: identity.RBAC{DB: db, FingerprintKey: key}, Google: google, AppOrigin: appOrigin, AdminOrigin: adminOrigin}.Handler()
	cookieFor := func(value string) *http.Cookie { return &http.Cookie{Name: tenantCookie, Value: value} }
	ownerCookie, staffA, staffB, staffOther := cookieFor("membership-owner"), cookieFor("staff-browser-a"), cookieFor("staff-browser-b"), cookieFor("staff-other-tenant")
	call := func(method, path, key string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(h, method, path, appOrigin, key, body, cookies...)
	}
	base := "/api/v1/app/tenants/" + tenant
	target := base + "/members/" + staff
	deactivate, reactivate := target+"/deactivate", target+"/reactivate"
	change := func(revision int) map[string]any {
		return map[string]any{"revision": revision, "reason_code": "administrative_action"}
	}
	const changeKey = "membership-first-deactivate"
	expectStatus(t, call("POST", deactivate, changeKey, change(0), staffA), 403)
	expectStatus(t, call("POST", deactivate, changeKey, change(0), cookieFor("membership-platform")), 401)
	expectStatus(t, call("POST", base+"/members/"+owner+"/deactivate", changeKey, change(0), ownerCookie), 403)
	expectStatus(t, call("POST", base+"/members/"+otherOwner+"/deactivate", changeKey, change(0), ownerCookie), 404)
	expectStatus(t, call("POST", deactivate, changeKey, change(3), ownerCookie), 409)
	expectStatus(t, call("GET", base+"/roles", "", nil, staffA), 200)

	exec(`CREATE FUNCTION hive.test_membership_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'fixture failure'; END $$;
	CREATE TRIGGER test_membership_audit_failure BEFORE INSERT ON hive.membership_lifecycle_audit
	FOR EACH ROW EXECUTE FUNCTION hive.test_membership_audit_failure()`)
	expectStatus(t, call("POST", deactivate, changeKey, change(0), ownerCookie), 503)
	expectStatus(t, call("GET", base+"/roles", "", nil, staffA), 200)
	expectStatus(t, call("GET", base+"/session", "", nil, staffB), 200)
	var active bool
	var membershipRevision, receiptCount int
	err := db.QueryRow(ctx, `SELECT active,lifecycle_revision,(SELECT count(*) FROM hive.membership_lifecycle_receipts WHERE tenant_id=$1)
		FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$2`, tenant, staff).
		Scan(&active, &membershipRevision, &receiptCount)
	if err != nil || !active || membershipRevision != 0 || receiptCount != 0 {
		t.Fatalf("partial lifecycle mutation after audit failure: %v", err)
	}
	exec(`DROP TRIGGER test_membership_audit_failure ON hive.membership_lifecycle_audit`)

	freshLogin := func(subject string) (string, *http.Cookie) {
		t.Helper()
		w := call("POST", base+"/login", "", nil)
		expectStatus(t, w, 200)
		var payload map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		auth, err := url.Parse(payload["authorization_url"])
		if err != nil {
			t.Fatal(err)
		}
		q := auth.Query()
		callback := "/api/v1/oidc/google/callback?" + url.Values{
			"state": {q.Get("state")}, "code": {signed(subject, q.Get("nonce"), "")},
		}.Encode()
		return callback, w.Result().Cookies()[0]
	}
	callback, browser := freshLogin("membership-staff")
	expectStatus(t, call("GET", callback, "", nil, browser), 403)
	// Rotate a browser from a staff fixture session to a real Owner Google session.
	// The existing staff membership must remain locked until issuance commits.
	exec(`CREATE FUNCTION hive.test_hold_membership_issuance() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN PERFORM pg_advisory_xact_lock(8031206); RETURN NEW; END $$;
	CREATE TRIGGER test_hold_membership_issuance BEFORE INSERT ON hive.tenant_sessions
	FOR EACH ROW EXECUTE FUNCTION hive.test_hold_membership_issuance()`)
	blocker, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(8031206)`); err != nil {
		t.Fatal(err)
	}
	callback, browser = freshLogin("membership-owner")
	loginDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { loginDone <- call("GET", callback, "", nil, browser, cookieFor("staff-browser-rotation")) }()
	issuerPID := waitForBlocked(t, db, blocker.Conn().PgConn().PID())
	deactivated := make(chan *httptest.ResponseRecorder, 1)
	go func() { deactivated <- call("POST", deactivate, changeKey, change(0), ownerCookie) }()
	deactivatePID := waitForBlocked(t, db, issuerPID)

	// The concurrent assignment queues behind the tenant RBAC revision, not the session token.
	assignment := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		assignment <- call("PUT", target+"/roles", "", map[string]any{"revision": 0, "role_ids": []string{role}}, ownerCookie)
	}()
	waitForBlocked(t, db, deactivatePID)
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	issued := <-loginDone
	expectStatus(t, issued, 204)
	newOwner := issued.Result().Cookies()[1]
	first := <-deactivated
	expectStatus(t, first, 200)
	expectStatus(t, <-assignment, 409)
	exec(`DROP TRIGGER test_hold_membership_issuance ON hive.tenant_sessions`)
	for _, session := range []*http.Cookie{staffA, staffB, cookieFor("staff-browser-c"), cookieFor("staff-browser-rotation")} {
		expectStatus(t, call("GET", base+"/session", "", nil, session), 401)
	}
	expectStatus(t, call("GET", "/api/v1/app/tenants/"+other+"/session", "", nil, staffOther), 200)
	callback, browser = freshLogin("membership-staff")
	expectStatus(t, call("GET", callback, "", nil, browser), 403)
	replay := call("POST", deactivate, changeKey, change(0), ownerCookie)
	if replay.Body.String() != first.Body.String() {
		t.Fatal("deactivation replay changed result")
	}
	expectStatus(t, call("POST", deactivate, changeKey, change(1), ownerCookie), 409)
	expectStatus(t, call("POST", deactivate, "old-revision-other-key", change(0), ownerCookie), 409)
	expectStatus(t, call("POST", reactivate, "membership-reactivate-key", change(1), ownerCookie), 200)
	expectStatus(t, call("GET", base+"/session", "", nil, staffA), 401)
	callback, browser = freshLogin("membership-staff")
	expectStatus(t, call("GET", callback, "", nil, browser), 403)
	// A fixture representing a future fresh staff login must have no prior grants.
	reauth := cookieFor("staff-new-fixture")
	exec(`INSERT INTO hive.tenant_sessions (digest,tenant_id,principal_id,expires_at,auth_method)
		VALUES ($1,$2,$3,clock_timestamp()+interval '1 hour','device_pin')`, identity.Digest(reauth.Value), tenant, staff)
	expectStatus(t, call("GET", base+"/roles", "", nil, reauth), 403)
	expectStatus(t, call("POST", deactivate, changeKey, change(0), ownerCookie), 200)
	expectStatus(t, call("GET", base+"/session", "", nil, reauth), 200)
	expectStatus(t, call("GET", base+"/session", "", nil, newOwner), 200)

	var count, removed int
	err = db.QueryRow(ctx, `SELECT count(*),max(revoked_sessions) FROM hive.membership_lifecycle_audit
		WHERE tenant_id=$1 AND member_id=$2`, tenant, staff).Scan(&count, &removed)
	if err != nil || count != 2 || removed != 3 {
		t.Fatalf("audit lost history or overcounted: %d/%d: %v", count, removed, err)
	}
	var mirrored int
	err = db.QueryRow(ctx, `SELECT count(*) FROM hive.identity_principals WHERE id IN ($1,$2)`, owner, staff).Scan(&mirrored)
	if err != nil || mirrored != 2 {
		t.Fatalf("legacy Google writer did not keep neutral registry in sync: %v", err)
	}
	// The expand step mirrors existing Google identities and keeps PIN-only onboarding gated.
	_, err = db.Exec(ctx, `WITH neutral AS (INSERT INTO hive.identity_principals (id)
		VALUES (gen_random_uuid()) RETURNING id)
		INSERT INTO hive.tenant_memberships (tenant_id,principal_id,role)
		SELECT $1,id,'staff' FROM neutral`, tenant)
	var fk *pgconn.PgError
	if !errors.As(err, &fk) || fk.ConstraintName != "tenant_memberships_principal_id_fkey" {
		t.Fatalf("neutral-only membership bypassed the legacy Google FK: %v", err)
	}

}
