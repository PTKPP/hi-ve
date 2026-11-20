package transport

import (
	"errors"
	"net/http"

	"hi-ve/backend/internal/identity"
)

func (a API) staffPINRoutes(mux *http.ServeMux) {
	if a.StaffPIN == nil {
		return
	}
	const base = "/api/v1/app/tenants/{tenant_id}/members"
	mux.HandleFunc("POST "+base+"/pin-profiles", a.origin(a.AppOrigin, a.createStaffPIN))
	mux.HandleFunc("GET "+base+"/{member_id}/pin", a.origin(a.AppOrigin, a.staffPIN))
	mux.HandleFunc("PUT "+base+"/{member_id}/pin", a.origin(a.AppOrigin, a.staffPIN))
}

func (a API) createStaffPIN(w http.ResponseWriter, r *http.Request) {
	tenant, key := r.PathValue("tenant_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !keyPattern.MatchString(key) {
		rbacError(w, identity.ErrRBACInput)
		return
	}
	var input struct {
		Name string `json:"display_name"`
	}
	if !decode(w, r, &input) {
		return
	}
	result, err := a.StaffPIN.CreateProfile(r.Context(), tenant, cookie(r, tenantCookie), key, input.Name)
	if err != nil {
		rbacError(w, err)
		return
	}
	respond(w, 201, struct {
		identity.PINStatus
		Status string `json:"status"`
	}{result, "active"})
}

func (a API) staffPIN(w http.ResponseWriter, r *http.Request) {
	tenant, member := r.PathValue("tenant_id"), r.PathValue("member_id")
	if !uuid.MatchString(tenant) || !uuid.MatchString(member) {
		rbacError(w, identity.ErrRBACInput)
		return
	}
	ctx, token := r.Context(), cookie(r, tenantCookie)
	var result identity.PINStatus
	var err error
	if r.Method == http.MethodPut {
		var input struct {
			PIN      string `json:"pin"`
			Revision *int64 `json:"expected_revision"`
		}
		if !decode(w, r, &input) {
			return
		}
		result, err = a.StaffPIN.Set(ctx, tenant, member, token, input.PIN, input.Revision)
	} else {
		result, err = a.StaffPIN.Status(ctx, tenant, member, token)
	}
	if errors.Is(err, identity.ErrPINBusy) {
		w.Header().Set("Retry-After", "1")
		fail(w, 429, "RATE_LIMITED")
		return
	}
	if err != nil {
		rbacError(w, err)
		return
	}
	respond(w, 200, result)
}
