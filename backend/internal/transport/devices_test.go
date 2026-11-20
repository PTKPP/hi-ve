package transport

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
)

func TestDevicesPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, owner, own := ownerSessionFixture(t, db, "pairing-owner")
	foreign, _, other := ownerSessionFixture(t, db, "pairing-foreign")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	location := func(tenant, code, kind string) string {
		t.Helper()
		var id string
		err := db.QueryRow(ctx, `INSERT INTO hive.locations (tenant_id,code,name,kind)
			VALUES ($1,$2,$2,$3) RETURNING id`, tenant, code, kind).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	active := location(tenant, "first", "outlet")
	two := location(tenant, "second", "outlet")
	wrongTenant := location(foreign, "outlet", "outlet")
	warehouse := location(tenant, "warehouse", "warehouse")
	service := devices.Service{DB: db, CodeKey: bytes.Repeat([]byte{7}, 32), RateKey: bytes.Repeat([]byte{8}, 32),
		AuthorizeOwner: identity.AuthorizeTenantOwner, LockOutlets: locations.LockActiveOutlets}
	handler := API{Devices: service, AppOrigin: appOrigin, AdminOrigin: adminOrigin}.Handler()
	const startPath = "/api/v1/devices/pairings"
	encode := base64.RawURLEncoding.EncodeToString
	newKey := func() (*ecdsa.PrivateKey, devices.PublicKey) {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return key, devices.PublicKey{Type: "EC", Curve: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
	}
	start := func(public devices.PublicKey, key string) *httptest.ResponseRecorder {
		return accessRequest(handler, "POST", startPath, appOrigin, key, map[string]any{"public_key_jwk": public})
	}
	challenge := func(resp *httptest.ResponseRecorder) devices.Challenge {
		t.Helper()
		expectStatus(t, resp, 201)
		var result devices.Challenge
		if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil || len(result.ID) != 48 || len(result.Code) != 8 ||
			result.Nonce == "" || time.Until(result.ExpiresAt) < 9*time.Minute {
			t.Fatalf("invalid challenge: %v", err)
		}
		return result
	}
	approve := func(c devices.Challenge, target, key string, outlets []string, token *http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(handler, "POST", "/api/v1/app/tenants/"+target+"/device-pairings/"+c.ID+"/approve",
			appOrigin, key, devices.Approval{Code: c.Code, Outlets: outlets}, token)
	}
	sign := func(c devices.Challenge, key string, private *ecdsa.PrivateKey) string {
		t.Helper()
		b, err := json.Marshal(map[string]any{"purpose": "hive.device.pair.redeem.v1", "pairing_id": c.ID,
			"nonce": c.Nonce, "expires_at": c.ExpiresAt, "idempotency_key": key})
		if err != nil {
			t.Fatal(err)
		}
		body := "eyJhbGciOiJFUzI1NiJ9." + encode(b)
		sum := sha256.Sum256([]byte(body))
		r, s, err := ecdsa.Sign(rand.Reader, private, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		return body + "." + encode(sig)
	}
	redeem := func(c devices.Challenge, key, proof string) *httptest.ResponseRecorder {
		return accessRequest(handler, "POST", startPath+"/"+c.ID+"/redeem", appOrigin, key, map[string]string{"proof": proof})
	}
	const approvalKey, redeemKey = "approve-device-receipt", "redeem-device-receipt"
	private, public := newKey()
	c := challenge(start(public, "start-device-receipt"))
	if replay := challenge(start(public, "start-device-receipt")); replay != c {
		t.Fatal("start replay changed challenge")
	}
	expectStatus(t, approve(c, tenant, approvalKey, []string{active}, nil), 401)
	expectStatus(t, approve(c, foreign, approvalKey, []string{active}, own), 401)
	expectStatus(t, approve(c, tenant, approvalKey, []string{wrongTenant}, own), 404)
	expectStatus(t, approve(c, tenant, approvalKey, []string{warehouse}, own), 404)
	expectStatus(t, approve(c, tenant, approvalKey, []string{active, active}, own), 400)
	original := sign(c, redeemKey, private)
	expectStatus(t, redeem(c, redeemKey, original), 409)
	setFailure := func(action string) {
		t.Helper()
		exec(`DROP TRIGGER IF EXISTS test_pairing_audit_failure ON hive.device_pairing_audit`)
		if action != "" {
			exec(fmt.Sprintf(`CREATE TRIGGER test_pairing_audit_failure BEFORE INSERT ON hive.device_pairing_audit
				FOR EACH ROW WHEN (NEW.tenant_id='%s' AND NEW.action='%s') EXECUTE FUNCTION hive.test_pairing_fail()`, tenant, action))
		}
	}
	exec(`CREATE FUNCTION hive.test_pairing_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$`)
	setFailure("approved")
	expectStatus(t, approve(c, tenant, approvalKey, []string{active, two}, own), 503)
	setFailure("")
	results := make(chan *httptest.ResponseRecorder, 8)
	for i := 0; i < 2; i++ {
		go func() { results <- approve(c, tenant, approvalKey, []string{two, active}, own) }()
	}
	expectStatus(t, <-results, 204)
	expectStatus(t, <-results, 204)
	expectStatus(t, approve(c, foreign, approvalKey, []string{wrongTenant}, other), 404)
	expectStatus(t, approve(c, tenant, approvalKey, []string{active}, own), 409)
	wrong, _ := newKey()
	expectStatus(t, redeem(c, redeemKey, sign(c, redeemKey, wrong)), 401)
	expectStatus(t, redeem(c, "other-redeem-key", original), 401)
	exec(`UPDATE hive.locations SET status='inactive' WHERE tenant_id=$1 AND id=$2`, tenant, two)
	expectStatus(t, redeem(c, redeemKey, original), 404)
	exec(`UPDATE hive.locations SET status='active' WHERE tenant_id=$1 AND id=$2`, tenant, two)
	setFailure("redeemed")
	expectStatus(t, redeem(c, redeemKey, original), 503)
	setFailure("")
	for i := 0; i < 2; i++ {
		go func() { results <- redeem(c, redeemKey, original) }()
	}
	first, second := <-results, <-results
	expectStatus(t, first, 200)
	expectStatus(t, second, 200)
	if first.Body.String() != second.Body.String() || len(first.Result().Cookies()) != 0 {
		t.Fatal("unexpected redemption retry or session")
	}
	var d devices.Device
	if err := json.Unmarshal(first.Body.Bytes(), &d); err != nil || d.Revision != 1 {
		t.Fatalf("invalid device: %v", err)
	}
	expectStatus(t, redeem(c, "different-redeem-key", sign(c, "different-redeem-key", private)), 409)
	revoke := func(target, key string, rev int, token *http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(handler, "POST", "/api/v1/app/tenants/"+target+"/devices/"+d.ID+"/revoke", appOrigin,
			key, map[string]int{"expected_revision": rev}, token)
	}
	const revokeKey = "revoke-device-receipt"
	expectStatus(t, revoke(foreign, revokeKey, 1, other), 404)
	expectStatus(t, revoke(tenant, revokeKey, 2, own), 409)
	setFailure("revoked")
	expectStatus(t, revoke(tenant, revokeKey, 1, own), 503)
	expectStatus(t, redeem(c, redeemKey, original), 200)
	setFailure("")
	for i := 0; i < 2; i++ {
		go func() { results <- revoke(tenant, revokeKey, 1, own) }()
	}
	expectStatus(t, <-results, 200)
	expectStatus(t, <-results, 200)
	expectStatus(t, revoke(tenant, revokeKey, 2, own), 409)
	expectStatus(t, redeem(c, redeemKey, original), 404)
	var audits, assignments, count int
	err := db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM hive.device_pairing_audit WHERE tenant_id=$1 AND actor_id=$2),
		(SELECT count(*) FROM hive.device_outlets WHERE tenant_id=$1 AND device_id=$3),
		(SELECT count(*) FROM hive.devices WHERE tenant_id=$1)`, tenant, owner, d.ID).Scan(&audits, &assignments, &count)
	if err != nil || audits != 3 || assignments != 2 || count != 1 {
		t.Fatalf("unexpected devices/assignments/audit: %d/%d/%d %v", count, assignments, audits, err)
	}
	expired := challenge(start(public, "start-expiry-receipt"))
	exec(`UPDATE hive.device_pairings SET expires_at=clock_timestamp() WHERE id=$1`, expired.ID)
	expectStatus(t, approve(expired, tenant, approvalKey, []string{active}, own), 404)
	expectStatus(t, redeem(expired, redeemKey, sign(expired, redeemKey, private)), 404)
	if err = service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	limited := challenge(start(public, "start-exhausted-receipt"))
	expectStatus(t, start(public, "start-excess-receipt"), 429)
	for i := 0; i < 6; i++ {
		go func() { results <- redeem(limited, redeemKey, "invalid.proof") }()
	}
	failed, throttled := 0, 0
	for i := 0; i < 6; i++ {
		switch (<-results).Code {
		case 401:
			failed++
		case 429:
			throttled++
		}
	}
	if failed != 5 || throttled != 1 {
		t.Fatalf("invalid concurrent proofs: %d denied, %d limited", failed, throttled)
	}

	wrongCode := limited
	if limited.Code == "00000000" {
		wrongCode.Code = "00000001"
	} else {
		wrongCode.Code = "00000000"
	}
	for i := 0; i < 6; i++ {
		go func() { results <- approve(wrongCode, tenant, approvalKey, []string{active}, own) }()
	}
	failed, throttled = 0, 0
	for i := 0; i < 6; i++ {
		switch (<-results).Code {
		case 401:
			failed++
		case 429:
			throttled++
		}
	}
	if failed != 5 || throttled != 1 {
		t.Fatalf("concurrent bad codes: %d denied, %d limited", failed, throttled)
	}

	for i := 0; i < 8; i++ {
		_, pub := newKey()
		raw, _ := json.Marshal(map[string]any{"public_key_jwk": pub})
		req := httptest.NewRequest("POST", startPath, bytes.NewReader(raw))
		req.Header.Set("Origin", appOrigin)
		req.Header.Set("Idempotency-Key", fmt.Sprintf("start-peer-%08d", i))
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		req.Header.Set("CF-Connecting-IP", fmt.Sprintf("198.51.100.%d", i+1))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if i == 7 {
			expectStatus(t, w, 429)
			if strings.Contains(w.Body.String(), "peer") {
				t.Fatal("rate response exposed scope")
			}
		} else {
			expectStatus(t, w, 201)
		}
	}
}
