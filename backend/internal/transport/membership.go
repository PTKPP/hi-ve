package transport

import (
	"net/http"

	"hi-ve/backend/internal/identity"
)

func (a API) membershipRoutes(mux *http.ServeMux) {
	const path = "/api/v1/app/tenants/{tenant_id}/members/{member_id}"
	mux.HandleFunc("POST "+path+"/deactivate", a.origin(a.AppOrigin, a.changeMembership(false)))
	mux.HandleFunc("POST "+path+"/reactivate", a.origin(a.AppOrigin, a.changeMembership(true)))
}

func (a API) changeMembership(enable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, member, key := r.PathValue("tenant_id"), r.PathValue("member_id"), r.Header.Get("Idempotency-Key")
		if !uuid.MatchString(tenant) || !uuid.MatchString(member) || !keyPattern.MatchString(key) {
			rbacError(w, identity.ErrRBACInput)
			return
		}
		var body identity.MembershipChange
		if !decode(w, r, &body) {
			return
		}
		result, err := a.Memberships.Transition(r.Context(), tenant, member, cookie(r, tenantCookie), key, enable, body)
		if err != nil {
			rbacError(w, err)
			return
		}
		respond(w, 200, result)
	}
}
