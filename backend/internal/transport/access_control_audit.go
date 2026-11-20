package transport

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"hi-ve/backend/internal/audit"
)

func (a API) accessControlAuditRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/app/tenants/{tenant_id}/audit/sensitive-events", a.origin(a.AppOrigin, a.accessControlAudit))
}

func (a API) accessControlAudit(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !uuid.MatchString(tenant) {
		fail(w, 400, "INVALID_REQUEST")
		return
	}
	limit := 50
	for name, values := range query {
		if len(values) != 1 || values[0] == "" {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		switch name {
		case "limit":
			limit, err = strconv.Atoi(values[0])
			if err != nil || limit < 1 || limit > 100 {
				fail(w, 400, "INVALID_REQUEST")
				return
			}
		case "category":
			if values[0] != audit.AccessControlCategory {
				fail(w, 400, "INVALID_REQUEST")
				return
			}
		case "cursor":
		default:
			fail(w, 400, "INVALID_REQUEST")
			return
		}
	}
	page, err := a.AccessControlAudit.List(r.Context(), tenant, cookie(r, tenantCookie), query.Get("cursor"), limit)
	if errors.Is(err, audit.ErrInvalidCursor) {
		fail(w, 400, "INVALID_CURSOR")
		return
	}
	if err != nil {
		handleError(w, err)
		return
	}
	respond(w, 200, page)
}
