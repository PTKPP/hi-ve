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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/platform/provisioning"
	"hi-ve/backend/internal/settings"
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
	codeKey, codeErr := hex.DecodeString(os.Getenv("HIVE_DEVICE_PAIRING_CODE_KEY"))
	previousCodeKey, previousCodeErr := hex.DecodeString(os.Getenv("HIVE_DEVICE_PAIRING_PREVIOUS_CODE_KEY"))
	admin, app := os.Getenv("HIVE_ADMIN_ORIGIN"), os.Getenv("HIVE_APP_ORIGIN")
	redirect := os.Getenv("HIVE_GOOGLE_REDIRECT_URL")
	clientID, clientSecret := os.Getenv("HIVE_GOOGLE_CLIENT_ID"), os.Getenv("HIVE_GOOGLE_CLIENT_SECRET")
	u, parseErr := url.Parse(redirect)
	if dsn == "" || err != nil || len(key) < 32 || codeErr != nil || len(codeKey) < 32 || previousCodeErr != nil ||
		(len(previousCodeKey) != 0 && len(previousCodeKey) < 32) || !validOrigin(admin) || !validOrigin(app) || admin == app ||
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
	if err = db.QueryRow(start, `SELECT version FROM public.hive_schema_version`).Scan(&version); err != nil || version < 28 {
		return errors.New("required backend migrations are missing")
	}
	google, err := identity.NewGoogle(start, clientID, clientSecret, redirect)
	if err != nil {
		return errors.New("Google OIDC discovery unavailable")
	}
	deviceService := devices.Service{DB: db, CatalogCommands: version >= 42, InventoryCommands: version >= 47, SalesCommands: version >= 49, CodeKey: codeKey, PreviousCodeKey: previousCodeKey, RateKey: key,
		AuthorizeOwner: identity.AuthorizeTenantOwner, LockOutlets: locations.LockActiveOutlets,
		LockOperatorMember: identity.LockOperatorMember, AuthorizeOperator: identity.AuthorizeDeviceOperator}
	staffPIN, err := configuredPIN(db, key, version, deviceService)
	if err != nil {
		return err
	}
	deviceService.PINContextSchema = version >= 35
	if deviceService.PINContextSchema {
		deviceService.PIN = staffPIN
	}
	api := transport.API{StaffPIN: staffPIN, Provisioning: provisioning.Service{DB: db, FingerprintKey: key}, Identity: identity.Store{DB: db},
		LoginHistory:       audit.LoginHistory{DB: db, CursorKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner},
		AccessControlAudit: audit.AccessControlReader{DB: db, CursorKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner, TenantLocationIDs: locations.TenantLocationIDs},
		Devices:            deviceService,
		Settings:           settings.Service{DB: db, FingerprintKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner},
		Locations:          locations.Service{DB: db, FingerprintKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner}, Memberships: identity.MembershipLifecycle{DB: db, StaffPINSchema: version >= 31, FingerprintKey: key, RemoveOperatorAccess: deviceService.RemoveOperatorAccess}, Entitlements: entitlements.Service{DB: db, FingerprintKey: key}, TenantRBAC: identity.RBAC{DB: db, CatalogProducts: version >= 42, InventoryActions: version >= 47, SalesActions: version >= 49, LockInventoryLocations: locations.LockActiveInventoryLocations, CatalogPrices: version >= 41, FingerprintKey: key, LockOutlets: locations.LockActiveOutlets}, Google: google, AdminOrigin: admin, AppOrigin: app, Deliver: deliver}
	if version >= 40 {
		api.Locations.EndOutletContexts = deviceService.EndOutletContexts
	}
	if version >= 39 {
		api.Catalog = catalog.Service{DB: db, CheckCapability: api.Entitlements.CheckWrite, AuthorizeOwner: identity.AuthorizeTenantOwner, LockOutlets: locations.LockActiveOutlets}
	}
	if version >= 47 {
		api.InventoryDB = db
	}
	if version >= 49 {
		api.SalesDB = db
	}
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
				backlog, err := api.LoginHistory.Cleanup(cleanupCtx)
				if err != nil && cleanupCtx.Err() == nil {
					slog.Warn("online login history cleanup failed")
				} else if backlog {
					slog.Warn("online login history cleanup reached batch limit")
				}
				if deviceService.PINContextSchema {
					backlog, err := api.LoginHistory.CleanupOperators(cleanupCtx)
					if err != nil && cleanupCtx.Err() == nil {
						slog.Warn("operator login history cleanup failed")
					} else if backlog {
						slog.Warn("operator login history cleanup reached batch limit")
					}
				}
				if err := api.Devices.Cleanup(cleanupCtx); err != nil && cleanupCtx.Err() == nil {
					slog.Warn("expired device pairing state cleanup failed")
				}
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

func configuredPIN(db *pgxpool.Pool, fingerprint []byte, schema int, devices devices.Service) (*identity.PIN, error) {
	currentKey := os.Getenv("HIVE_PIN_PEPPER")
	currentVersion := os.Getenv("HIVE_PIN_PEPPER_VERSION")
	previousKey := os.Getenv("HIVE_PIN_PREVIOUS_PEPPER")
	previousVersion := os.Getenv("HIVE_PIN_PREVIOUS_PEPPER_VERSION")
	if currentKey == "" && currentVersion == "" && previousKey == "" && previousVersion == "" {
		return nil, nil
	}
	if schema < 35 {
		return nil, errors.New("DV3 PIN requires compatible schema")
	}
	current, err := strconv.ParseInt(currentVersion, 10, 32)
	key, keyErr := hex.DecodeString(currentKey)
	previous := int64(0)
	if previousVersion != "" {
		var previousErr error
		previous, previousErr = strconv.ParseInt(previousVersion, 10, 32)
		if previousErr != nil || previous < 1 {
			return nil, identity.ErrPINKey
		}
	}
	old, oldErr := hex.DecodeString(previousKey)
	if err != nil || keyErr != nil || oldErr != nil {
		return nil, identity.ErrPINKey
	}
	return identity.NewPIN(db, fingerprint, int(current), key, int(previous), old, devices.EndMemberContexts)
}
