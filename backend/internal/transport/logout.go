package transport

import "net/http"

func (a API) logoutRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/logout", a.admin(a.logout(false)))
	mux.HandleFunc("POST /api/v1/app/tenants/{tenant_id}/logout", a.origin(a.AppOrigin, a.logout(true)))
}

func (a API) logout(app bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, name := "", platformCookie
		if app {
			tenant, name = r.PathValue("tenant_id"), tenantCookie
			if !uuid.MatchString(tenant) {
				fail(w, 400, "INVALID_REQUEST")
				return
			}
		}
		removed, err := a.Identity.Logout(r.Context(), tenant, cookie(r, name))
		if err != nil {
			handleError(w, err)
			return
		}
		if removed {
			setCookie(w, name, "", -1)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
