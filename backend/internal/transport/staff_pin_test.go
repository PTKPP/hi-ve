package transport

import (
	"bytes"
	"encoding/json"
	"testing"

	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
)

func TestStaffPINPostgres(t *testing.T) {
	db := disposableDatabase(t)
	const origin = "https://app.example.test"
	tenant, owner, session := ownerSessionFixture(t, db, "staff-pin-owner")
	other, _, foreign := ownerSessionFixture(t, db, "staff-pin-foreign")
	svc := devices.Service{}
	pin, err := identity.NewPIN(db, bytes.Repeat([]byte{1}, 32), 1, bytes.Repeat([]byte{2}, 32), 0, nil, svc.EndMemberContexts)
	if err != nil {
		t.Fatal(err)
	}
	h := API{StaffPIN: pin, AppOrigin: origin, Memberships: identity.MembershipLifecycle{DB: db,
		FingerprintKey: bytes.Repeat([]byte{1}, 32), StaffPINSchema: true, RemoveOperatorAccess: svc.RemoveOperatorAccess}}.Handler()
	base := "/api/v1/app/tenants/" + tenant + "/members/"
	create := func(key, name string) identity.PINStatus {
		t.Helper()
		res := accessRequest(h, "POST", base+"pin-profiles", origin, key, map[string]string{"display_name": name}, session)
		expectStatus(t, res, 201)
		var result identity.PINStatus
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || result.Member == "" || result.Configured {
			t.Fatalf("create: %v %v", result, err)
		}
		return result
	}
	member := create("pin-profile-create", " Staff ").Member
	if replay := create("pin-profile-create", "Staff"); replay.Member != member {
		t.Fatal("replay returned another member")
	}
	expectStatus(t, accessRequest(h, "POST", base+"pin-profiles", origin, "pin-profile-create", map[string]string{"display_name": "Other"}, session), 409)
	expectStatus(t, accessRequest(h, "GET", "/api/v1/app/tenants/"+other+"/members/"+member+"/pin", origin, "", nil, foreign), 404)
	expectStatus(t, accessRequest(h, "GET", base+owner+"/pin", origin, "", nil, session), 404)
	path := base + member + "/pin"
	set := func(value string, rev int, status int) {
		t.Helper()
		res := accessRequest(h, "PUT", path, origin, "", map[string]any{"pin": value, "expected_revision": rev}, session)
		expectStatus(t, res, status)
		if bytes.Contains(res.Body.Bytes(), []byte(value)) {
			t.Fatal("credential in response")
		}
	}
	set("١٢٣٤٥٦", 0, 400)
	set("012345", 0, 200)
	set("654321", 0, 409)
	lifecycle := map[string]any{"revision": 0, "reason_code": "administrative_action"}
	expectStatus(t, accessRequest(h, "POST", base+member+"/deactivate", origin, "pin-deactivation", lifecycle, session), 200)
	lifecycle["revision"] = 1
	expectStatus(t, accessRequest(h, "POST", base+member+"/reactivate", origin, "pin-reactivation", lifecycle, session), 200)
	status := accessRequest(h, "GET", path, origin, "", nil, session)
	expectStatus(t, status, 200)
	var state identity.PINStatus
	if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil || state.Configured || state.Revision != 2 {
		t.Fatalf("unexpected reactivated credential state: %+v %v", state, err)
	}
	set("012344", 2, 200)
	var linked bool
	if err := db.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM hive.google_principals WHERE id=$1)`, member).Scan(&linked); err != nil || linked {
		t.Fatalf("unexpected Google link: %v %v", linked, err)
	}
}
