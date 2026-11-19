package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/platform/provisioning"
	"hi-ve/backend/internal/transport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("backend stopped", "reason", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	dsn := os.Getenv("HIVE_DATABASE_URL")
	key, err := hex.DecodeString(os.Getenv("HIVE_RECEIPT_HMAC_KEY"))
	admin, app := os.Getenv("HIVE_ADMIN_ORIGIN"), os.Getenv("HIVE_APP_ORIGIN")
	redirect := os.Getenv("HIVE_GOOGLE_REDIRECT_URL")
	clientID, clientSecret := os.Getenv("HIVE_GOOGLE_CLIENT_ID"), os.Getenv("HIVE_GOOGLE_CLIENT_SECRET")
	u, parseErr := url.Parse(redirect)
	if dsn == "" || err != nil || len(key) < 32 || !validOrigin(admin) || !validOrigin(app) || admin == app ||
		parseErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Path != "/api/v1/oidc/google/callback" || clientID == "" || clientSecret == "" {
		return errors.New("invalid database, HMAC, origins or OIDC configuration")
	}
	deliver, err := mailer()
	if err != nil {
		return err
	}
	start, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	db, err := pgxpool.New(start, dsn)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	defer db.Close()
	if db.Ping(start) != nil {
		return errors.New("database unavailable")
	}
	var version int
	if err = db.QueryRow(start, `SELECT version FROM public.hive_schema_version`).Scan(&version); err != nil || version < 12 {
		return errors.New("required backend migrations are missing")
	}
	google, err := identity.NewGoogle(start, clientID, clientSecret, redirect)
	if err != nil {
		return errors.New("Google OIDC discovery unavailable")
	}
	api := transport.API{Provisioning: provisioning.Service{DB: db, FingerprintKey: key}, Identity: identity.Store{DB: db},
		Memberships: identity.MembershipLifecycle{DB: db, FingerprintKey: key}, Entitlements: entitlements.Service{DB: db, FingerprintKey: key}, TenantRBAC: identity.RBAC{DB: db, FingerprintKey: key}, Google: google, AdminOrigin: admin, AppOrigin: app, Deliver: deliver}
	address := os.Getenv("HIVE_LISTEN")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	handler := api.Handler()
	server := &http.Server{Addr: address, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		handler.ServeHTTP(w, r.WithContext(request))
	}), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		store := identity.Store{DB: db}
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
				if err := store.RemoveExpiredAuthState(cleanupCtx); err != nil && cleanupCtx.Err() == nil {
					slog.Warn("expired authentication state cleanup failed")
				}
			}
		}
	}()
	defer func() {
		stopCleanup()
		<-cleanupDone
	}()
	failed := make(chan error, 1)
	go func() { failed <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case <-failed:
		return errors.New("HTTP listener failed")
	}
}

func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && raw == "https://"+u.Host
}

func mailer() (func(context.Context, provisioning.Delivery) error, error) {
	address, user, password, from := os.Getenv("HIVE_SMTP_ADDRESS"), os.Getenv("HIVE_SMTP_USER"), os.Getenv("HIVE_SMTP_PASSWORD"), os.Getenv("HIVE_SMTP_FROM")
	host, _, err := net.SplitHostPort(address)
	sender, fromErr := mail.ParseAddress(from)
	if err != nil || host == "" || user == "" || password == "" || fromErr != nil || sender.Address != from {
		return nil, errors.New("invalid SMTP configuration")
	}
	return func(ctx context.Context, d provisioning.Delivery) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		dialer := tls.Dialer{Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		defer conn.Close()
		deadline, _ := ctx.Deadline()
		if err = conn.SetDeadline(deadline); err != nil {
			return err
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer client.Close()
		if err = client.Auth(smtp.PlainAuth("", user, password, host)); err != nil {
			return err
		}
		if err = client.Mail(from); err != nil {
			return err
		}
		if err = client.Rcpt(d.Email); err != nil {
			return err
		}
		data, err := client.Data()
		if err != nil {
			return err
		}
		message := "From: " + from + "\r\nTo: " + d.Email + "\r\nSubject: Hi-Ve Primary Owner invitation\r\n\r\n" +
			"Enter this code in the Hi-Ve Owner application within 24 hours:\r\n" + d.Code + "\r\n" +
			"Complete Google sign-in and contact your organization administrator for verification.\r\n"
		_, err = data.Write([]byte(strings.TrimSpace(message) + "\r\n"))
		if err != nil {
			return err
		}
		return data.Close()
	}, nil
}
