package transport

import (
	"errors"
	"net/http"

	"hi-ve/backend/internal/platform/entitlements"
)

func (a API) entitlementRoutes(mux *http.ServeMux) {
	const path = "/api/v1/admin/tenants/{tenant_id}/entitlements/{capability_id}"
	mux.HandleFunc("GET "+path, a.admin(a.entitlement))
	mux.HandleFunc("PUT "+path, a.admin(a.grantEntitlement))
	mux.HandleFunc("POST "+path+"/revoke", a.admin(a.revokeEntitlement))
}
func entitlementError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, entitlements.ErrInput):
		fail(w, 400, "INVALID_REQUEST")
	case errors.Is(err, entitlements.ErrMissing):
		fail(w, 404, "NOT_FOUND")
	case errors.Is(err, entitlements.ErrRevision):
		fail(w, 409, "REVISION_CONFLICT")
	case errors.Is(err, entitlements.ErrReplay):
		fail(w, 409, "IDEMPOTENCY_CONFLICT")
	case errors.Is(err, entitlements.ErrState):
		fail(w, 409, "STATE_CONFLICT")
	default:
		handleError(w, err)
	}
}
func (a API) entitlement(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	if !uuid.MatchString(tenant) {
		entitlementError(w, entitlements.ErrInput)
		return
	}
	result, err := a.Entitlements.Get(r.Context(), cookie(r, platformCookie), tenant, r.PathValue("capability_id"))
	if err != nil {
		entitlementError(w, err)
		return
	}
	respond(w, 200, result)
}
func (a API) grantEntitlement(w http.ResponseWriter, r *http.Request) {
	tenant, key := r.PathValue("tenant_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !keyPattern.MatchString(key) {
		entitlementError(w, entitlements.ErrInput)
		return
	}
	var body entitlements.Grant
	if !decode(w, r, &body) {
		return
	}
	result, err := a.Entitlements.Grant(r.Context(), cookie(r, platformCookie), tenant, r.PathValue("capability_id"), key, body)
	if err != nil {
		entitlementError(w, err)
		return
	}
	respond(w, 200, result)
}
func (a API) revokeEntitlement(w http.ResponseWriter, r *http.Request) {
	tenant, key := r.PathValue("tenant_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !keyPattern.MatchString(key) {
		entitlementError(w, entitlements.ErrInput)
		return
	}
	var body entitlements.Revoke
	if !decode(w, r, &body) {
		return
	}
	result, err := a.Entitlements.Revoke(r.Context(), cookie(r, platformCookie), tenant, r.PathValue("capability_id"), key, body)
	if err != nil {
		entitlementError(w, err)
		return
	}
	respond(w, 200, result)
}
