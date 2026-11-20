package transport

import (
	"net/http"

	"hi-ve/backend/internal/identity"
)

func locationPolicyPath(w http.ResponseWriter, r *http.Request, param string) (string, string, string, bool) {
	tenant, target, key := r.PathValue("tenant_id"), r.PathValue(param), r.Header.Get("Idempotency-Key")
	if r.Method == http.MethodGet {
		key = ""
	}
	if !uuid.MatchString(tenant) || !uuid.MatchString(target) || (r.Method == http.MethodPut && !keyPattern.MatchString(key)) {
		rbacError(w, identity.ErrRBACInput)
		return "", "", "", false
	}
	return tenant, target, key, true
}

func (a API) locationActions(w http.ResponseWriter, r *http.Request) {
	tenant, target, key, ok := locationPolicyPath(w, r, "role_id")
	if !ok {
		return
	}
	var in struct {
		Revision *int64   `json:"expected_revision"`
		Actions  []string `json:"actions"`
	}
	if key != "" && !decode(w, r, &in) {
		return
	}
	out, err := a.TenantRBAC.LocationActions(r.Context(), tenant, cookie(r, tenantCookie), target, key, in.Revision, in.Actions)
	if err != nil {
		rbacError(w, err)
		return
	}
	respond(w, 200, map[string]any{"role_id": target, "actions": out.Values, "revision": out.Revision})
}

func (a API) locationAssignments(w http.ResponseWriter, r *http.Request) {
	tenant, target, key, ok := locationPolicyPath(w, r, "member_id")
	if !ok {
		return
	}
	var in struct {
		Revision    *int64                        `json:"expected_revision"`
		Assignments []identity.LocationAssignment `json:"assignments"`
	}
	if key != "" && !decode(w, r, &in) {
		return
	}
	out, err := a.TenantRBAC.LocationAssignments(r.Context(), tenant, cookie(r, tenantCookie), target, key, in.Revision, in.Assignments)
	if err != nil {
		rbacError(w, err)
		return
	}
	respond(w, 200, map[string]any{"member_id": target, "assignments": out.Values, "revision": out.Revision})
}
