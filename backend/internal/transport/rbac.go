package transport

import (
	"errors"
	"net/http"

	"hi-ve/backend/internal/identity"
)

func (a API) rbacRoutes(mux *http.ServeMux) {
	const base = "/api/v1/app/tenants/{tenant_id}"
	mux.HandleFunc("GET "+base+"/roles", a.origin(a.AppOrigin, a.listRoles))
	mux.HandleFunc("POST "+base+"/roles", a.origin(a.AppOrigin, a.createRole))
	mux.HandleFunc("PUT "+base+"/roles/{role_id}", a.origin(a.AppOrigin, a.replaceRole))
	mux.HandleFunc("PUT "+base+"/members/{member_id}/roles", a.origin(a.AppOrigin, a.replaceRolesForMember))
}

func rbacError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrRBACInput):
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
	case errors.Is(err, identity.ErrRBACTarget):
		fail(w, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, identity.ErrRBACConflict):
		fail(w, http.StatusConflict, "STATE_CONFLICT")
	case errors.Is(err, identity.ErrRBACReplay):
		fail(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
	case errors.Is(err, identity.ErrRBACRevision):
		fail(w, http.StatusConflict, "REVISION_CONFLICT")
	default:
		handleError(w, err)
	}
}

func (a API) listRoles(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	if !uuid.MatchString(tenant) {
		rbacError(w, identity.ErrRBACInput)
		return
	}
	result, err := a.TenantRBAC.List(r.Context(), tenant, cookie(r, tenantCookie))
	if err != nil {
		rbacError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a API) createRole(w http.ResponseWriter, r *http.Request) {
	tenant, key := r.PathValue("tenant_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !keyPattern.MatchString(key) {
		rbacError(w, identity.ErrRBACInput)
		return
	}
	var input identity.CreateRole
	if !decode(w, r, &input) {
		return
	}
	result, err := a.TenantRBAC.Create(r.Context(), tenant, cookie(r, tenantCookie), key, input)
	if err != nil {
		rbacError(w, err)
		return
	}
	respond(w, http.StatusCreated, result)
}

func (a API) replaceRole(w http.ResponseWriter, r *http.Request) {
	tenant, role := r.PathValue("tenant_id"), r.PathValue("role_id")
	if !uuid.MatchString(tenant) || !uuid.MatchString(role) {
		rbacError(w, identity.ErrRBACInput)
		return
	}
	var input identity.ReplaceRole
	if !decode(w, r, &input) {
		return
	}
	result, err := a.TenantRBAC.Replace(r.Context(), tenant, cookie(r, tenantCookie), role, input)
	if err != nil {
		rbacError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a API) replaceRolesForMember(w http.ResponseWriter, r *http.Request) {
	tenant, member := r.PathValue("tenant_id"), r.PathValue("member_id")
	if !uuid.MatchString(tenant) || !uuid.MatchString(member) {
		rbacError(w, identity.ErrRBACInput)
		return
	}
	var input identity.ReplaceAssignments
	if !decode(w, r, &input) {
		return
	}
	result, err := a.TenantRBAC.Assign(r.Context(), tenant, cookie(r, tenantCookie), member, input)
	if err != nil {
		rbacError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
