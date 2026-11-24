package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"hi-ve/backend/internal/realtime"
)

func TestRealtimeDrainAndResume(t *testing.T) {
	drain := make(chan struct{})
	first := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after, ok := feedQuery(r.URL.RawQuery)
		if !ok {
			t.Error("invalid cursor")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		feed := realtime.Feed{Cursor: "42"}
		stop := drain
		if after == 42 {
			stop = nil
			feed.Cursor = "43"
		} else if after != -1 {
			t.Errorf("unexpected resume cursor %d", after)
		}
		streamRealtime(r.Context(), conn, stop, feed, func(context.Context, int64) (realtime.Feed, error) {
			return feed, nil
		})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	for _, query := range []string{"", "?after=42"} {
		conn, _, err := websocket.Dial(ctx, endpoint+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		var feed realtime.Feed
		if err := wsjson.Read(ctx, conn, &feed); err != nil {
			conn.CloseNow()
			t.Fatal(err)
		}
		if first {
			close(drain)
			_, _, err := conn.Read(ctx)
			if feed.Cursor != "42" || websocket.CloseStatus(err) != websocket.StatusGoingAway {
				t.Errorf("expected restart frame and prior cursor, got %q and %v", feed.Cursor, err)
			}
		} else if feed.Cursor != "43" {
			t.Errorf("expected cursor after replay, got %q", feed.Cursor)
		}
		conn.CloseNow()
		first = false
	}
}
