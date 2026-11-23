package transport

import (
	"errors"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/provisioning"
	"hi-ve/backend/internal/realtime"
	"hi-ve/backend/internal/settings"
	"net/http"
)

func (a API) controlsRoutes(mux *http.ServeMux) {
	if a.Global.DB == nil {
		return
	}
	const path = "/api/v1/admin/settings/{key}"
	mux.HandleFunc("GET "+path, a.admin(func(w http.ResponseWriter, r *http.Request) {
		result, err := a.Global.Get(r.Context(), cookie(r, platformCookie), r.PathValue("key"))
		if err != nil {
			controlError(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("PUT "+path, a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled  *bool  `json:"enabled"`
			Revision *int64 `json:"revision"`
		}
		if !decode(w, r, &in) {
			return
		}
		request := r.Header.Get("Idempotency-Key")
		if !keyPattern.MatchString(request) || in.Enabled == nil || in.Revision == nil {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		result, err := a.Global.Put(r.Context(), cookie(r, platformCookie), r.PathValue("key"), request, *in.Enabled, *in.Revision)
		if err != nil {
			controlError(w, err)
			return
		}
		respond(w, 200, result)
	}))
	if !a.Devices.TenantEpochSchema {
		return
	}
	mux.HandleFunc("POST /api/v1/admin/tenants/{tenant_id}/lifecycle/{action}", a.admin(func(w http.ResponseWriter, r *http.Request) {
		tenant, action := r.PathValue("tenant_id"), r.PathValue("action")
		var in provisioning.Administration
		if !decode(w, r, &in) {
			return
		}
		if !uuid.MatchString(tenant) || !referencePattern.MatchString(in.Case) ||
			action != "suspend" && action != "reactivate" && action != "close" && action != "transfer" ||
			action == "transfer" && (!uuid.MatchString(in.Expected) || !uuid.MatchString(in.Candidate) || !referencePattern.MatchString(in.Evidence)) ||
			action != "transfer" && (in.Expected != "" || in.Candidate != "" || in.Evidence != "") {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		result, err := a.Provisioning.Administer(r.Context(), cookie(r, platformCookie), tenant, action, in, devices.FenceTenantContexts, a.Devices.RemoveOperatorAccess)
		if err != nil {
			controlError(w, err)
			return
		}
		respond(w, 200, result)
	}))
}
func controlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, settings.ErrGlobalInput), errors.Is(err, realtime.ErrPushInput):
		fail(w, 400, "INVALID_REQUEST")
	case errors.Is(err, settings.ErrGlobalConflict):
		fail(w, 409, "REVISION_CONFLICT")
	case errors.Is(err, identity.ErrOwnerCandidate):
		fail(w, 409, "GOOGLE_IDENTITY_REQUIRED")
	case errors.Is(err, realtime.ErrPushUnavailable):
		fail(w, 404, "WEB_PUSH_UNAVAILABLE")
	default:
		reportError(w, err)
	}
}
func (a API) pushRoutes(mux *http.ServeMux) {
	if !a.PushSchema || a.Realtime.DB == nil {
		return
	}
	const root = "/api/v1/app/tenants/{tenant_id}/realtime/push"
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		return a.origin(a.AppOrigin, func(w http.ResponseWriter, r *http.Request) {
			if !uuid.MatchString(r.PathValue("tenant_id")) {
				fail(w, 400, "INVALID_REQUEST")
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET "+root, wrap(func(w http.ResponseWriter, r *http.Request) {
		key, err := a.Realtime.PushKey(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie))
		if err != nil {
			controlError(w, err)
			return
		}
		respond(w, 200, map[string]string{"public_key": key})
	}))
	mux.HandleFunc("PUT "+root, wrap(func(w http.ResponseWriter, r *http.Request) {
		var in realtime.PushSubscription
		if !decode(w, r, &in) {
			return
		}
		if err := a.Realtime.Subscribe(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), in); err != nil {
			controlError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("DELETE "+root, wrap(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Endpoint string `json:"endpoint"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := a.Realtime.Unsubscribe(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), in.Endpoint); err != nil {
			controlError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}
