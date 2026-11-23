package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/offline"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/sales"
)

func (a API) offlineRoutes(mux *http.ServeMux) {
	if a.Offline.DB == nil {
		return
	}
	const device = "/api/v1/operator/tenants/{tenant_id}/devices/{device_id}/offline"
	const owner = "/api/v1/app/tenants/{tenant_id}/offline"
	register := func(method, path string, handler http.HandlerFunc) {
		mux.HandleFunc(method+" "+path, a.origin(a.AppOrigin, func(w http.ResponseWriter, r *http.Request) {
			if !uuid.MatchString(r.PathValue("tenant_id")) || r.PathValue("device_id") != "" && !uuid.MatchString(r.PathValue("device_id")) ||
				r.Method != "GET" && r.URL.RawQuery != "" {
				offlineError(w, offline.ErrInput)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			handler(w, r)
		}))
	}
	register("POST", device+"/lease", func(w http.ResponseWriter, r *http.Request) {
		out, err := a.Offline.Issue(r.Context(), r.PathValue("tenant_id"), r.PathValue("device_id"), r.Header.Get("Device-Proof"), cookie(r, operatorCookie))
		if err != nil {
			offlineError(w, err)
			return
		}
		respond(w, 200, out)
	})
	register("PUT", device+"/leases/{lease}/sales/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in offline.SaleInput
		raw, ok := offlineBody(w, r, &in)
		if !ok {
			return
		}
		out, err := a.Offline.IngestSale(r.Context(), r.PathValue("tenant_id"), r.PathValue("device_id"), r.PathValue("lease"), r.PathValue("id"), r.Header.Get("Device-Proof"), raw, in)
		if err != nil {
			offlineError(w, err)
			return
		}
		respond(w, 200, out)
	})
	register("PUT", device+"/leases/{lease}/logins/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in offline.LoginInput
		raw, ok := offlineBody(w, r, &in)
		if !ok {
			return
		}
		err := a.Offline.IngestLogin(r.Context(), r.PathValue("tenant_id"), r.PathValue("device_id"), r.PathValue("lease"), r.PathValue("id"), r.Header.Get("Device-Proof"), raw, in)
		if err != nil {
			offlineError(w, err)
			return
		}
		respond(w, 200, map[string]string{"id": r.PathValue("id"), "lease_id": r.PathValue("lease"), "state": "recorded"})
	})
	register("PUT", device+"/leases/{lease}/seal", func(w http.ResponseWriter, r *http.Request) {
		var in offline.SealInput
		raw, ok := offlineBody(w, r, &in)
		if !ok {
			return
		}
		err := a.Offline.Seal(r.Context(), r.PathValue("tenant_id"), r.PathValue("device_id"), r.PathValue("lease"), r.Header.Get("Device-Proof"), raw, in)
		if err != nil {
			offlineError(w, err)
			return
		}
		w.WriteHeader(204)
	})
	register("GET", owner+"/review", func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()
		if len(values) > 2 || len(values["after_journal"]) > 1 || len(values["after_sale"]) > 1 {
			offlineError(w, offline.ErrInput)
			return
		}
		out, err := a.Offline.Review(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), values.Get("after_journal"), values.Get("after_sale"))
		if err != nil {
			offlineError(w, err)
			return
		}
		respond(w, 200, out)
	})
	register("PUT", owner+"/sales/{id}/resolution", func(w http.ResponseWriter, r *http.Request) {
		var in offline.Resolution
		if !decode(w, r, &in) {
			return
		}
		if err := a.Offline.Resolve(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), r.PathValue("id"), in); err != nil {
			offlineError(w, err)
			return
		}
		w.WriteHeader(204)
	})
	register("PUT", owner+"/leases/{lease}/close", func(w http.ResponseWriter, r *http.Request) {
		var in offline.CloseInput
		if !decode(w, r, &in) {
			return
		}
		if err := a.Offline.Close(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), r.PathValue("lease"), in); err != nil {
			offlineError(w, err)
			return
		}
		w.WriteHeader(204)
	})
	register("GET", owner+"/devices/{device_id}/outlet", func(w http.ResponseWriter, r *http.Request) {
		out, err := a.Offline.SwitchState(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), r.PathValue("device_id"))
		if err != nil {
			offlineError(w, err)
			return
		}
		respond(w, 200, out)
	})
	register("PUT", owner+"/devices/{device_id}/outlet", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Outlet   string `json:"outlet_id"`
			Revision *int64 `json:"expected_revision"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Revision == nil {
			offlineError(w, offline.ErrInput)
			return
		}
		revision, err := a.Offline.Switch(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), r.PathValue("device_id"), in.Outlet, *in.Revision)
		if err != nil {
			offlineError(w, err)
			return
		}
		respond(w, 200, map[string]int64{"revision": revision})
	})
}

func offlineBody(w http.ResponseWriter, r *http.Request, dst any) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err == nil {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(dst) == nil && d.Decode(new(any)) == io.EOF {
			return raw, true
		}
	}
	offlineError(w, offline.ErrInput)
	return nil, false
}

func offlineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, offline.ErrInput), errors.Is(err, sales.ErrInput), errors.Is(err, catalog.ErrInput):
		fail(w, 400, "INVALID_REQUEST")
	case errors.Is(err, offline.ErrOutstanding):
		fail(w, 409, "OFFLINE_JOURNAL_OUTSTANDING")
	case errors.Is(err, offline.ErrConflict), errors.Is(err, audit.ErrOfflineConflict), errors.Is(err, sales.ErrConflict), errors.Is(err, devices.ErrConflict):
		fail(w, 409, "STATE_CONFLICT")
	case errors.Is(err, offline.ErrUnavailable), errors.Is(err, catalog.ErrUnavailable):
		fail(w, 404, "NOT_FOUND")
	case errors.Is(err, inventory.ErrShortage):
		fail(w, 409, "INSUFFICIENT_STOCK")
	case errors.Is(err, sales.ErrTender):
		fail(w, 409, "INSUFFICIENT_TENDER")
	case errors.Is(err, entitlements.ErrDenied):
		fail(w, 403, "ENTITLEMENT_DENIED")
	case errors.Is(err, identity.ErrForbidden):
		fail(w, 403, "FORBIDDEN")
	case errors.Is(err, devices.ErrProof):
		deviceError(w, err)
	default:
		handleError(w, err)
	}
}
