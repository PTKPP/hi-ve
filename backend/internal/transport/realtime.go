package transport

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func feedPosition(raw string) (int64, bool) {
	if raw == "" {
		return -1, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n >= 0
}

func (a API) realtimeRoutes(mux *http.ServeMux) {
	if a.Realtime.DB == nil {
		return
	}
	const root = "/api/v1/app/tenants/{tenant_id}/realtime"
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		return a.origin(a.AppOrigin, func(w http.ResponseWriter, r *http.Request) {
			if !uuid.MatchString(r.PathValue("tenant_id")) {
				fail(w, 400, "INVALID_REQUEST")
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET "+root+"/events", wrap(func(w http.ResponseWriter, r *http.Request) {
		after, ok := feedQuery(r.URL.RawQuery)
		if !ok {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		f, err := a.Realtime.Feed(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), after)
		if err != nil {
			reportError(w, err)
			return
		}
		respond(w, 200, f)
	}))
	mux.HandleFunc("PUT "+root+"/preferences", wrap(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Sales *bool `json:"sale_notifications"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Sales == nil {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		if err := a.Realtime.Preference(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), *in.Sales); err != nil {
			reportError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET "+root+"/socket", wrap(a.realtimeSocket))
}

func feedQuery(raw string) (int64, bool) {
	q, err := url.ParseQuery(raw)
	if err != nil || len(q) > 0 && (len(q) != 1 || len(q["after"]) != 1) {
		return 0, false
	}
	return feedPosition(q.Get("after"))
}

func (a API) realtimeSocket(w http.ResponseWriter, r *http.Request) {
	after, ok := feedQuery(r.URL.RawQuery)
	if !ok {
		fail(w, 400, "INVALID_REQUEST")
		return
	}
	tenant, secret := r.PathValue("tenant_id"), cookie(r, tenantCookie)
	feed, err := a.Realtime.Feed(r.Context(), tenant, secret, after)
	if err != nil {
		reportError(w, err)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{a.AppOrigin}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := conn.CloseRead(r.Context())
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		write, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = wsjson.Write(write, conn, feed)
		cancel()
		if err != nil {
			return
		}
		after, _ = feedPosition(feed.Cursor)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		feed, err = a.Realtime.Feed(ctx, tenant, secret, after)
		if err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "subscription unavailable")
			return
		}
	}
}
