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
	"strconv"
	"testing"
	"time"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
)

func TestOperatorPINPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, _, owner := ownerSessionFixture(t, db, "operator-pin-owner")
	foreignTenant, _, foreign := ownerSessionFixture(t, db, "operator-pin-foreign")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	id := func(q string, args ...any) string {
		t.Helper()
		var result string
		if err := db.QueryRow(ctx, q, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := devices.PublicKey{Curve: "P-256", Type: "EC", X: encode(signer.X.FillBytes(make([]byte, 32))), Y: encode(signer.Y.FillBytes(make([]byte, 32)))}
	makeOutlet := func(code string) string {
		return id(`INSERT INTO hive.locations(tenant_id,code,name,kind) VALUES ($1,$2,$2,'outlet') RETURNING id`, tenant, code)
	}
	outlet, otherOutlet := makeOutlet("pin-one"), makeOutlet("pin-two")
	makeDevice := func(label string) string {
		device := id(`INSERT INTO hive.devices(tenant_id,pairing_id,public_key,thumbprint) VALUES ($1,$2,$3,sha256($2::text::bytea)) RETURNING id`, tenant, label, public)
		exec(`INSERT INTO hive.device_outlets VALUES($1,$2,$3),($1,$2,$4)`, tenant, device, outlet, otherOutlet)
		return device
	}
	device, otherDevice := makeDevice("operator-pin-one"), makeDevice("operator-pin-two")
	role := id(`INSERT INTO hive.tenant_roles(tenant_id,role_key,name) VALUES($1,'operator','Operator') RETURNING id`, tenant)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES ($1,$2,'devices.operators.use')`, tenant, role)
	key := bytes.Repeat([]byte{4}, 32)
	svc := devices.Service{DB: db, RateKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner, AuthorizeOperator: identity.AuthorizeDeviceOperator,
		LockOutlets: locations.LockActiveOutlets, PINContextSchema: true}
	newPIN := func() *identity.PIN {
		pin, err := identity.NewPIN(db, key, 1, bytes.Repeat([]byte{5}, 32), 0, nil, svc.EndMemberContexts)
		if err != nil {
			t.Fatal(err)
		}
		return pin
	}
	svc.PIN = newPIN()
	app := API{Devices: svc, StaffPIN: svc.PIN, AppOrigin: appOrigin, LoginHistory: audit.LoginHistory{DB: db, CursorKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner}}
	app.Memberships = identity.MembershipLifecycle{DB: db, FingerprintKey: key, StaffPINSchema: true, RemoveOperatorAccess: svc.RemoveOperatorAccess}
	handler := app.Handler()
	replica := app
	replica.Devices.PIN = newPIN()
	otherHandler := replica.Handler()
	makeMember := func(label, pin string) string {
		profile, err := svc.PIN.CreateProfile(ctx, tenant, owner.Value, label, label)
		if err != nil {
			t.Fatal(err)
		}
		zero := int64(0)
		if _, err = svc.PIN.Set(ctx, tenant, profile.Member, owner.Value, pin, &zero); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO hive.tenant_location_role_assignments(tenant_id,member_id,role_id,location_id)
            VALUES ($1,$2,$3,$4),($1,$2,$3,$5)`, tenant, profile.Member, role, outlet, otherOutlet)
		for _, d := range []string{device, otherDevice} {
			exec(`INSERT INTO hive.device_operator_assignments(tenant_id,device_id,outlet_id,member_id)
               VALUES ($1,$2,$3,$5),($1,$2,$4,$5)`, tenant, d, outlet, otherOutlet, profile.Member)
		}
		return profile.Member
	}
	member := makeMember("pin-first", "012345")
	second := makeMember("pin-second", "098765")
	base := func(device string) string { return "/api/v1/operator/tenants/" + tenant + "/devices/" + device }
	send := func(h http.Handler, method, path, proof string, body any, session *http.Cookie) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Origin", appOrigin)
		req.Header.Set("Device-Proof", proof)
		if session != nil {
			req.AddCookie(session)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	sign := func(payload any) string {
		encoded, _ := json.Marshal(payload)
		content := "eyJhbGciOiJFUzI1NiJ9." + encode(encoded)
		hash := sha256.Sum256([]byte(content))
		r, s, err := ecdsa.Sign(rand.Reader, signer, hash[:])
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		return content + "." + encode(sig)
	}
	challenge := func(device string, target devices.OperationTarget) string {
		start := sign(struct {
			devices.OperationTarget
			Purpose string    `json:"purpose"`
			Tenant  string    `json:"tenant_id"`
			Device  string    `json:"device_id"`
			Request string    `json:"request_id"`
			Issued  time.Time `json:"issued_at"`
		}{target, "hive.device.challenge.bootstrap.v1", tenant, device, id("SELECT gen_random_uuid()"), time.Now()})
		res := send(handler, "POST", base(device)+"/challenges", start, target, nil)
		expectStatus(t, res, 201)
		var c devices.OperationChallenge
		if err := json.Unmarshal(res.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return sign(struct {
			devices.OperationChallenge
			devices.OperationTarget
			Purpose string `json:"purpose"`
			Tenant  string `json:"tenant_id"`
			Device  string `json:"device_id"`
		}{c, target, "hive.device.operation.v1", tenant, device})
	}
	loginRequest := func(device, outlet, member, pin string, rev int64) (string, map[string]any) {
		proof := challenge(device, devices.OperationTarget{Operation: "context.login", Outlet: outlet, Member: member, Revision: &rev})
		return proof, map[string]any{"outlet_id": outlet, "member_id": member, "pin": pin, "expected_revision": rev}
	}
	login := func(device, outlet, member, pin string, rev int64, status int) *httptest.ResponseRecorder {
		t.Helper()
		proof, input := loginRequest(device, outlet, member, pin, rev)
		res := send(handler, "POST", base(device)+"/context", proof, input, nil)
		expectStatus(t, res, status)
		return res
	}
	discovery := challenge(device, devices.OperationTarget{Operation: "profiles", Outlet: outlet})
	expectStatus(t, send(handler, "GET", base(device)+"/profiles?outlet_id="+outlet, "bad-proof", nil, nil), 401)
	profiles := send(handler, "GET", base(device)+"/profiles?outlet_id="+outlet, discovery, nil, nil)
	expectStatus(t, profiles, 200)
	if !bytes.Contains(profiles.Body.Bytes(), []byte(member)) {
		t.Fatal("eligible profile missing")
	}
	expectStatus(t, send(handler, "GET", base(device)+"/profiles?outlet_id="+outlet, discovery, nil, nil), 401)

	// Distinct service instances share the member lease and the failed-attempt limit.
	exec(`INSERT INTO hive.member_pin_attempt_state(tenant_id,member_id,attempt_id,lease_until)
        VALUES($1,$2,gen_random_uuid(),clock_timestamp()+interval '5 seconds')`, tenant, member)
	blockedProof, blockedInput := loginRequest(otherDevice, outlet, member, "000000", 0)
	expectStatus(t, send(otherHandler, "POST", base(otherDevice)+"/context", blockedProof, blockedInput, nil), 429)
	exec(`UPDATE hive.member_pin_attempt_state SET lease_until=clock_timestamp() WHERE tenant_id=$1 AND member_id=$2`, tenant, member)
	exec(`ALTER TABLE hive.operator_login_history ADD CONSTRAINT pin_test_denial_fault CHECK(outcome<>'denied') NOT VALID`)
	login(device, outlet, member, "000000", 0, 401)
	exec(`ALTER TABLE hive.operator_login_history DROP CONSTRAINT pin_test_denial_fault`)
	if failures := id("SELECT failures::text FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2", tenant, member); failures != "1" {
		t.Fatal("failed denial audit lost the PIN counter")
	}
	firstProof, firstInput := loginRequest(device, outlet, member, "000000", 0)
	secondProof, secondInput := loginRequest(otherDevice, outlet, member, "000000", 0)
	results := make(chan int, 2)
	go func() { results <- send(handler, "POST", base(device)+"/context", firstProof, firstInput, nil).Code }()
	go func() {
		results <- send(otherHandler, "POST", base(otherDevice)+"/context", secondProof, secondInput, nil).Code
	}()
	for range 2 {
		if code := <-results; code != 401 && code != 429 {
			t.Fatalf("parallel attempt status %d", code)
		}
	}
	prior := id("SELECT failures::text FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2", tenant, member)
	initial, err := strconv.Atoi(prior)
	if err != nil || initial < 2 || initial > 3 {
		t.Fatalf("unexpected shared counter %q", prior)
	}
	for i := initial; i < 5; i++ {
		login(device, outlet, member, "000000", 0, 401)
	}
	until := id("SELECT locked_until::text FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2", tenant, member)
	login(otherDevice, outlet, member, "012345", 0, 401)
	if now := id("SELECT locked_until::text FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2", tenant, member); now != until {
		t.Fatal("lockout was extended")
	}
	exec("UPDATE hive.member_pin_attempt_state SET locked_until=clock_timestamp() WHERE tenant_id=$1 AND member_id=$2", tenant, member)
	replayProof, replayInput := loginRequest(device, outlet, member, "012345", 0)
	res := send(handler, "POST", base(device)+"/context", replayProof, replayInput, nil)
	expectStatus(t, res, 201)
	replay := send(otherHandler, "POST", base(device)+"/context", replayProof, replayInput, nil)
	if (replay.Code != 401 && replay.Code != 409) || len(replay.Result().Cookies()) != 0 {
		t.Fatal("login proof replay issued a session")
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != operatorCookie || cookies[0].SameSite != http.SameSiteStrictMode || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("unexpected operator cookie")
	}
	var previous devices.OperationalContext
	if err := json.Unmarshal(res.Body.Bytes(), &previous); err != nil || previous.Member != member {
		t.Fatal("unexpected context", err)
	}
	login(device, otherOutlet, second, "098765", 1, 401)
	exec(`ALTER TABLE hive.operator_login_history ADD CONSTRAINT pin_test_audit_fault CHECK (outcome<>'success') NOT VALID`)
	failed := login(device, outlet, second, "098765", 1, 503)
	if len(failed.Result().Cookies()) != 0 {
		t.Fatal("failed audit set a cookie")
	}
	exec(`ALTER TABLE hive.operator_login_history DROP CONSTRAINT pin_test_audit_fault`)
	oldRead := func(status int) {
		rev := previous.Revision
		proof := challenge(device, devices.OperationTarget{Operation: "context.read", ContextID: previous.ID, Revision: &rev})
		expectStatus(t, send(handler, "GET", base(device)+"/context", proof, nil, cookies[0]), status)
	}
	oldRead(200)
	switched := login(device, outlet, second, "098765", 1, 201)
	oldRead(401)
	rev := previous.Revision
	lateProof := challenge(device, devices.OperationTarget{Operation: "context.logout", ContextID: previous.ID, Revision: &rev})
	late := send(handler, "POST", base(device)+"/context/logout", lateProof, nil, cookies[0])
	expectStatus(t, late, 204)
	if len(late.Result().Cookies()) != 0 {
		t.Fatal("late logout mutated the new cookie")
	}
	newCookie := switched.Result().Cookies()[0]
	expectStatus(t, accessRequest(handler, "GET", "/api/v1/app/tenants/"+tenant+"/operator-login-history", appOrigin, "", nil, newCookie), 401)
	expectStatus(t, accessRequest(handler, "GET", "/api/v1/app/tenants/"+tenant+"/operator-login-history?limit=1&member_id="+member, appOrigin, "", nil, owner), 200)
	hidden := accessRequest(handler, "GET", "/api/v1/app/tenants/"+foreignTenant+"/operator-login-history", appOrigin, "", nil, foreign)
	expectStatus(t, hidden, 200)
	if bytes.Contains(hidden.Body.Bytes(), []byte(member)) {
		t.Fatal("cross-tenant audit leak")
	}
	revision := int64(1)
	if _, err := svc.PIN.Set(ctx, tenant, second, owner.Value, "999999", &revision); err != nil {
		t.Fatal(err)
	}
	if count := id("SELECT count(*)::text FROM hive.operational_contexts WHERE tenant_id=$1 AND device_id=$2 AND ended_at IS NULL", tenant, device); count != "0" {
		t.Fatal("reset left a live context")
	}
	// A rollback binary can change lifecycle without updating the newly added credential table.
	exec(`UPDATE hive.tenant_memberships SET active=false,lifecycle_revision=lifecycle_revision+1 WHERE tenant_id=$1 AND principal_id=$2;
    `, tenant, second)
	exec(`UPDATE hive.tenant_memberships SET active=true,lifecycle_revision=lifecycle_revision+1 WHERE tenant_id=$1 AND principal_id=$2`, tenant, second)
	login(device, outlet, second, "999999", 2, 401)
	one, inputOne := loginRequest(otherDevice, outlet, member, "012345", 0)
	two, inputTwo := loginRequest(otherDevice, outlet, member, "012345", 0)
	go func() { results <- send(handler, "POST", base(otherDevice)+"/context", one, inputOne, nil).Code }()
	go func() { results <- send(otherHandler, "POST", base(otherDevice)+"/context", two, inputTwo, nil).Code }()
	successes := 0
	for range 2 {
		switch status := <-results; status {
		case 201:
			successes++
		case 409, 429:
		default:
			t.Errorf("unexpected competing issuance status %d", status)
		}
	}
	if successes != 1 {
		t.Fatalf("competing issuance returned %d successes", successes)
	}
	// Either the refresh commits before deactivation and is revoked, or
	// deactivation commits first and the narrowly scoped login is refused.
	proof, input := loginRequest(otherDevice, outlet, member, "012345", 1)
	deactivate := "/api/v1/app/tenants/" + tenant + "/members/" + member + "/deactivate"
	lifecycle := map[string]any{"revision": 0, "reason_code": "administrative_action"}
	mutation := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		mutation <- accessRequest(handler, "POST", deactivate, appOrigin, "dv3-live-deactivation", lifecycle, owner)
	}()
	raced := send(otherHandler, "POST", base(otherDevice)+"/context", proof, input, nil)
	if raced.Code != 201 && raced.Code != 401 {
		t.Fatalf("unexpected login/lifecycle ordering: %d", raced.Code)
	}
	expectStatus(t, <-mutation, 200)
	if count := id("SELECT count(*)::text FROM hive.operational_contexts WHERE tenant_id=$1 AND member_id=$2 AND ended_at IS NULL", tenant, member); count != "0" {
		t.Fatal("deactivation and issuance left a live context")
	}

	exec(`UPDATE hive.operator_login_history SET occurred_at=statement_timestamp()-interval '168 hours' WHERE tenant_id=$1`, tenant)
	expired := accessRequest(handler, "GET", "/api/v1/app/tenants/"+tenant+"/operator-login-history", appOrigin, "", nil, owner)
	expectStatus(t, expired, 200)
	if !bytes.Contains(expired.Body.Bytes(), []byte(`"events":[]`)) {
		t.Fatal("expired audit remained visible")
	}
	if _, err := app.LoginHistory.CleanupOperators(ctx); err != nil {
		t.Fatal(err)
	}
	if count := id("SELECT count(*)::text FROM hive.operator_login_history WHERE tenant_id=$1", tenant); count != "0" {
		t.Fatal("expired audit remained after cleanup")
	}

}
