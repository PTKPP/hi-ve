package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const stagingSchema = 55

func poolConfig(dsn string, staging bool, maxConnections string) (*pgxpool.Config, error) {
	invalid := errors.New("invalid database configuration")
	var host string
	if staging {
		u, err := url.Parse(dsn)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
			u.User.Username() != "hive_app" || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), ",/") {
			return nil, invalid
		}
		host = u.Hostname()
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return nil, invalid
		}
		// Permit only explicit TLS parameters in the staging URL. In particular
		// pgx also accepts query overrides for host, service and TLS settings.
		for key, value := range values {
			if (key != "sslmode" && key != "sslrootcert") || len(value) != 1 {
				return nil, invalid
			}
		}
		if values.Get("sslmode") != "verify-full" || !strings.HasPrefix(values.Get("sslrootcert"), "/") {
			return nil, errors.New("staging database requires verify-full and an explicit CA path")
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, invalid
	}
	if staging {
		tls := cfg.ConnConfig.TLSConfig
		// Validate the effective pgx config too, including environment defaults.
		if cfg.ConnConfig.Host != host || tls == nil || tls.InsecureSkipVerify || tls.ServerName != host ||
			tls.RootCAs == nil || len(cfg.ConnConfig.Fallbacks) != 0 {
			return nil, invalid
		}
		cfg.MaxConns = 12
		cfg.MinConns, cfg.MinIdleConns = 0, 0
		cfg.ConnConfig.ConnectTimeout = 4 * time.Second
		cfg.MaxConnIdleTime = 5 * time.Minute
		cfg.MaxConnLifetime = 30 * time.Minute
		cfg.MaxConnLifetimeJitter = 3 * time.Minute
		cfg.HealthCheckPeriod = time.Minute
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = "12000"
		cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
		if maxConnections != "" {
			n, err := strconv.ParseInt(maxConnections, 10, 32)
			if err != nil || n < 2 || n > 32 {
				return nil, errors.New("invalid staging pool limit")
			}
			cfg.MaxConns = int32(n)
		}
	}
	return cfg, nil
}

func schemaVersion(ctx context.Context, db *pgxpool.Pool, minimum int) (int, error) {
	var version int
	if err := db.QueryRow(ctx, `SELECT version FROM public.hive_schema_version`).Scan(&version); err != nil || version < minimum {
		return 0, errors.New("required backend migrations are missing or unavailable")
	}
	return version, nil
}

type runtimeHTTP struct {
	probe        func(context.Context) bool
	draining     chan struct{}
	mu           sync.Mutex
	sockets      sync.WaitGroup
	requests     atomic.Uint64
	requestNanos atomic.Uint64
	serverErrors atomic.Uint64
}

type runtimeResponse struct {
	http.ResponseWriter
	status int
}

func (w *runtimeResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *runtimeResponse) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *runtimeResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (rt *runtimeHTTP) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/livez" || r.URL.Path == "/readyz" {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path == "/readyz" {
				ctx, cancel := context.WithTimeout(r.Context(), time.Second)
				defer cancel()
				if rt.stopping() || !rt.probe(ctx) || rt.stopping() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		socket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && strings.HasSuffix(r.URL.Path, "/realtime/socket")
		if socket {
			rt.mu.Lock()
			if rt.stopping() {
				rt.mu.Unlock()
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			rt.sockets.Add(1)
			rt.mu.Unlock()
			defer rt.sockets.Done()
		} else {
			if rt.stopping() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			start := time.Now()
			response := &runtimeResponse{ResponseWriter: w}
			defer func() {
				rt.requestNanos.Add(uint64(time.Since(start)))
				rt.requests.Add(1)
				if response.status >= 500 {
					rt.serverErrors.Add(1)
				}
			}()
			w = response
		}
		timeout := 20 * time.Second
		if socket {
			timeout = 10 * time.Minute
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (rt *runtimeHTTP) stopping() bool {
	select {
	case <-rt.draining:
		return true
	default:
		return false
	}
}

func (rt *runtimeHTTP) serve(ctx context.Context, ln net.Listener, server *http.Server) error {
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(ln) }()
	var serveErr error
	select {
	case <-ctx.Done():
	case <-failed:
		serveErr = errors.New("HTTP listener failed")
	}
	rt.mu.Lock()
	close(rt.draining)
	rt.mu.Unlock()

	drained := make(chan struct{})
	go func() {
		rt.sockets.Wait()
		close(drained)
	}()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return errors.New("HTTP shutdown deadline exceeded")
	}
	select {
	case <-drained:
		return serveErr
	case <-shutdown.Done():
		return errors.New("WebSocket drain deadline exceeded")
	}
}

func (rt *runtimeHTTP) logMetrics(db *pgxpool.Pool) {
	pool := db.Stat()
	slog.Info("runtime metrics", "http_requests_total", rt.requests.Load(),
		"http_request_duration_ms_total", rt.requestNanos.Load()/uint64(time.Millisecond),
		"http_server_errors_total", rt.serverErrors.Load(),
		"db_acquired", pool.AcquiredConns(), "db_idle", pool.IdleConns(), "db_max", pool.MaxConns(),
		"db_wait_ms_total", pool.EmptyAcquireWaitTime().Milliseconds(), "db_acquire_canceled_total", pool.CanceledAcquireCount())
}

func probeCLI() int {
	client := http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{}}
	resp, err := client.Get("http://127.0.0.1:8080/readyz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return 1
	}
	return 0
}

func runtimeMode() (bool, error) {
	switch os.Getenv("HIVE_RUNTIME") {
	case "", "development":
		return false, nil
	case "staging":
		return true, nil
	default:
		return false, errors.New("invalid runtime mode")
	}
}
