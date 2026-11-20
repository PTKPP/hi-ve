package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hi-ve/backend/internal/identity"
)

func accessRequest(handler http.Handler, method, path, origin, key string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Origin", origin)
	req.Header.Set("Idempotency-Key", key)
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func expectStatus(t *testing.T, res *httptest.ResponseRecorder, status int) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("status %d, want %d: %s", res.Code, status, res.Body.String())
	}
}

func waitForBlocked(t *testing.T, db *pgxpool.Pool, pid uint32) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked uint32
		err := db.QueryRow(ctx, `SELECT COALESCE((SELECT pid FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)) LIMIT 1),0)`, pid).Scan(&blocked)
		if err != nil {
			t.Fatalf("waiting for transaction ordering: %v", err)
		}
		if blocked != 0 {
			return blocked
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("second transaction never waited on the targeted lock")
		}
	}
}

// ownerSessionFixture creates a tenant, its Owner and a scoped session on the disposable database.
func ownerSessionFixture(t *testing.T, db *pgxpool.Pool, label string) (tenant, owner string, cookie *http.Cookie) {
	t.Helper()
	cookie = &http.Cookie{Name: tenantCookie, Value: label}
	err := db.QueryRow(t.Context(), `WITH tenant AS (
		INSERT INTO hive.tenants(display_name,status) VALUES ($1,'active') RETURNING id
	), candidate AS (
		INSERT INTO hive.owner_candidates(tenant_id,contact_email)
		SELECT id,'fixture@example.test' FROM tenant RETURNING tenant_id,id
	), principal AS (
		INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com',$1) RETURNING id
	), membership AS (
		INSERT INTO hive.tenant_memberships(tenant_id,principal_id,owner_id,role)
		SELECT tenant_id,principal.id,candidate.id,'primary_owner' FROM candidate CROSS JOIN principal
		RETURNING tenant_id,principal_id
	) INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at)
	SELECT $2,tenant_id,principal_id,clock_timestamp()+interval '1 hour' FROM membership
	RETURNING tenant_id,principal_id`, label, identity.Digest(cookie.Value)).Scan(&tenant, &owner)
	if err != nil {
		t.Fatal(err)
	}
	return tenant, owner, cookie
}
