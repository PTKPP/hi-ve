package transport

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/platform/provisioning"
)

const (
	platformCookie = "__Host-hive-platform"
	tenantCookie   = "__Host-hive-tenant"
	flowCookie     = "__Host-hive-oidc"
)

var (
	uuid             = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	keyPattern       = regexp.MustCompile(`^[A-Za-z0-9_.:-]{16,128}$`)
	referencePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

type Google interface {
	AuthorizationURL(string, string, string) string
	Exchange(context.Context, string, string, string) (identity.GoogleIdentity, error)
}

type API struct {
	Provisioning           provisioning.Service
	Identity               identity.Store
	TenantRBAC             identity.RBAC
	Entitlements           entitlements.Service
	Memberships            identity.MembershipLifecycle
	Locations              locations.Service
	Devices                devices.Service
	Google                 Google
	AdminOrigin, AppOrigin string
	Deliver                func(context.Context, provisioning.Delivery) error
}

func (a API) Handler() http.Handler {
	mux := http.NewServeMux()
	a.rbacRoutes(mux)
	a.entitlementRoutes(mux)
	a.membershipRoutes(mux)
	a.locationRoutes(mux)
	a.logoutRoutes(mux)
	a.deviceRoutes(mux)
	mux.HandleFunc("POST /api/v1/admin/tenants", a.admin(a.create))
	mux.HandleFunc("POST /api/v1/admin/tenants/{tenant_id}/activate", a.admin(a.activate))
	mux.HandleFunc("POST /api/v1/admin/tenants/{tenant_id}/owner-invitation", a.admin(a.reissue))
	mux.HandleFunc("POST /api/v1/admin/login", a.origin(a.AdminOrigin, a.startFlow("platform")))
	mux.HandleFunc("POST /api/v1/app/tenants/{tenant_id}/login", a.origin(a.AppOrigin, a.startFlow("tenant")))
	mux.HandleFunc("POST /api/v1/app/owner-claim", a.origin(a.AppOrigin, a.startFlow("claim")))
	mux.HandleFunc("GET /api/v1/oidc/google/callback", a.callback)
	mux.HandleFunc("GET /api/v1/app/tenants/{tenant_id}/session", a.origin(a.AppOrigin, a.tenantSession))
	mux.HandleFunc("OPTIONS /api/v1/admin/{rest...}", a.origin(a.AdminOrigin, preflight))
	mux.HandleFunc("OPTIONS /api/v1/app/{rest...}", a.origin(a.AppOrigin, preflight))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func preflight(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

func (a API) origin(want string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if want == "" || r.Header.Get("Origin") != want {
			fail(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", want)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
		next(w, r)
	}
}

func (a API) admin(next http.HandlerFunc) http.HandlerFunc { return a.origin(a.AdminOrigin, next) }

func cookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func setCookie(w http.ResponseWriter, name, value string, seconds int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: seconds, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func respond(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func fail(w http.ResponseWriter, status int, code string) {
	respond(w, status, map[string]string{"code": code, "request_id": rand.Text()})
}

func handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated), errors.Is(err, identity.ErrInvalidProof):
		fail(w, http.StatusUnauthorized, "UNAUTHENTICATED")
	case errors.Is(err, identity.ErrForbidden):
		fail(w, http.StatusForbidden, "FORBIDDEN")
	case errors.Is(err, provisioning.ErrKeyConflict):
		fail(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
	case errors.Is(err, provisioning.ErrConflict):
		fail(w, http.StatusConflict, "STATE_CONFLICT")
	case errors.Is(err, provisioning.ErrNotFound):
		fail(w, http.StatusNotFound, "NOT_FOUND")
	default:
		slog.Error("backend request failed")
		fail(w, http.StatusServiceUnavailable, "TEMPORARILY_UNAVAILABLE")
	}
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return false
	}
	return true
}

func (a API) send(ctx context.Context, delivery provisioning.Delivery) {
	if delivery.Code != "" && (a.Deliver == nil || a.Deliver(ctx, delivery) != nil) {
		slog.Warn("owner invitation delivery failed; use authorized reissue")
	}
}

func (a API) create(w http.ResponseWriter, r *http.Request) {
	var input provisioning.CreateInput
	if !decode(w, r, &input) {
		return
	}
	input.Email = strings.TrimSpace(input.Email)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	email, err := mail.ParseAddress(input.Email)
	key := r.Header.Get("Idempotency-Key")
	if err != nil || email.Address != input.Email || len(input.Email) > 254 || len(input.DisplayName) == 0 ||
		len(input.DisplayName) > 200 || !keyPattern.MatchString(key) {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	result, delivery, err := a.Provisioning.Create(r.Context(), cookie(r, platformCookie), key, input)
	if err != nil {
		handleError(w, err)
		return
	}
	a.send(r.Context(), delivery)
	respond(w, http.StatusCreated, result)
}

func (a API) activate(w http.ResponseWriter, r *http.Request) {
	var input provisioning.ActivateInput
	if !decode(w, r, &input) {
		return
	}
	tenant, key := r.PathValue("tenant_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !uuid.MatchString(input.Claim) || !keyPattern.MatchString(key) ||
		input.Method != "verified-organization-contact" || !referencePattern.MatchString(input.Reference) {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	result, err := a.Provisioning.Activate(r.Context(), cookie(r, platformCookie), key, tenant, input)
	if err != nil {
		handleError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a API) reissue(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	if !uuid.MatchString(tenant) {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	delivery, err := a.Provisioning.Reissue(r.Context(), cookie(r, platformCookie), tenant)
	if err != nil {
		handleError(w, err)
		return
	}
	if a.Deliver == nil || a.Deliver(r.Context(), delivery) != nil {
		handleError(w, errors.New("invitation delivery unavailable"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a API) startFlow(purpose string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flow := identity.Flow{Purpose: purpose, Nonce: rand.Text(), Verifier: oauth2.GenerateVerifier(), Expires: time.Now().Add(10 * time.Minute)}
		switch purpose {
		case "tenant":
			flow.Tenant = r.PathValue("tenant_id")
			if !uuid.MatchString(flow.Tenant) {
				fail(w, http.StatusBadRequest, "INVALID_REQUEST")
				return
			}
		case "claim":
			var body struct {
				Invitation string `json:"invitation"`
			}
			if !decode(w, r, &body) {
				return
			}
			invitation, err := a.Provisioning.Invitation(r.Context(), body.Invitation)
			if err != nil {
				handleError(w, err)
				return
			}
			flow.Tenant, flow.Generation = invitation.Tenant, invitation.Generation
			if invitation.Expires.Before(flow.Expires) {
				flow.Expires = invitation.Expires
			}
		}
		state, browser := rand.Text(), rand.Text()
		if err := a.Identity.SaveFlow(r.Context(), state, browser, flow); err != nil {
			handleError(w, err)
			return
		}
		setCookie(w, flowCookie, browser, 600)
		respond(w, http.StatusOK, map[string]string{"authorization_url": a.Google.AuthorizationURL(state, flow.Nonce, flow.Verifier)})
	}
}

func (a API) callback(w http.ResponseWriter, r *http.Request) {
	state, code := r.URL.Query().Get("state"), r.URL.Query().Get("code")
	browser := cookie(r, flowCookie)
	if state == "" || code == "" || browser == "" {
		handleError(w, identity.ErrInvalidProof)
		return
	}
	flow, err := a.Identity.TakeFlow(r.Context(), state, browser)
	if errors.Is(err, pgx.ErrNoRows) {
		err = identity.ErrInvalidProof
	}
	if err != nil {
		handleError(w, err)
		return
	}
	setCookie(w, flowCookie, "", -1)
	ctx, cancel := context.WithDeadline(r.Context(), flow.Expires)
	defer cancel()
	proof, err := a.Google.Exchange(ctx, code, flow.Nonce, flow.Verifier)
	if err != nil {
		handleError(w, err)
		return
	}
	if flow.Purpose == "claim" {
		claim, err := a.Provisioning.Claim(ctx, flow.Tenant, flow.Generation, flow.Expires, proof)
		if err != nil {
			handleError(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]string{"owner_claim_id": claim})
		return
	}
	name := platformCookie
	if flow.Purpose == "tenant" {
		name = tenantCookie
	}
	const lifetime = 8 * time.Hour
	token := rand.Text()
	if err = a.Identity.StartSession(ctx, proof, flow.Tenant, token, cookie(r, name), time.Now().Add(lifetime)); err != nil {
		handleError(w, err)
		return
	}
	setCookie(w, name, token, int(lifetime.Seconds()))
	w.WriteHeader(http.StatusNoContent)
}

func (a API) tenantSession(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	if !uuid.MatchString(tenant) {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	id, err := a.Identity.TenantPrincipal(r.Context(), cookie(r, tenantCookie), tenant)
	if err != nil {
		handleError(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]string{"tenant_id": tenant, "principal_id": id})
}
