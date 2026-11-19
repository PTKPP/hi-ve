package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/provisioning"
)

const (
	adminOrigin = "https://admin.example.test"
	appOrigin   = "https://app.example.test"
	issuer      = "https://accounts.google.com"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func signedProvider(t *testing.T) (*identity.Google, func(string, string, string) string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			respond(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": provider.URL + "/authorize",
				"token_endpoint": provider.URL + "/token", "jwks_uri": provider.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			respond(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code_verifier") == "" {
				http.Error(w, "missing verifier", 400)
				return
			}
			respond(w, 200, map[string]string{"access_token": "mock", "token_type": "Bearer", "id_token": r.Form.Get("code")})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	endpoint, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "accounts.google.com" {
			r = r.Clone(r.Context())
			r.URL.Scheme, r.URL.Host = endpoint.Scheme, endpoint.Host
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	google, err := identity.NewGoogle(oidc.ClientContext(t.Context(), client), "test-client", "test-secret", "https://api.example.test/api/v1/oidc/google/callback")
	if err != nil {
		t.Fatal(err)
	}
	return google, func(subject, nonce, variant string) string {
		claims := map[string]any{"iss": issuer, "aud": "test-client", "sub": subject, "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
		switch variant {
		case "issuer":
			claims["iss"] = "https://untrusted.invalid"
		case "audience":
			claims["aud"] = "another-client"
		case "nonce":
			claims["nonce"] = "another-nonce"
		case "expiry":
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		case "party":
			claims["azp"] = "another-client"
		}
		raw, err := json.Marshal(claims)
		if err != nil {
			t.Fatal(err)
		}
		jws, err := signer.Sign(raw)
		if err != nil {
			t.Fatal(err)
		}
		token, err := jws.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		if variant == "signature" {
			segments := strings.Split(token, ".")
			first := "A"
			if strings.HasPrefix(segments[2], first) {
				first = "B"
			}
			segments[2] = first + segments[2][1:]
			token = strings.Join(segments, ".")
		}
		return token
	}
}

func TestGoogleProof(t *testing.T) {
	google, token := signedProvider(t)
	for _, variant := range []string{"issuer", "audience", "nonce", "expiry", "party", "signature"} {
		t.Run(variant, func(t *testing.T) {
			if _, err := google.Exchange(t.Context(), token("owner", "nonce", variant), "nonce", "verifier"); err != identity.ErrInvalidProof {
				t.Fatalf("invalid proof was accepted or misclassified: %v", err)
			}
		})
	}
	if _, err := google.Exchange(t.Context(), token("owner", "nonce", ""), "nonce", "verifier"); err != nil {
		t.Fatal(err)
	}
}

func TestProvisioningPostgres(t *testing.T) {
	dsn := os.Getenv("HIVE_DISPOSABLE_TEST_DATABASE")
	if dsn == "" {
		t.Skip("run scripts/test-migrations.sh for PostgreSQL integration")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/hive_test" {
		t.Fatal("integration tests require the loopback disposable database")
	}
	db, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var disposable bool
	if err = db.QueryRow(t.Context(), `SELECT to_regclass('public.hive_disposable_test') IS NOT NULL`).Scan(&disposable); err != nil || !disposable {
		t.Fatal("disposable integration marker missing")
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`WITH principal AS (
		INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','admin') RETURNING id
	), admin AS (
		INSERT INTO hive.platform_principals (principal_id) SELECT id FROM principal RETURNING principal_id
	) INSERT INTO hive.platform_permissions (principal_id,permission)
	  SELECT principal_id,permission FROM admin CROSS JOIN (VALUES ('platform.tenants.provision'),('platform.tenants.activate')) AS p(permission)`)

	google, token := signedProvider(t)
	var delivery provisioning.Delivery
	var mu sync.Mutex
	handler := API{Provisioning: provisioning.Service{DB: db, FingerprintKey: bytes.Repeat([]byte{2}, 32)}, Identity: identity.Store{DB: db},
		Google: google, AdminOrigin: adminOrigin, AppOrigin: appOrigin, Deliver: func(_ context.Context, d provisioning.Delivery) error {
			mu.Lock()
			defer mu.Unlock()
			delivery = d
			return nil
		}}.Handler()
	request := func(method, path, origin, key string, body any, c *http.Cookie) *httptest.ResponseRecorder {
		var r io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			r = bytes.NewReader(data)
		}
		req := httptest.NewRequest(method, path, r)
		req.Header.Set("Origin", origin)
		req.Header.Set("Idempotency-Key", key)
		if c != nil {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	check := func(w *httptest.ResponseRecorder, code int) {
		t.Helper()
		if w.Code != code {
			t.Fatalf("response %d, want %d", w.Code, code)
		}
	}
	decodeMap := func(w *httptest.ResponseRecorder) map[string]string {
		t.Helper()
		var data map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	oidcStart := func(path, origin string, input any, subject, variant string) (string, *http.Cookie) {
		t.Helper()
		w := request("POST", path, origin, "", input, nil)
		check(w, 200)
		params, err := url.Parse(decodeMap(w)["authorization_url"])
		if err != nil {
			t.Fatal(err)
		}
		query := params.Query()
		if query.Get("code_challenge_method") != "S256" {
			t.Fatal("PKCE missing")
		}
		callback := "/api/v1/oidc/google/callback?" + url.Values{"state": {query.Get("state")}, "code": {token(subject, query.Get("nonce"), variant)}}.Encode()
		return callback, w.Result().Cookies()[0]
	}
	callback, browser := oidcStart("/api/v1/admin/login", adminOrigin, nil, "not-provisioned", "")
	check(request("GET", callback, "", "", nil, browser), 403)
	callback, browser = oidcStart("/api/v1/admin/login", adminOrigin, nil, "admin", "")
	w := request("GET", callback, "", "", nil, browser)
	check(w, 204)
	adminSession := w.Result().Cookies()[1]
	check(request("GET", callback, "", "", nil, browser), 401)

	const create = "/api/v1/admin/tenants"
	input := provisioning.CreateInput{DisplayName: "Hi-Ve Integration Tenant", Email: "owner@example.test"}
	check(request("POST", create, appOrigin, "first-create-key", input, adminSession), 403)
	createResponses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() {
			createResponses <- request("POST", create, adminOrigin, "first-create-key", input, adminSession)
		}()
	}
	first, second := <-createResponses, <-createResponses
	check(first, 201)
	check(second, 201)
	if first.Body.String() != second.Body.String() {
		t.Fatal("different concurrent create results")
	}
	tenant := decodeMap(first)["tenant_id"]
	mu.Lock()
	invite := delivery.Code
	mu.Unlock()
	other := input
	other.DisplayName = "Conflicting payload"
	check(request("POST", create, adminOrigin, "first-create-key", other, adminSession), 409)
	w = request("POST", create, adminOrigin, "separate-tenant-key", input, adminSession)
	check(w, 201)
	anotherTenant := decodeMap(w)["tenant_id"]
	mu.Lock()
	staleInvite := delivery.Code
	mu.Unlock()
	expiredCallback, expiredBrowser := oidcStart("/api/v1/app/owner-claim", appOrigin, map[string]string{"invitation": staleInvite}, "owner", "")
	exec(`UPDATE hive.oidc_flows SET expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1`, anotherTenant)
	check(request("GET", expiredCallback, "", "", nil, expiredBrowser), 401)
	staleCallback, staleBrowser := oidcStart("/api/v1/app/owner-claim", appOrigin, map[string]string{"invitation": staleInvite}, "owner", "")
	check(request("POST", create+"/"+anotherTenant+"/owner-invitation", adminOrigin, "", nil, adminSession), 204)
	check(request("GET", staleCallback, "", "", nil, staleBrowser), 409)
	check(request("POST", "/api/v1/app/owner-claim", appOrigin, "", map[string]string{"invitation": staleInvite}, nil), 404)
	mu.Lock()
	freshInvite := delivery.Code
	mu.Unlock()
	exec(`UPDATE hive.owner_invitations SET expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1`, anotherTenant)
	check(request("POST", "/api/v1/app/owner-claim", appOrigin, "", map[string]string{"invitation": freshInvite}, nil), 404)
	login := "/api/v1/app/tenants/" + tenant + "/login"
	callback, browser = oidcStart(login, appOrigin, nil, "owner", "")
	check(request("GET", callback, "", "", nil, browser), 403)
	claimInput := map[string]string{"invitation": invite}
	callback, browser = oidcStart("/api/v1/app/owner-claim", appOrigin, claimInput, "owner", "signature")
	check(request("GET", callback, "", "", nil, browser), 401)
	callback, browser = oidcStart("/api/v1/app/owner-claim", appOrigin, claimInput, "owner", "")
	check(request("GET", callback, "", "", nil, &http.Cookie{Name: flowCookie, Value: "wrong-browser"}), 401)
	w = request("GET", callback, "", "", nil, browser)
	check(w, 200)
	if len(w.Result().Cookies()) != 1 {
		t.Fatal("claim issued an operational session")
	}
	claim := decodeMap(w)["owner_claim_id"]
	check(request("GET", callback, "", "", nil, browser), 401)
	check(request("POST", "/api/v1/app/owner-claim", appOrigin, "", claimInput, nil), 404)
	activation := provisioning.ActivateInput{Claim: claim, Method: "verified-organization-contact", Reference: "ORG-112"}
	activate := create + "/" + tenant + "/activate"
	check(request("POST", create+"/"+anotherTenant+"/activate", adminOrigin, "wrong-tenant-key", activation, adminSession), 409)
	exec(`DELETE FROM hive.platform_permissions WHERE permission='platform.tenants.activate'`)
	check(request("POST", activate, adminOrigin, "deny-no-activate", activation, adminSession), 403)
	exec(`INSERT INTO hive.platform_permissions SELECT principal_id,'platform.tenants.activate' FROM hive.platform_principals`)

	exec(`CREATE FUNCTION hive.test_fail_activation_audit() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.action='activate' AND NEW.outcome='succeeded' THEN RAISE EXCEPTION 'integration failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER test_fail_activation_audit BEFORE INSERT ON hive.provisioning_audit FOR EACH ROW EXECUTE FUNCTION hive.test_fail_activation_audit()`)
	check(request("POST", activate, adminOrigin, "activation-retry-key", activation, adminSession), 503)
	var clean bool
	if err = db.QueryRow(t.Context(), `SELECT t.status='pending'
		AND NOT EXISTS(SELECT 1 FROM hive.tenant_memberships WHERE tenant_id=t.id)
		AND NOT EXISTS(SELECT 1 FROM hive.provisioning_receipts WHERE request_key='activation-retry-key')
		AND NOT EXISTS(SELECT 1 FROM hive.google_principals WHERE subject='owner')
		AND NOT EXISTS(SELECT 1 FROM hive.owner_claims WHERE tenant_id=t.id AND consumed_at IS NOT NULL)
		FROM hive.tenants t WHERE t.id=$1`, tenant).Scan(&clean); err != nil || !clean {
		t.Fatalf("rollback was partial: %v", err)
	}
	exec(`DROP TRIGGER test_fail_activation_audit ON hive.provisioning_audit`)

	ready := make(chan struct{})
	response := make(chan *httptest.ResponseRecorder, 2)
	for _, key := range []string{"activation-retry-key", "concurrent-other-key"} {
		go func() { <-ready; response <- request("POST", activate, adminOrigin, key, activation, adminSession) }()
	}
	close(ready)
	x, y := <-response, <-response
	if x.Code+y.Code != 200+409 || x.Code == y.Code {
		t.Fatalf("concurrent results: %d/%d", x.Code, y.Code)
	}
	// Retrying whichever key committed must not consume the claim a second time.
	found := false
	for _, key := range []string{"activation-retry-key", "concurrent-other-key"} {
		result := request("POST", activate, adminOrigin, key, activation, adminSession)
		if result.Code == 200 {
			found = true
			continue
		}
		check(result, 409)
	}
	if !found {
		t.Fatal("successful activation receipt missing")
	}
	var owners, records int
	if err = db.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM hive.tenant_memberships WHERE tenant_id=$1 AND role='primary_owner' AND active),
		(SELECT count(*) FROM hive.provisioning_audit WHERE tenant_id=$1 AND action='activate' AND outcome='succeeded' AND verification_reference='ORG-112')`, tenant).Scan(&owners, &records); err != nil || owners != 1 || records != 1 {
		t.Fatalf("expected one owner and audited activation: %d/%d %v", owners, records, err)
	}

	callback, browser = oidcStart(login, appOrigin, nil, "owner", "")
	w = request("GET", callback, "", "", nil, browser)
	check(w, 204)
	tenantSession := w.Result().Cookies()[1]
	check(request("GET", fmt.Sprintf("/api/v1/app/tenants/%s/session", tenant), appOrigin, "", nil, tenantSession), 200)
	check(request("GET", fmt.Sprintf("/api/v1/app/tenants/%s/session", anotherTenant), appOrigin, "", nil, tenantSession), 401)
	check(request("GET", fmt.Sprintf("/api/v1/app/tenants/%s/session", tenant), appOrigin, "", nil, adminSession), 401)
	check(request("POST", create, adminOrigin, "invalid-admin-session", input, tenantSession), 401)
}
