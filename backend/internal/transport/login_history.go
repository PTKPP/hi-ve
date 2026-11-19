package transport

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"hi-ve/backend/internal/audit"
)

func (a API) loginHistoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/app/tenants/{tenant_id}/login-history", a.origin(a.AppOrigin, a.loginHistory))
}

func historyFilter(query string) (audit.LoginFilter, bool) {
	filter := audit.LoginFilter{Limit: 50}
	values, err := url.ParseQuery(query)
	if err != nil {
		return filter, false
	}
	for name, value := range values {
		if len(value) != 1 || value[0] == "" {
			return filter, false
		}
		switch name {
		case "limit":
			filter.Limit, err = strconv.Atoi(value[0])
			if err != nil || filter.Limit < 1 || filter.Limit > 100 {
				return filter, false
			}
		case "outcome":
			filter.Outcome = value[0]
			if filter.Outcome != "success" && filter.Outcome != "denied" {
				return filter, false
			}
		case "actor_id":
			filter.Actor = value[0]
			if !uuid.MatchString(filter.Actor) {
				return filter, false
			}
		case "cursor":
			filter.Cursor = value[0]
		case "from", "to":
			at, err := time.Parse(time.RFC3339Nano, value[0])
			if err != nil {
				return filter, false
			}
			if name == "from" {
				filter.From = &at
			} else {
				filter.To = &at
			}
		default:
			return filter, false
		}
	}
	return filter, filter.From == nil || filter.To == nil || filter.From.Before(*filter.To)
}

func (a API) loginHistory(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	filter, ok := historyFilter(r.URL.RawQuery)
	if !uuid.MatchString(tenant) || !ok {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	page, err := a.LoginHistory.List(r.Context(), tenant, cookie(r, tenantCookie), filter)
	if errors.Is(err, audit.ErrInvalidCursor) {
		fail(w, http.StatusBadRequest, "INVALID_CURSOR")
		return
	}
	if err != nil {
		handleError(w, err)
		return
	}
	respond(w, http.StatusOK, page)
}
