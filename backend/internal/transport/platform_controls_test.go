package transport

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/platform/provisioning"
	"hi-ve/backend/internal/realtime"
	"hi-ve/backend/internal/settings"
)

func TestPlatformControlsPostgres(t *testing.T) {
	db := disposableDatabase(t)
	runtimeDB := businessRuntime(t, db)
	ctx := t.Context()
	tenant, owner, session := ownerSessionFixture(t, db, "control-owner")
	_, candidate, _ := ownerSessionFixture(t, db, "control-candidate")
	admin := entitlementAdminFixture(t, db, "control-admin")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, params ...any) { t.Helper(); _, err := db.Exec(ctx, q, params...); must(err) }
	id := func(q string, params ...any) string {
		t.Helper()
		var out string
		must(db.QueryRow(ctx, q, params...).Scan(&out))
		return out
	}
	adminID := id(`SELECT principal_id FROM hive.platform_sessions WHERE digest=$1`, identity.Digest(admin.Value))
	var sent atomic.Int32
	push := realtime.PushConfig{Public: "fixture-key", Send: func(_ context.Context, _ realtime.PushSubscription, raw []byte) (int, error) {
		var payload map[string]string
		if err := json.Unmarshal(raw, &payload); err != nil || len(payload) != 2 || payload["type"] != "sale.received" || strings.Contains(string(raw), tenant) {
			t.Errorf("unexpected payload %q", raw)
		}
		sent.Add(1)
		return 201, nil
	}}
	rt := realtime.Service{DB: runtimeDB, Entitlements: entitlements.Service{DB: runtimeDB}, Push: push}
	a := API{AdminOrigin: adminOrigin, AppOrigin: appOrigin, Global: settings.Global{DB: runtimeDB}, PushSchema: true, Provisioning: provisioning.Service{DB: runtimeDB},
		Identity: identity.Store{DB: runtimeDB}, Devices: devices.Service{DB: runtimeDB, TenantEpochSchema: true}, Realtime: rt}
	h := a.Handler()
	const settingsPath = "/api/v1/admin/settings/notifications.web_push_enabled"
	preEpoch := a
	preEpoch.Devices.TenantEpochSchema = false
	preEpochLifecycle := "/api/v1/admin/tenants/" + tenant + "/lifecycle/suspend"
	expectStatus(t, accessRequest(preEpoch.Handler(), "POST", preEpochLifecycle, adminOrigin, "", nil, admin), 405)
	exec(`INSERT INTO hive.platform_permissions SELECT $1,p FROM unnest(ARRAY['platform.settings.read','platform.tenants.suspend',
   'platform.tenants.reactivate','platform.tenants.close','platform.tenants.owner.transfer']) p`, adminID)
	put := func(key string, enabled bool, rev int) *httptest.ResponseRecorder {
		return accessRequest(h, "PUT", settingsPath, adminOrigin, key, map[string]any{"enabled": enabled, "revision": rev}, admin)
	}
	expectStatus(t, accessRequest(h, "GET", settingsPath, adminOrigin, "", nil, session), 401)
	initial := accessRequest(h, "GET", settingsPath, adminOrigin, "", nil, admin)
	expectStatus(t, initial, 200)
	if !strings.Contains(initial.Body.String(), `"enabled":false`) {
		t.Fatal(initial.Body.String())
	}
	expectStatus(t, put("settings-control-a", true, 0), 403)
	exec(`INSERT INTO hive.platform_permissions VALUES($1,'platform.settings.update')`, adminID)
	concurrent := make(chan *httptest.ResponseRecorder, 2)
	for _, key := range []string{"settings-control-a", "settings-control-b"} {
		go func() { concurrent <- put(key, true, 0) }()
	}
	x, y := <-concurrent, <-concurrent
	if !(x.Code == 200 && y.Code == 409 || x.Code == 409 && y.Code == 200) {
		t.Fatal(x.Code, y.Code, x.Body.String(), y.Body.String())
	}
	if n := id(`SELECT count(*)::text FROM hive.global_setting_audit WHERE actor_id=$1`, adminID); n != "1" {
		t.Fatal("setting replay", n)
	}
	exec(`INSERT INTO hive.tenant_entitlements
		SELECT $1,capability,now()-interval '1 hour',now()+interval '1 hour','fixture',NULL,1
		FROM unnest(ARRAY['realtime.notifications','realtime.events']) capability`, tenant)
	pushPath := "/api/v1/app/tenants/" + tenant + "/realtime/push"
	enc := base64.RawURLEncoding.EncodeToString
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	must(err)
	sub := realtime.PushSubscription{Endpoint: "https://fcm.googleapis.com/fcm/send/disposable-fixture"}
	sub.Keys.P256dh = enc(priv.PublicKey().Bytes())
	sub.Keys.Auth = enc(make([]byte, 16))
	unsafe := sub
	unsafe.Endpoint = "https://127.0.0.1/private"
	expectStatus(t, accessRequest(h, "PUT", pushPath, appOrigin, "", unsafe, session), 400)
	expectStatus(t, accessRequest(h, "PUT", pushPath, appOrigin, "", sub, admin), 401)
	expectStatus(t, accessRequest(h, "PUT", pushPath, appOrigin, "", sub, session), 204)
	outlet := id(`INSERT INTO hive.locations(tenant_id,code,name,kind) VALUES($1,'push','Push','outlet') RETURNING id`, tenant)
	checkout := func() {
		exec(`INSERT INTO hive.sales_orders(tenant_id,id,outlet_id,actor_id,fingerprint,total,method,tendered,change,outlet_name)
       VALUES($1,gen_random_uuid(),$2,$3,sha256('fixture'::bytea),100,'cash',100,0,'Push')`, tenant, outlet, owner)
	}
	checkout()
	for range 2 {
		go func() {
			_, err := rt.DeliverPush(ctx)
			if err != nil {
				t.Error(err)
			}
			concurrent <- nil
		}()
	}
	<-concurrent
	<-concurrent
	if sent.Load() != 1 {
		t.Fatal("bad push dedup", sent.Load())
	}
	checkout()
	ambiguous := rt
	ambiguous.Push.Send = func(context.Context, realtime.PushSubscription, []byte) (int, error) {
		return 0, errors.New("fixture: provider completion unknown")
	}
	if claimed, err := ambiguous.DeliverPush(ctx); !claimed || err == nil {
		t.Fatalf("ambiguous delivery: claimed=%v, err=%v", claimed, err)
	}
	if claimed, err := rt.DeliverPush(ctx); claimed || err != nil {
		t.Fatalf("ambiguous delivery retried: claimed=%v, err=%v", claimed, err)
	}
	fallback, err := rt.Feed(ctx, tenant, session.Value, 1)
	must(err)
	if len(fallback.Events) != 1 || fallback.Events[0].Kind != "sale.received" {
		t.Fatalf("in-app replay lost the event: %+v", fallback)
	}
	checkout()
	exec(`UPDATE hive.tenant_entitlements SET revoked_at=clock_timestamp(),revision=2 WHERE tenant_id=$1 AND capability_id='realtime.notifications'`, tenant)
	_, err = rt.DeliverPush(ctx)
	must(err)
	if sent.Load() != 1 {
		t.Fatal("revoked push delivered")
	}
	logout := "/api/v1/app/tenants/" + tenant + "/logout"
	expectStatus(t, accessRequest(h, "POST", logout, appOrigin, "", nil, session), 204)
	if n := id(`SELECT count(*)::text FROM hive.web_push_subscriptions WHERE tenant_id=$1`, tenant); n != "0" {
		t.Fatal("logout did not revoke", n)
	}
	// Keep financial history and test the irreversible close and owner fence.
	call := func(action string, body any) *httptest.ResponseRecorder {
		return accessRequest(h, "POST", "/api/v1/admin/tenants/"+tenant+"/lifecycle/"+action, adminOrigin, "", body, admin)
	}
	transfer := map[string]any{"case_id": "case-owner-1", "reason": "manual identity evidence verified", "revision": 0,
		"expected_owner_id": owner, "candidate_principal_id": candidate, "evidence_reference": "ref-001"}
	missing := map[string]any{}
	for k, v := range transfer {
		missing[k] = v
	}
	missing["candidate_principal_id"] = id("SELECT gen_random_uuid()")
	expectStatus(t, call("transfer", missing), 409)
	exec(`INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at,auth_method) VALUES($1,$2,$3,now()+interval '1 hour','google')`, identity.Digest("owner-before-transfer"), tenant, owner)
	exec(`ALTER TABLE hive.tenant_administration_audit ADD CONSTRAINT control_test_fault CHECK(case_id<>'case-owner-1') NOT VALID`)
	expectStatus(t, call("transfer", transfer), 503)
	if n := id(`SELECT count(*)::text FROM hive.tenant_sessions WHERE tenant_id=$1`, tenant); n != "1" {
		t.Fatal("audit rollback lost live session", n)
	}
	if current := id(`SELECT principal_id FROM hive.tenant_memberships WHERE tenant_id=$1 AND active AND role='primary_owner'`, tenant); current != owner {
		t.Fatal("audit rollback changed owner", current)
	}
	exec(`ALTER TABLE hive.tenant_administration_audit DROP CONSTRAINT control_test_fault`)

	t.Run("administration waits for Owner before locking staff", func(t *testing.T) {
		const staff = "00000000-0000-4000-8000-000000000055"
		exec(`INSERT INTO hive.identity_principals VALUES($1)`, staff)
		exec(`INSERT INTO hive.tenant_memberships(tenant_id,principal_id,role) VALUES($1,$2,'staff')`, tenant, staff)
		limited, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ownerTx, err := db.Begin(limited)
		must(err)
		defer ownerTx.Rollback(ctx)
		var ownerPID int32
		must(ownerTx.QueryRow(limited, `SELECT pg_backend_pid()`).Scan(&ownerPID))
		_, err = ownerTx.Exec(limited, `SELECT principal_id FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$2 FOR SHARE`, tenant, owner)
		must(err)
		locked := make(chan error, 1)
		go func() {
			locked <- pgx.BeginFunc(limited, db, func(tx pgx.Tx) error {
				return identity.LockTenantMemberships(limited, tx, tenant)
			})
		}()
		for {
			var waiting bool
			must(db.QueryRow(limited, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, ownerPID).Scan(&waiting))
			if waiting {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		// The lower UUID staff row must still be available to the Owner.
		_, err = ownerTx.Exec(limited, `SELECT principal_id FROM hive.tenant_memberships WHERE tenant_id=$1 AND principal_id=$2 FOR NO KEY UPDATE NOWAIT`, tenant, staff)
		must(err)
		must(ownerTx.Commit(limited))
		must(<-locked)
	})

	competing := map[string]any{}
	for k, v := range transfer {
		competing[k] = v
	}
	competing["case_id"] = "case-owner-2"
	for _, input := range []map[string]any{transfer, competing} {
		go func() { concurrent <- call("transfer", input) }()
	}
	first, second := <-concurrent, <-concurrent
	if !(first.Code == 200 && second.Code == 409 || first.Code == 409 && second.Code == 200) {
		t.Fatalf("concurrent Owner transfer: %d / %d", first.Code, second.Code)
	}
	winner := id(`SELECT case_id FROM hive.tenant_administration_audit WHERE tenant_id=$1 AND action='transfer'`, tenant)
	if winner == "case-owner-2" {
		transfer = competing
	}
	expectStatus(t, call("transfer", transfer), 200)
	if current := id(`SELECT principal_id FROM hive.tenant_memberships WHERE tenant_id=$1 AND active AND role='primary_owner'`, tenant); current != candidate {
		t.Fatal(current)
	}
	if n := id(`SELECT count(*)::text FROM hive.tenant_sessions WHERE tenant_id=$1`, tenant); n != "0" {
		t.Fatal("old sessions survive", n)
	}
	expectStatus(t, call("suspend", map[string]any{"case_id": "case-suspend", "reason": "fixture", "revision": 1}), 200)
	expectStatus(t, call("reactivate", map[string]any{"case_id": "case-restore", "reason": "fixture", "revision": 2}), 200)
	expectStatus(t, call("close", map[string]any{"case_id": "case-close", "reason": "fixture", "revision": 3}), 200)
	expectStatus(t, call("reactivate", map[string]any{"case_id": "case-after-close", "reason": "fixture", "revision": 4}), 409)
	if n := id(`SELECT count(*)::text FROM hive.sales_orders WHERE tenant_id=$1`, tenant); n != "3" {
		t.Fatal("history not preserved", n)
	}
}

func TestReportCalendarWindow(t *testing.T) {
	window, _, err := reportFilter("sales", httptest.NewRequest("GET", "/reports/sales?timezone=America%2FNew_York&date_from=2026-11-01&date_through=2026-11-01", nil))
	if err != nil || window.To.Sub(window.From).Hours() != 25 {
		t.Fatalf("%v %v", window, err)
	}
	for _, tc := range []struct {
		zone, from, through, start, end string
	}{
		{"Africa/Cairo", "2025-04-25", "2025-04-25", "2025-04-24T22:00:00Z", "2025-04-25T21:00:00Z"},
		{"America/Sao_Paulo", "2018-11-04", "2018-11-04", "2018-11-04T03:00:00Z", "2018-11-05T02:00:00Z"},
		{"America/New_York", "2026-08-30", "2026-11-30", "2026-08-30T04:00:00Z", "2026-12-01T05:00:00Z"},
		{"Pacific/Apia", "2011-12-29", "2011-12-29", "2011-12-29T10:00:00Z", "2011-12-30T10:00:00Z"},
	} {
		r := httptest.NewRequest("GET", "/reports/sales?timezone="+tc.zone+"&date_from="+tc.from+"&date_through="+tc.through, nil)
		window, _, err := reportFilter("sales", r)
		if err != nil || window.From.Format(time.RFC3339) != tc.start || window.To.Format(time.RFC3339) != tc.end {
			t.Fatalf("%s: %v %v", tc.zone, window, err)
		}
	}
	for _, q := range []string{
		"timezone=Local&date_from=2026-11-01&date_through=2026-11-01",
		"timezone=Not%2FAZone&date_from=2026-11-01&date_through=2026-11-01",
		"timezone=UTC&date_from=2026-11-01",
		"timezone=UTC&date_from=2026-11-02&date_through=2026-11-01",
		"timezone=Pacific/Apia&date_from=2011-12-30&date_through=2011-12-30",
		"timezone=America/New_York&date_from=2026-08-29&date_through=2026-11-30",
	} {
		if _, _, err := reportFilter("sales", httptest.NewRequest("GET", "/reports/sales?"+q, nil)); err == nil {
			t.Fatal("invalid window", q)
		}
	}
}
