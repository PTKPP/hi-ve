package main

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStagingPoolConfig(t *testing.T) {
	t.Setenv("PGSERVICE", "")
	t.Setenv("PGSSLSNI", "1")
	tls := httptest.NewTLSServer(http.NotFoundHandler())
	defer tls.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tls.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}

	const base = "postgresql://hive_app:fixture-secret@pg.example.invalid/hive"
	full := base + "?sslmode=verify-full&sslrootcert=" + url.QueryEscape(ca)
	cfg, err := poolConfig(full, true, "8")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 8 || cfg.ConnConfig.TLSConfig.ServerName != "pg.example.invalid" || cfg.ConnConfig.ConnectTimeout != 4*time.Second {
		t.Fatal("staging connection budget or TLS hostname was lost")
	}
	for _, dsn := range []string{
		base, base + "?sslmode=require", base + "?sslmode=prefer", base + "?sslmode=verify-full",
		full + "&sslmode=disable", full + "&host=localhost", full + "&service=fixture",
		strings.Replace(full, "pg.example.invalid", "pg.example.invalid,other.example.invalid", 1),
		strings.Replace(full, url.QueryEscape(ca), "/missing.pem", 1),
	} {
		if _, err := poolConfig(dsn, true, ""); err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatal("unsafe staging config was accepted or echoed")
		}
	}
	if _, err := poolConfig(full, true, "100"); err == nil {
		t.Fatal("unbounded staging pool accepted")
	}
	if _, err := poolConfig(base+"?sslmode=disable", false, ""); err != nil {
		t.Fatal("development DSN stopped working")
	}
	t.Setenv("HIVE_RUNTIME", "typo")
	if _, err := runtimeMode(); err == nil {
		t.Fatal("unknown runtime accepted")
	}
}

func TestRuntimeProbes(t *testing.T) {
	var available atomic.Bool
	rt := &runtimeHTTP{draining: make(chan struct{}), probe: func(ctx context.Context) bool {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("DB probe needs a deadline")
		}
		return available.Load()
	}}
	h := rt.handler(http.NotFoundHandler())
	check := func(method, path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != want || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s received %d", method, path, w.Code)
		}
	}
	check("GET", "/livez", 204)
	check("GET", "/readyz", 503)
	check("POST", "/readyz", 405)
	available.Store(true)
	check("HEAD", "/readyz", 204)
	close(rt.draining)
	check("GET", "/readyz", 503)

	t.Run("disconnected database", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listener.Close()
		db, err := pgxpool.New(t.Context(), "postgresql://hive_app@"+listener.Addr().String()+"/hive?sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err = schemaVersion(ctx, db, stagingSchema); err == nil {
			t.Fatal("connection failure did not fail readiness")
		}
	})
}

func TestRuntimeWaitsForRequestsAndUpgradedHandlers(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rt := &runtimeHTTP{draining: make(chan struct{})}
	started := make(chan string, 2)
	finishHTTP, finishSocket := make(chan struct{}), make(chan struct{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: rt.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realtime/socket" {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			started <- "socket"
			<-rt.draining
			<-finishSocket
		} else {
			started <- "http"
			<-finishHTTP
			w.WriteHeader(http.StatusNoContent)
		}
	}))}
	finished := make(chan error, 1)
	go func() { finished <- rt.serve(ctx, ln, server) }()
	httpDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
		httpDone <- err
	}()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.Write([]byte("GET /realtime/socket HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("requests did not reach handlers")
		}
	}
	cancel()
	<-rt.draining
	close(finishHTTP)
	if err := <-httpDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
		t.Fatal("shutdown exited with an upgraded handler still connected")
	case <-time.After(30 * time.Millisecond):
	}
	close(finishSocket)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestStagingDisposableDatabase(t *testing.T) {
	dsn := os.Getenv("HIVE_STAGING_TEST_URL")
	if dsn == "" {
		t.Skip("run scripts/test-staging-db.sh")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/hive_staging" {
		t.Fatal("expected the loopback staging fixture")
	}
	open := func(dsn string) *pgxpool.Pool {
		t.Helper()
		cfg, err := poolConfig(dsn, true, "2")
		if err != nil {
			t.Fatal(err)
		}
		db, err := pgxpool.NewWithConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal("pool creation failed")
		}
		t.Cleanup(db.Close)
		return db
	}
	db := open(dsn)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var tlsOK, marker bool
	if err := db.QueryRow(ctx, `SELECT ssl, to_regclass('public.staging_disposable_marker') IS NOT NULL
		FROM pg_stat_ssl WHERE pid=pg_backend_pid()`).Scan(&tlsOK, &marker); err != nil || !tlsOK || !marker {
		t.Fatal("expected role-authenticated disposable TLS connection")
	}
	if v, err := schemaVersion(ctx, db, stagingSchema); err != nil || v != stagingSchema {
		t.Fatalf("expected schema 055, got %d", v)
	}
	if _, err := schemaVersion(ctx, db, stagingSchema+1); err == nil {
		t.Fatal("unsupported schema passed readiness")
	}
	for _, bad := range []string{"hostname", "CA"} {
		t.Run(bad, func(t *testing.T) {
			wrong := *u
			if bad == "hostname" {
				wrong.Host = net.JoinHostPort("localhost", u.Port())
			} else {
				params := wrong.Query()
				params.Set("sslrootcert", "/etc/ssl/certs/ca-certificates.crt")
				wrong.RawQuery = params.Encode()
			}
			p := open(wrong.String())
			if err := p.Ping(ctx); err == nil {
				t.Fatal("connection accepted an untrusted hostname or CA")
			}
		})
	}

	// Prove the staged pool stays bounded when all slots are occupied.
	first, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	saturated, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	if extra, err := db.Acquire(saturated); err == nil {
		extra.Release()
		t.Fatal("pool exceeded its configured connection budget")
	}
}
