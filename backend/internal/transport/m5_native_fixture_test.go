package transport

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The unchanged binary discovers the synthetic Google issuer over TLS. Its
// dedicated CONNECT proxy forwards only that address, to a local fixture.
func m5OIDCNetwork(t *testing.T) (string, string, func(string, string, string) string) {
	t.Helper()
	base, sign := localOIDCProvider(t)
	provider, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "M5 fixture CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		BasicConstraintsValid: true, IsCA: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		DNSNames: []string{"accounts.google.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	ca := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err = os.WriteFile(ca, pemData, 0600); err != nil {
		t.Fatal(err)
	}
	google := httptest.NewUnstartedServer(httputil.NewSingleHostReverseProxy(provider))
	google.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
	google.StartTLS()
	t.Cleanup(google.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "accounts.google.com:443" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp", google.Listener.Addr().String(), time.Second)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, stream, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		if _, err = stream.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err = stream.Flush(); err != nil {
			return
		}
		go func() {
			_, _ = io.Copy(upstream, stream.Reader)
			_ = upstream.Close()
		}()
		_, _ = io.Copy(client, upstream)
	}))
	t.Cleanup(proxy.Close)
	return proxy.URL, ca, sign
}

type m5Process struct {
	url, name string
	http      *http.Client
}

func startM5Process(t *testing.T, binary, dsn, proxy, ca string) (m5Process, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := os.CreateTemp(t.TempDir(), "api-log")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	cmd := exec.CommandContext(t.Context(), binary)
	cmd.Env = []string{
		"HIVE_DATABASE_URL=" + dsn, "HIVE_LISTEN=" + addr,
		"HIVE_RECEIPT_HMAC_KEY=" + strings.Repeat("13", 32),
		"HIVE_DEVICE_PAIRING_CODE_KEY=" + strings.Repeat("32", 32),
		"HIVE_APP_ORIGIN=" + appOrigin, "HIVE_ADMIN_ORIGIN=" + adminOrigin,
		"HIVE_GOOGLE_REDIRECT_URL=https://api.example.test/api/v1/oidc/google/callback",
		"HIVE_GOOGLE_CLIENT_ID=test-client", "HIVE_GOOGLE_CLIENT_SECRET=fixture",
		// The invitation row is delivered to the synthetic user via a fixture.
		"HIVE_SMTP_ADDRESS=127.0.0.1:1", "HIVE_SMTP_USER=fixture", "HIVE_SMTP_PASSWORD=fixture", "HIVE_SMTP_FROM=fixture@example.test",
		"HTTPS_PROXY=" + proxy, "NO_PROXY=127.0.0.1", "SSL_CERT_FILE=" + ca,
	}
	cmd.Stdout, cmd.Stderr = out, out
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(stopped)
	}()
	stop := func() {
		select {
		case <-stopped:
			return
		default:
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-stopped
		}
	}
	t.Cleanup(stop)
	p := m5Process{url: "http://" + addr, name: filepath.Base(binary), http: &http.Client{Timeout: 10 * time.Second}}
	deadline := time.Now().Add(16 * time.Second)
	for time.Now().Before(deadline) {
		res, getErr := p.http.Get(p.url + "/m5-rehearsal-readiness")
		if getErr == nil {
			res.Body.Close()
			if res.StatusCode == 404 && res.Header.Get("Cache-Control") == "no-store" && res.Header.Get("Referrer-Policy") == "no-referrer" {
				return p, stop
			}
		}
		select {
		case <-stopped:
			log, _ := os.ReadFile(out.Name())
			t.Fatalf("%s exited before startup: %s", p.name, log)
		case <-time.After(25 * time.Millisecond):
		}
	}
	stop()
	log, _ := os.ReadFile(out.Name())
	t.Fatalf("%s startup timed out: %s", p.name, log)
	return p, stop
}

type m5Response struct {
	code    int
	data    []byte
	cookies []*http.Cookie
}

func (p m5Process) call(method, path, origin, key string, input any, cookies ...*http.Cookie) (m5Response, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return m5Response{}, err
	}
	req, err := http.NewRequest(method, p.url+path, bytes.NewReader(encoded))
	if err != nil {
		return m5Response{}, err
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Idempotency-Key", key)
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return m5Response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return m5Response{code: resp.StatusCode, data: body, cookies: resp.Cookies()}, err
}

func (p m5Process) expect(t *testing.T, status int, method, path, origin, key string, input any, cookies ...*http.Cookie) m5Response {
	t.Helper()
	r, err := p.call(method, path, origin, key, input, cookies...)
	if err != nil {
		t.Fatal(err)
	}
	if r.code != status {
		reportedPath, _, _ := strings.Cut(path, "?")
		t.Fatalf("%s %s %s returned %d, want %d: %s", p.name, method, reportedPath, r.code, status, r.data)
	}
	return r
}

func (r m5Response) cookie(t *testing.T, name string) *http.Cookie {
	t.Helper()
	for _, c := range r.cookies {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("expected %s cookie in %d response", name, r.code)
	return nil
}

func (r m5Response) field(t *testing.T, name string) string {
	t.Helper()
	var data map[string]json.RawMessage
	var value string
	if err := json.Unmarshal(r.data, &data); err != nil || json.Unmarshal(data[name], &value) != nil || value == "" {
		t.Fatalf("expected string field %s", name)
	}
	return value
}

// One binary starts the flow and another can consume it, as during a rollout.
func m5Login(t *testing.T, start, callback m5Process, sign func(string, string, string) string,
	path, origin, subject string, input any, status int) m5Response {
	t.Helper()
	flow := start.expect(t, 200, "POST", path, origin, "", input)
	authorization, err := url.Parse(flow.field(t, "authorization_url"))
	if err != nil {
		t.Fatal(err)
	}
	q := authorization.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" {
		t.Fatal("expected OIDC PKCE and nonce")
	}
	result := url.Values{"state": {q.Get("state")}, "code": {sign(subject, q.Get("nonce"), "")}}
	return callback.expect(t, status, "GET", "/api/v1/oidc/google/callback?"+result.Encode(), "", "", nil, flow.cookie(t, flowCookie))
}

func m5RuntimeDSN(t *testing.T, db *pgxpool.Pool) string {
	t.Helper()
	var marked bool
	if err := db.QueryRow(t.Context(), `SELECT to_regclass('public.hive_m5_rehearsal_marker') IS NOT NULL`).Scan(&marked); err != nil || !marked {
		t.Fatal("missing M5 disposable fixture marker")
	}
	dsn, err := url.Parse(os.Getenv("HIVE_DISPOSABLE_TEST_DATABASE"))
	if err != nil || dsn.Hostname() != "127.0.0.1" || dsn.Path != "/hive_test" || dsn.Port() == "" {
		t.Fatal("invalid M5 fixture address")
	}
	dsn.User = url.UserPassword("hive_m5_runtime", "hive_m5_runtime_test")
	dsn.RawQuery = "sslmode=disable&connect_timeout=5"
	return dsn.String()
}

func m5BackendRoot(t *testing.T) string {
	t.Helper()
	// go test's working directory is the source package even with -trimpath.
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(directory, "../..")
}

func m5Migrate(t *testing.T, directory string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "tern", "migrate", "--config", filepath.Join(m5BackendRoot(t), "migrations", "tern.conf"),
		"--migrations", directory, "--conn-string", os.Getenv("HIVE_DISPOSABLE_TEST_DATABASE"))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	raw, err := cmd.CombinedOutput()
	return string(raw), err
}

func m5CopyFile(t *testing.T, path, destination string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(destination, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func m5MigrationDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	files, err := filepath.Glob(filepath.Join(m5BackendRoot(t), "migrations", "*.sql"))
	if err != nil || len(files) < 29 || filepath.Base(files[28]) != "029_m5_contract.sql" {
		t.Fatal("the rehearsal is anchored to schema 028")
	}
	for _, path := range files[:28] {
		m5CopyFile(t, path, filepath.Join(tmp, filepath.Base(path)))
	}
	return tmp
}

func m5CheckMigrate(t *testing.T, directory, expectedFailure string) {
	t.Helper()
	log, err := m5Migrate(t, directory)
	if expectedFailure == "" {
		if err != nil {
			t.Fatalf("tern failed: %v\n%s", err, log)
		}
		return
	}
	if err == nil || !strings.Contains(log, expectedFailure) {
		t.Fatalf("expected %s from tern: %v\n%s", expectedFailure, err, log)
	}
}

func m5NativePaths(t *testing.T) (string, string, string) {
	t.Helper()
	directory := os.Getenv("HIVE_M5_BINARY_DIR")
	if directory == "" {
		t.Skip("run scripts/rehearse-m5-contract.sh")
	}
	if !filepath.IsAbs(directory) || runtime.GOOS != "linux" {
		t.Fatal("M5 fixture requires a native Linux binary directory")
	}
	return filepath.Join(directory, "legacy-api"), filepath.Join(directory, "rollback-api"), filepath.Join(directory, "candidate-api")
}
