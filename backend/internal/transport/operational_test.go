package transport

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
)

func TestOperationalContextPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, _, owner := ownerSessionFixture(t, db, "operational-owner")
	_, _, foreign := ownerSessionFixture(t, db, "operational-foreign")
	var member, outlet, device, role string
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	row := func(out *string, q string, args ...any) {
		t.Helper()
		if err := db.QueryRow(ctx, q, args...).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	row(&member, `INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','operational-test-member') RETURNING id`)
	exec(`INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) VALUES ($1,$2,'staff')`, tenant, member)
	row(&outlet, `INSERT INTO hive.locations(tenant_id,code,name,kind) VALUES ($1,'pos','POS','outlet') RETURNING id`, tenant)
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := devices.PublicKey{Curve: "P-256", Type: "EC", X: encode(private.X.FillBytes(make([]byte, 32))), Y: encode(private.Y.FillBytes(make([]byte, 32)))}
	row(&device, `INSERT INTO hive.devices(tenant_id,pairing_id,public_key,thumbprint) VALUES ($1,'operational-fixture',$2,sha256('operational-fixture'::bytea)) RETURNING id`, tenant, public)
	exec(`INSERT INTO hive.device_outlets VALUES($1,$2,$3)`, tenant, device, outlet)
	row(&role, `INSERT INTO hive.tenant_roles(tenant_id,role_key,name) VALUES ($1,'tester','Tester') RETURNING id`, tenant)
	key := bytes.Repeat([]byte{7}, 32)
	svc := devices.Service{DB: db, RateKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner, LockOutlets: locations.LockActiveOutlets,
		LockOperatorMember: identity.LockOperatorMember, AuthorizeOperator: identity.AuthorizeDeviceOperator}
	h := API{Devices: svc, AppOrigin: appOrigin, Memberships: identity.MembershipLifecycle{DB: db, FingerprintKey: key, RemoveOperatorAccess: svc.RemoveOperatorAccess}}.Handler()
	base := "/api/v1/operator/tenants/" + tenant + "/devices/" + device
	assign := "/api/v1/app/tenants/" + tenant + "/devices/" + device + "/operator-assignments"
	const receipt = "assign-operators-test"
	body := map[string]any{"expected_revision": 1, "assignments": []devices.OperatorAssignment{{Member: member, Outlet: outlet}}}
	expectStatus(t, accessRequest(h, "PUT", assign, appOrigin, receipt, body, foreign), 401)
	expectStatus(t, accessRequest(h, "PUT", assign, appOrigin, receipt, body, owner), 200)
	expectStatus(t, accessRequest(h, "PUT", assign, appOrigin, receipt, body, owner), 200)
	expectStatus(t, accessRequest(h, "PUT", assign, appOrigin, "different-operators-test", body, owner), 409)

	challenge := func(operation, id string, revision int64) string {
		t.Helper()
		input := devices.OperationTarget{Operation: operation, ContextID: id}
		if id != "" {
			input.Revision = &revision
		}
		response := accessRequest(h, "POST", base+"/challenges", appOrigin, "", input)
		expectStatus(t, response, 201)
		var c devices.OperationChallenge
		if err := json.Unmarshal(response.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(struct {
			devices.OperationChallenge
			devices.OperationTarget
			Purpose string `json:"purpose"`
			Tenant  string `json:"tenant_id"`
			Device  string `json:"device_id"`
		}{c, input, "hive.device.operation.v1", tenant, device})
		content := "eyJhbGciOiJFUzI1NiJ9." + encode(payload)
		sum := sha256.Sum256([]byte(content))
		r, s, err := ecdsa.Sign(rand.Reader, private, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		return content + "." + encode(sig)
	}
	use := func(method, path, proof, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, base+path, nil)
		r.Header.Set("Origin", appOrigin)
		r.Header.Set("Device-Proof", proof)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: operatorCookie, Value: token})
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		return response
	}
	stateProof := challenge("state", "", 0)
	expectStatus(t, use("GET", "/context", stateProof, ""), 401)
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { results <- use("GET", "/state", stateProof, "") }()
	}
	a, b := <-results, <-results
	if a.Code == b.Code || a.Code+b.Code != 601 {
		t.Fatalf("nonce replay: %d %d", a.Code, b.Code)
	}
	expired := challenge("state", "", 0)
	exec(`UPDATE hive.device_operation_challenges SET expires_at=clock_timestamp() WHERE tenant_id=$1`, tenant)
	expectStatus(t, use("GET", "/state", expired, ""), 401)
	seed := func(token string, rev int64) string {
		t.Helper()
		var id string
		row(&id, `WITH d AS (UPDATE hive.devices SET context_revision=$4,last_outlet_id=$3 WHERE tenant_id=$1 AND id=$2 RETURNING revision)
 INSERT INTO hive.operational_contexts(tenant_id,device_id,outlet_id,revision,member_id,digest,device_revision,idle_expires_at,expires_at)
 SELECT $1,$2,$3,$4,$5,$6,revision,clock_timestamp()+interval '15 minutes',clock_timestamp()+interval '8 hours' FROM d RETURNING id`,
			tenant, device, outlet, rev, member, identity.Digest(token))
		return id
	}
	old := seed("old-operator", 1)
	expectStatus(t, use("GET", "/context", challenge("context.read", old, 1), "old-operator"), 403)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES($1,$2,'devices.operators.use')`, tenant, role)
	exec(`INSERT INTO hive.tenant_location_role_assignments(tenant_id,member_id,role_id,location_id) VALUES($1,$2,$3,$4)`, tenant, member, role, outlet)
	expectStatus(t, use("GET", "/context", challenge("context.read", old, 1), "old-operator"), 200)
	if _, err := db.Exec(ctx, `INSERT INTO hive.operational_contexts(tenant_id,device_id,outlet_id,revision,member_id,digest,device_revision,idle_expires_at,expires_at)
 SELECT tenant_id,device_id,outlet_id,3,member_id,$2,device_revision,idle_expires_at,expires_at FROM hive.operational_contexts WHERE id=$1`, old, identity.Digest("duplicate")); err == nil {
		t.Fatal("two open contexts accepted")
	}
	logout := challenge("context.logout", old, 1)
	exec(`CREATE FUNCTION hive.test_context_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$;
 CREATE TRIGGER test_context_audit_fail BEFORE INSERT ON hive.operational_device_audit FOR EACH ROW EXECUTE FUNCTION hive.test_context_audit_fail()`)
	expectStatus(t, use("POST", "/context/logout", logout, "old-operator"), 503)
	expectStatus(t, use("GET", "/context", challenge("context.read", old, 1), "old-operator"), 200)
	exec(`DROP TRIGGER test_context_audit_fail ON hive.operational_device_audit`)
	response := use("POST", "/context/logout", logout, "old-operator")
	expectStatus(t, response, 204)
	current := seed("new-operator", 2)
	if response.Result().Header.Get("Set-Cookie") != "" {
		t.Fatal("late logout response can clear the new cookie")
	}
	expectStatus(t, use("POST", "/context/logout", challenge("context.logout", old, 1), "old-operator"), 204)
	expectStatus(t, use("GET", "/context", challenge("context.read", current, 2), "new-operator"), 200)
	expectStatus(t, accessRequest(h, "POST", base+"/context", appOrigin, "", map[string]string{"pin": "000000"}), 405)
	expectStatus(t, accessRequest(h, "PUT", base+"/context/outlet", appOrigin, "", nil), 405)
	pending := challenge("state", "", 0)
	expectStatus(t, accessRequest(h, "POST", "/api/v1/app/tenants/"+tenant+"/members/"+member+"/deactivate", appOrigin,
		"remove-operator-access", map[string]any{"revision": 0, "reason_code": "administrative_action"}, owner), 200)
	var count int
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM hive.device_operator_assignments WHERE tenant_id=$1)
 + (SELECT count(*) FROM hive.operational_contexts WHERE tenant_id=$1 AND ended_at IS NULL)`, tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deactivation retained device access: %d/%v", count, err)
	}
	expectStatus(t, accessRequest(h, "POST", "/api/v1/app/tenants/"+tenant+"/devices/"+device+"/revoke", appOrigin,
		"revoke-operational-device", map[string]int{"expected_revision": 2}, owner), 200)
	expectStatus(t, use("GET", "/state", pending, ""), 401)
}
