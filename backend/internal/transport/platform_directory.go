package transport

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/provisioning"
)

func (a API) directoryRoutes(mux *http.ServeMux) {
	if a.Reports.DB == nil || a.Provisioning.DB == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/admin/tenants", a.admin(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		after := q.Get("after")
		if len(q) != 0 && (len(q) != 1 || len(q["after"]) != 1 || !uuid.MatchString(after)) {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		rows, err := a.Provisioning.Directory(r.Context(), cookie(r, platformCookie), after)
		if err != nil {
			handleError(w, err)
			return
		}
		out := struct {
			Tenants []provisioning.TenantEntry `json:"tenants"`
			Next    string                     `json:"next,omitempty"`
		}{Tenants: rows}
		if len(rows) > 50 {
			out.Next = rows[49].ID
			out.Tenants = rows[:50]
		}
		respond(w, 200, out)
	}))
	mux.HandleFunc("GET /api/v1/admin/tenants/{tenant_id}/audit", a.admin(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tenant, id := r.PathValue("tenant_id"), q.Get("before_id")
		var before *time.Time
		if len(q) > 0 {
			at, err := time.Parse(time.RFC3339Nano, q.Get("before"))
			if err != nil || len(q) != 2 || len(q["before"]) != 1 || len(q["before_id"]) != 1 || !uuid.MatchString(id) {
				fail(w, 400, "INVALID_REQUEST")
				return
			}
			before = &at
		}
		if !uuid.MatchString(tenant) {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		var rows []audit.PlatformEvent
		err := pgx.BeginFunc(r.Context(), a.Provisioning.DB, func(tx pgx.Tx) error {
			if _, err := identity.AuthorizePlatform(r.Context(), tx, cookie(r, platformCookie), "platform.audit.read"); err != nil {
				return err
			}
			var err error
			rows, err = audit.PlatformProvisioning(r.Context(), tx, tenant, id, before)
			return err
		})
		if err != nil {
			handleError(w, err)
			return
		}
		out := struct {
			Events []audit.PlatformEvent `json:"events"`
			Next   *audit.PlatformEvent  `json:"next,omitempty"`
		}{Events: rows}
		if len(rows) > 50 {
			out.Next = &rows[49]
			out.Events = rows[:50]
		}
		respond(w, 200, out)
	}))
}
