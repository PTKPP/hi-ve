package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/offline"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/sales"
)

func TestOperatorPINPostgres(t *testing.T) {
	db := disposableDatabase(t)
	ctx := t.Context()
	tenant, ownerID, owner := ownerSessionFixture(t, db, "operator-pin-owner")
	foreignTenant, _, foreign := ownerSessionFixture(t, db, "operator-pin-foreign")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	id := func(q string, args ...any) string {
		t.Helper()
		var result string
		if err := db.QueryRow(ctx, q, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := devices.PublicKey{Curve: "P-256", Type: "EC", X: encode(signer.X.FillBytes(make([]byte, 32))), Y: encode(signer.Y.FillBytes(make([]byte, 32)))}
	makeOutlet := func(code string) string {
		return id(`INSERT INTO hive.locations(tenant_id,code,name,kind) VALUES ($1,$2,$2,'outlet') RETURNING id`, tenant, code)
	}
	outlet, otherOutlet := makeOutlet("pin-one"), makeOutlet("pin-two")
	makeDevice := func(label string) string {
		device := id(`INSERT INTO hive.devices(tenant_id,pairing_id,public_key,thumbprint) VALUES ($1,$2,$3,sha256($2::text::bytea)) RETURNING id`, tenant, label, public)
		exec(`INSERT INTO hive.device_outlets VALUES($1,$2,$3),($1,$2,$4)`, tenant, device, outlet, otherOutlet)
		return device
	}
	device, otherDevice := makeDevice("operator-pin-one"), makeDevice("operator-pin-two")
	role := id(`INSERT INTO hive.tenant_roles(tenant_id,role_key,name) VALUES($1,'operator','Operator') RETURNING id`, tenant)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES ($1,$2,'devices.operators.use')`, tenant, role)
	key := bytes.Repeat([]byte{4}, 32)
	svc := devices.Service{DB: db, CatalogCommands: true, InventoryCommands: true, SalesCommands: true, RateKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner, AuthorizeOperator: identity.AuthorizeDeviceOperator,
		LockOutlets: locations.LockActiveOutlets, PINContextSchema: true}
	newPIN := func() *identity.PIN {
		pin, err := identity.NewPIN(db, key, 1, bytes.Repeat([]byte{5}, 32), 0, nil, svc.EndMemberContexts)
		if err != nil {
			t.Fatal(err)
		}
		return pin
	}
	svc.PIN = newPIN()
	app := API{Devices: svc, StaffPIN: svc.PIN, AppOrigin: appOrigin, LoginHistory: audit.LoginHistory{DB: db, CursorKey: key, AuthorizeOwner: identity.AuthorizeTenantOwner}}
	app.AdminOrigin = adminOrigin
	app.InventoryDB = db
	app.SalesDB = db
	app.Entitlements = entitlements.Service{DB: db, FingerprintKey: key}
	app.Catalog = catalog.Service{DB: db, LockOutlets: locations.LockActiveOutlets, AuthorizeOwner: identity.AuthorizeTenantOwner, CheckCapability: app.Entitlements.CheckWrite}
	app.Offline = offline.New(db, svc, app.Entitlements, app.Catalog, key)
	app.Memberships = identity.MembershipLifecycle{DB: db, FingerprintKey: key, StaffPINSchema: true, RemoveOperatorAccess: svc.RemoveOperatorAccess}
	handler := app.Handler()
	replica := app
	replica.Devices.PIN = newPIN()
	otherHandler := replica.Handler()
	makeMember := func(label, pin string) string {
		profile, err := svc.PIN.CreateProfile(ctx, tenant, owner.Value, label, label)
		if err != nil {
			t.Fatal(err)
		}
		zero := int64(0)
		if _, err = svc.PIN.Set(ctx, tenant, profile.Member, owner.Value, pin, &zero); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO hive.tenant_location_role_assignments(tenant_id,member_id,role_id,location_id)
            VALUES ($1,$2,$3,$4),($1,$2,$3,$5)`, tenant, profile.Member, role, outlet, otherOutlet)
		for _, d := range []string{device, otherDevice} {
			exec(`INSERT INTO hive.device_operator_assignments(tenant_id,device_id,outlet_id,member_id)
               VALUES ($1,$2,$3,$5),($1,$2,$4,$5)`, tenant, d, outlet, otherOutlet, profile.Member)
		}
		return profile.Member
	}
	member := makeMember("pin-first", "012345")
	second := makeMember("pin-second", "098765")
	base := func(device string) string { return "/api/v1/operator/tenants/" + tenant + "/devices/" + device }
	send := func(h http.Handler, method, path, proof string, body any, session *http.Cookie) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Origin", appOrigin)
		req.Header.Set("Device-Proof", proof)
		if method == "PUT" || strings.Contains(path, "/inventory/") || strings.Contains(path, "/sales") {
			req.Header.Set("Idempotency-Key", "staff-catalog-command-01")
		}
		if session != nil {
			req.AddCookie(session)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	sign := func(payload any) string {
		encoded, _ := json.Marshal(payload)
		content := "eyJhbGciOiJFUzI1NiJ9." + encode(encoded)
		hash := sha256.Sum256([]byte(content))
		r, s, err := ecdsa.Sign(rand.Reader, signer, hash[:])
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		return content + "." + encode(sig)
	}
	challenge := func(device string, target devices.OperationTarget) string {
		start := sign(struct {
			devices.OperationTarget
			Purpose string    `json:"purpose"`
			Tenant  string    `json:"tenant_id"`
			Device  string    `json:"device_id"`
			Request string    `json:"request_id"`
			Issued  time.Time `json:"issued_at"`
		}{target, "hive.device.challenge.bootstrap.v1", tenant, device, id("SELECT gen_random_uuid()"), time.Now()})
		res := send(handler, "POST", base(device)+"/challenges", start, target, nil)
		expectStatus(t, res, 201)
		var c devices.OperationChallenge
		if err := json.Unmarshal(res.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return sign(struct {
			devices.OperationChallenge
			devices.OperationTarget
			Purpose string `json:"purpose"`
			Tenant  string `json:"tenant_id"`
			Device  string `json:"device_id"`
		}{c, target, "hive.device.operation.v1", tenant, device})
	}
	loginRequest := func(device, outlet, member, pin string, rev int64) (string, map[string]any) {
		proof := challenge(device, devices.OperationTarget{Operation: "context.login", Outlet: outlet, Member: member, Revision: &rev})
		return proof, map[string]any{"outlet_id": outlet, "member_id": member, "pin": pin, "expected_revision": rev}
	}
	login := func(device, outlet, member, pin string, rev int64, status int) *httptest.ResponseRecorder {
		t.Helper()
		proof, input := loginRequest(device, outlet, member, pin, rev)
		res := send(handler, "POST", base(device)+"/context", proof, input, nil)
		expectStatus(t, res, status)
		return res
	}
	discovery := challenge(device, devices.OperationTarget{Operation: "profiles", Outlet: outlet})
	expectStatus(t, send(handler, "GET", base(device)+"/profiles?outlet_id="+outlet, "bad-proof", nil, nil), 401)
	profiles := send(handler, "GET", base(device)+"/profiles?outlet_id="+outlet, discovery, nil, nil)
	expectStatus(t, profiles, 200)
	if !bytes.Contains(profiles.Body.Bytes(), []byte(member)) {
		t.Fatal("eligible profile missing")
	}
	expectStatus(t, send(handler, "GET", base(device)+"/profiles?outlet_id="+outlet, discovery, nil, nil), 401)

	// Distinct service instances share the member lease and the failed-attempt limit.
	exec(`INSERT INTO hive.member_pin_attempt_state(tenant_id,member_id,attempt_id,lease_until)
        VALUES($1,$2,gen_random_uuid(),clock_timestamp()+interval '5 seconds')`, tenant, member)
	blockedProof, blockedInput := loginRequest(otherDevice, outlet, member, "000000", 0)
	expectStatus(t, send(otherHandler, "POST", base(otherDevice)+"/context", blockedProof, blockedInput, nil), 429)
	exec(`UPDATE hive.member_pin_attempt_state SET lease_until=clock_timestamp() WHERE tenant_id=$1 AND member_id=$2`, tenant, member)
	exec(`ALTER TABLE hive.operator_login_history ADD CONSTRAINT pin_test_denial_fault CHECK(outcome<>'denied') NOT VALID`)
	login(device, outlet, member, "000000", 0, 401)
	exec(`ALTER TABLE hive.operator_login_history DROP CONSTRAINT pin_test_denial_fault`)
	if failures := id("SELECT failures::text FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2", tenant, member); failures != "1" {
		t.Fatal("failed denial audit lost the PIN counter")
	}
	firstProof, firstInput := loginRequest(device, outlet, member, "000000", 0)
	secondProof, secondInput := loginRequest(otherDevice, outlet, member, "000000", 0)
	results := make(chan int, 2)
	go func() { results <- send(handler, "POST", base(device)+"/context", firstProof, firstInput, nil).Code }()
	go func() {
		results <- send(otherHandler, "POST", base(otherDevice)+"/context", secondProof, secondInput, nil).Code
	}()
	for range 2 {
		if code := <-results; code != 401 && code != 429 {
			t.Fatalf("parallel attempt status %d", code)
		}
	}
	prior := id("SELECT failures::text FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2", tenant, member)
	initial, err := strconv.Atoi(prior)
	if err != nil || initial < 2 || initial > 3 {
		t.Fatalf("unexpected shared counter %q", prior)
	}
	for i := initial; i < 5; i++ {
		login(device, outlet, member, "000000", 0, 401)
	}
	// Saturating the device bucket must not debit the shared peer bucket.
	peerCount := `SELECT sum(cardinality(attempts))::text FROM hive.device_pairing_limits WHERE scope='pin_peer'`
	beforePeer := id(peerCount)
	exec(`UPDATE hive.device_pairing_limits SET attempts=array_fill(clock_timestamp(),ARRAY[30]) WHERE scope='pin_device'`)
	login(otherDevice, outlet, member, "012345", 0, 429)
	if afterPeer := id(peerCount); afterPeer != beforePeer {
		t.Fatal("exhausted device debited shared peer budget")
	}
	exec(`UPDATE hive.device_pairing_limits SET attempts='{}' WHERE scope='pin_device'`)
	until := id("SELECT locked_until::text FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2", tenant, member)
	login(otherDevice, outlet, member, "012345", 0, 401)
	if now := id("SELECT locked_until::text FROM hive.member_pin_attempt_state WHERE tenant_id=$1 AND member_id=$2", tenant, member); now != until {
		t.Fatal("lockout was extended")
	}
	exec("UPDATE hive.member_pin_attempt_state SET locked_until=clock_timestamp() WHERE tenant_id=$1 AND member_id=$2", tenant, member)
	replayProof, replayInput := loginRequest(device, outlet, member, "012345", 0)
	res := send(handler, "POST", base(device)+"/context", replayProof, replayInput, nil)
	expectStatus(t, res, 201)
	replay := send(otherHandler, "POST", base(device)+"/context", replayProof, replayInput, nil)
	if (replay.Code != 401 && replay.Code != 409) || len(replay.Result().Cookies()) != 0 {
		t.Fatal("login proof replay issued a session")
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != operatorCookie || cookies[0].SameSite != http.SameSiteStrictMode || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("unexpected operator cookie")
	}
	var previous devices.OperationalContext
	if err := json.Unmarshal(res.Body.Bytes(), &previous); err != nil || previous.Member != member {
		t.Fatal("unexpected context", err)
	}
	login(device, otherOutlet, second, "098765", 1, 401)
	exec(`ALTER TABLE hive.operator_login_history ADD CONSTRAINT pin_test_audit_fault CHECK (outcome<>'success') NOT VALID`)
	failed := login(device, outlet, second, "098765", 1, 503)
	if len(failed.Result().Cookies()) != 0 {
		t.Fatal("failed audit set a cookie")
	}
	exec(`ALTER TABLE hive.operator_login_history DROP CONSTRAINT pin_test_audit_fault`)
	oldRead := func(status int) {
		rev := previous.Revision
		proof := challenge(device, devices.OperationTarget{Operation: "context.read", ContextID: previous.ID, Revision: &rev})
		expectStatus(t, send(handler, "GET", base(device)+"/context", proof, nil, cookies[0]), status)
	}
	oldRead(200)
	// Resolve only the device's live outlet. A device assignment alone does not grant price access.
	product := id(`INSERT INTO hive.catalog_products(tenant_id,id,name,category,revision) VALUES($1,gen_random_uuid(),'A','B',1) RETURNING id`, tenant)
	sku := id(`INSERT INTO hive.catalog_skus(tenant_id,id,product_id,code,variant,unit,active,batch_tracked,revision)
        VALUES($1,gen_random_uuid(),$2,'curah','Curah','g',true,true,1) RETURNING id`, tenant, product)
	exec(`INSERT INTO hive.catalog_prices(tenant_id,sku_id,outlet_id,revision,amount,actor_id)
        SELECT $1::uuid,$2::uuid,v.outlet,1,v.amount,m.principal_id FROM hive.tenant_memberships m CROSS JOIN
        (VALUES ($3::uuid,100::numeric),($4::uuid,200::numeric)) v(outlet,amount) WHERE m.tenant_id=$1 AND m.role='primary_owner'`, tenant, sku, outlet, otherOutlet)
	pricePath := base(device) + "/catalog/skus/" + sku + "/price"
	readPrice := func(path string) *httptest.ResponseRecorder {
		rev := previous.Revision
		proof := challenge(device, devices.OperationTarget{Operation: "context.read", ContextID: previous.ID, Revision: &rev})
		return send(handler, "GET", path, proof, nil, cookies[0])
	}
	pricingAdmin := entitlementAdminFixture(t, db, "operator-pricing-admin")
	grantPath := "/api/v1/admin/tenants/" + tenant + "/entitlements/catalog.pricing"
	pricingNow := time.Now().UTC()
	expectStatus(t, accessRequest(handler, "PUT", grantPath, adminOrigin, "operator-pricing-grant",
		pricingGrant(0, pricingNow.Add(-time.Hour), pricingNow.Add(time.Hour)), pricingAdmin), 200)
	expectStatus(t, readPrice(pricePath), 403)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES($1,$2,'catalog.pricing.read')`, tenant, role)
	expectStatus(t, readPrice(pricePath+"?outlet_id="+otherOutlet), 400)
	priced := readPrice(pricePath)
	expectStatus(t, priced, 200)
	var fields map[string]any
	if err := json.Unmarshal(priced.Body.Bytes(), &fields); err != nil || len(fields) != 4 || fields["amount"] != "100" || fields["price_id"] == nil || fields["revision"] != float64(1) || fields["updated_at"] == nil {
		t.Fatal("unexpected operator price projection")
	}
	expectStatus(t, accessRequest(handler, "POST", grantPath+"/revoke", adminOrigin, "operator-pricing-revoke",
		map[string]any{"revision": 1, "reason_code": "administrative_revocation"}, pricingAdmin), 200)
	expectStatus(t, readPrice(pricePath), 403)
	expectStatus(t, accessRequest(handler, "PUT", grantPath, adminOrigin, "operator-pricing-regrant",
		pricingGrant(2, pricingNow.Add(-time.Hour), pricingNow.Add(time.Hour)), pricingAdmin), 200)
	exec(`DELETE FROM hive.tenant_role_location_actions WHERE tenant_id=$1 AND role_id=$2 AND action='catalog.pricing.read'`, tenant, role)
	expectStatus(t, readPrice(pricePath), 403)

	// Management requires a tenant role as well as the live operational context.
	staffPath := base(device) + "/catalog/products/" + id("SELECT gen_random_uuid()")
	body := map[string]any{"revision": 0, "name": "Madu staf", "category": "Pangan"}
	raw, _ := json.Marshal(body)
	digest := commandDigest("PUT", staffPath, "staff-catalog-command-01", raw)
	command := func() string {
		return challenge(device, devices.OperationTarget{Operation: "catalog.products.write", ContextID: previous.ID, Revision: &previous.Revision, CommandDigest: digest})
	}
	expectStatus(t, send(handler, "PUT", staffPath, command(), body, cookies[0]), 403)
	exec(`INSERT INTO hive.tenant_role_actions VALUES($1,$2,'catalog.products.manage')`, tenant, role)
	exec(`INSERT INTO hive.tenant_role_assignments VALUES($1,$2,$3)`, tenant, member, role)
	expectStatus(t, send(handler, "PUT", staffPath, command(), body, cookies[0]), 403)
	exec(`INSERT INTO hive.tenant_entitlements VALUES($1,'catalog.products',now(),now()+interval '1 day','fixture',NULL,1)`, tenant)
	appPricePath := "/api/v1/app/tenants/" + tenant + "/catalog/skus/" + sku + "/price"
	expectStatus(t, accessRequest(handler, "PUT", appPricePath, appOrigin, "operator-price-write",
		map[string]any{"revision": 0, "amount": "99"}, cookies[0]), 401)
	signed := command()
	expectStatus(t, send(handler, "PUT", staffPath, signed, map[string]any{"revision": 0, "name": "Lain", "category": "Pangan"}, cookies[0]), 401)
	expectStatus(t, send(handler, "PUT", staffPath, signed, body, cookies[0]), 200)
	expectStatus(t, send(handler, "PUT", staffPath, signed, body, cookies[0]), 401)
	expectStatus(t, send(handler, "PUT", staffPath, command(), body, cookies[0]), 200)
	exec(`DELETE FROM hive.tenant_role_assignments WHERE tenant_id=$1 AND principal_id=$2 AND role_id=$3`, tenant, member, role)
	expectStatus(t, send(handler, "PUT", staffPath, command(), body, cookies[0]), 403)

	// Approval uses a live location grant, separate from the operator session.
	inventoryPath := base(device) + "/locations/" + outlet + "/inventory/adjustments/"
	adjustmentID := id("SELECT gen_random_uuid()")
	ownAdjustmentID := id("SELECT gen_random_uuid()")
	ownerAdjustment := "/api/v1/app/tenants/" + tenant + "/locations/" + outlet + "/inventory/adjustments/" + adjustmentID
	batch := id(`INSERT INTO hive.inventory_batches(tenant_id,id,sku_id,lot_code,origin,received_on)
        VALUES($1,gen_random_uuid(),$2,'fixture','fixture',current_date) RETURNING id`, tenant, sku)
	adjustment := inventory.Adjustment{Item: inventory.Measure{SKU: sku, Batch: batch, Quantity: "1", Unit: "g"}, Reason: "verified gain"}
	exec(`INSERT INTO hive.tenant_entitlements VALUES($1,'inventory.adjustment',now(),now()+interval '1 day','fixture',NULL,1)`, tenant)
	expectStatus(t, accessRequest(handler, "PUT", ownerAdjustment, appOrigin, "", adjustment, owner), 204)
	inventoryCall := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		digest := commandDigest(method, path, "staff-catalog-command-01", raw)
		proof := challenge(device, devices.OperationTarget{Operation: "inventory.write", ContextID: previous.ID, Revision: &previous.Revision, CommandDigest: digest})
		return send(handler, method, path, proof, body, cookies[0])
	}
	reason := map[string]string{"reason": "checked adjustment"}
	expectStatus(t, inventoryCall("POST", inventoryPath+adjustmentID+"/approve", reason), 403)
	approvalRole := id(`INSERT INTO hive.tenant_roles(tenant_id,role_key,name) VALUES($1,'inventory-approver','Inventory approver') RETURNING id`, tenant)
	rbac := identity.RBAC{DB: db, FingerprintKey: key, InventoryActions: true, LockOutlets: locations.LockActiveOutlets,
		LockInventoryLocations: locations.LockActiveInventoryLocations}
	grantRevision := int64(0)
	actions, err := rbac.LocationActions(ctx, tenant, owner.Value, approvalRole, "inventory-actions-grant", &grantRevision,
		[]string{inventory.AdjustmentApprovePermission, inventory.AdjustmentDraftPermission})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := rbac.LocationAssignments(ctx, tenant, owner.Value, member, "inventory-scope-grant", &actions.Revision,
		[]identity.LocationAssignment{{Role: role, Location: outlet}, {Role: role, Location: otherOutlet}, {Role: approvalRole, Location: outlet}})
	if err != nil {
		t.Fatal(err)
	}
	wrongLocation := base(device) + "/locations/" + otherOutlet + "/inventory/adjustments/" + adjustmentID + "/approve"
	expectStatus(t, inventoryCall("POST", wrongLocation, reason), 403)
	expectStatus(t, inventoryCall("PUT", inventoryPath+ownAdjustmentID, adjustment), 204)
	expectStatus(t, inventoryCall("POST", inventoryPath+ownAdjustmentID+"/approve", reason), 403)
	expectStatus(t, inventoryCall("POST", inventoryPath+adjustmentID+"/approve", reason), 204)
	if auditDevice := id(`SELECT device_id FROM hive.inventory_postings WHERE tenant_id=$1 AND kind='adjustment' AND document_id=$2`, tenant, adjustmentID); auditDevice != device {
		t.Fatal("approval device was not recorded")
	}

	expectStatus(t, inventoryCall("POST", inventoryPath+adjustmentID+"/approve", reason), 204)
	if _, err = rbac.LocationActions(ctx, tenant, owner.Value, approvalRole, "inventory-approval-revoke", &assigned.Revision,
		[]string{inventory.AdjustmentDraftPermission}); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, inventoryCall("POST", inventoryPath+adjustmentID+"/approve", reason), 403)

	// Quick transfer requires the special action at both ends and no second approval.
	exec(`INSERT INTO hive.tenant_entitlements VALUES($1,'inventory.transfer',now(),now()+interval '1 day','fixture',NULL,1)`, tenant)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES($1,$2,'inventory.transfer.create'),($1,$3,'inventory.transfer.quick')`, tenant, role, approvalRole)
	transferID := id("SELECT gen_random_uuid()")
	transferPath := base(device) + "/locations/" + outlet + "/inventory/transfers/" + transferID
	transfer := inventory.Transfer{Destination: otherOutlet, Item: adjustment.Item, Quick: true}
	expectStatus(t, inventoryCall("PUT", transferPath, transfer), 204)
	expectStatus(t, inventoryCall("POST", transferPath+"/dispatch", nil), 403)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES($1,$2,'inventory.transfer.quick')`, tenant, role)
	expectStatus(t, inventoryCall("POST", transferPath+"/dispatch", nil), 204)
	expectStatus(t, inventoryCall("POST", transferPath+"/dispatch", nil), 204)
	if count := id(`SELECT count(*)::text FROM hive.inventory_approvals WHERE tenant_id=$1 AND document_id=$2`, tenant, transferID); count != "0" {
		t.Fatal("quick transfer required a second approval")
	}

	// The Sales API uses backend outlet scope and live RBAC even on retries.
	salesPath := base(device) + "/locations/" + outlet + "/sales"
	saleID := id("SELECT gen_random_uuid()")
	salePrice := id(`SELECT id FROM hive.catalog_prices WHERE tenant_id=$1 AND sku_id=$2 AND outlet_id=$3`, tenant, sku, outlet)
	saleBatch := id("SELECT gen_random_uuid()")
	inventoryFixture := inventory.Service{Authorize: func(context.Context, pgx.Tx, inventory.Scope, string, string) error { return nil }}
	receiptID := id("SELECT gen_random_uuid()")
	if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		scope := inventory.Scope{Tenant: tenant, Actor: ownerID}
		draft := inventory.Receiving{Item: inventory.Measure{SKU: sku, Batch: saleBatch, Quantity: "1", Unit: "g"}, Lot: "sale fixture", Origin: "fixture", ReceivedOn: "2026-01-01"}
		if err := inventoryFixture.DraftReceiving(ctx, tx, scope, receiptID, outlet, draft); err != nil {
			return err
		}
		return inventoryFixture.PostReceiving(ctx, tx, scope, receiptID, outlet)
	}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO hive.tenant_entitlements
        SELECT $1,cap,now(),now()+interval '1 day','fixture',NULL,1 FROM unnest(ARRAY['sales.transactions','sales.payments','sales.history','sales.returns','sales.cashier']) cap`, tenant)
	searchPath := salesPath + "/items/search?q=curah"
	expectStatus(t, readPrice(searchPath), 403)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES($1,$2,'sales.cashier.search')`, tenant, role)
	found := readPrice(searchPath)
	expectStatus(t, found, 200)
	var matches []catalog.SearchItem
	if err := json.Unmarshal(found.Body.Bytes(), &matches); err != nil || len(matches) != 1 ||
		matches[0].SKU != sku || !matches[0].BatchTracked {
		t.Fatal("unexpected cashier SKU projection", matches, err)
	}
	tendered := "200"
	saleInput := sales.CheckoutInput{Method: "cash", Tendered: &tendered, Items: []sales.CheckoutItem{
		{Measure: inventory.Measure{SKU: sku, Batch: saleBatch, Quantity: "1", Unit: "g"}, PriceID: salePrice},
	}}
	salesCall := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		digest := commandDigest(method, path, "staff-catalog-command-01", raw)
		proof := challenge(device, devices.OperationTarget{Operation: "sales.write", ContextID: previous.ID, Revision: &previous.Revision, CommandDigest: digest})
		return send(handler, method, path, proof, body, cookies[0])
	}
	expectStatus(t, salesCall("PUT", salesPath+"/"+saleID, saleInput), 403)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES($1,$2,'sales.checkout.post'),($1,$2,'sales.history.read'),
        ($1,$2,'sales.returns.create')`, tenant, approvalRole)
	expectStatus(t, salesCall("PUT", salesPath+"/"+saleID, saleInput), 200)
	ackPath := salesPath + "/" + saleID + "/ack"
	for range 2 {
		res := salesCall("POST", ackPath, saleInput)
		expectStatus(t, res, 200)
		var ack sales.CheckoutACK
		if err := json.Unmarshal(res.Body.Bytes(), &ack); err != nil || ack.ID != saleID || ack.Outlet != outlet || ack.Actor != member ||
			ack.State != "cash_received" || ack.PostedAt.IsZero() {
			t.Fatal("invalid durable checkout ACK", ack, err)
		}
	}
	exec(`UPDATE hive.tenant_entitlements SET expires_at=clock_timestamp() WHERE tenant_id=$1 AND capability_id='sales.payments'`, tenant)
	expectStatus(t, salesCall("POST", ackPath, saleInput), 403)
	exec(`UPDATE hive.tenant_entitlements SET expires_at=clock_timestamp()+interval '1 day' WHERE tenant_id=$1 AND capability_id='sales.payments'`, tenant)
	rawCheckout, _ := json.Marshal(saleInput)
	for _, change := range []struct {
		off, on string
		id      string
	}{
		{"UPDATE hive.tenants SET status='suspended' WHERE id=$1", "UPDATE hive.tenants SET status='active' WHERE id=$1", tenant},
		{"UPDATE hive.devices SET revoked_at=clock_timestamp() WHERE id=$1", "UPDATE hive.devices SET revoked_at=NULL WHERE id=$1", device},
	} {
		digest := commandDigest("POST", ackPath, "staff-catalog-command-01", rawCheckout)
		ackProof := challenge(device, devices.OperationTarget{Operation: "sales.write", ContextID: previous.ID, Revision: &previous.Revision, CommandDigest: digest})
		exec(change.off, change.id)
		expectStatus(t, send(handler, "POST", ackPath, ackProof, saleInput, cookies[0]), 401)
		exec(change.on, change.id)
	}

	altered := saleInput
	wrongCash := "201"
	altered.Tendered = &wrongCash
	expectStatus(t, salesCall("POST", ackPath, altered), 409)
	missingSale := id("SELECT gen_random_uuid()")
	expectStatus(t, salesCall("POST", salesPath+"/"+missingSale+"/ack", saleInput), 404)
	if count := id(`SELECT count(*)::text FROM hive.sales_orders WHERE tenant_id=$1 AND id IN ($2,$3)`, tenant, saleID, missingSale); count != "1" {
		t.Fatal("ACK lookup mutated sales", count)
	}

	anotherSalesPath := base(device) + "/locations/" + otherOutlet + "/sales"
	expectStatus(t, salesCall("PUT", anotherSalesPath+"/"+saleID, saleInput), 403)
	expectStatus(t, salesCall("PUT", salesPath+"/"+saleID, saleInput), 200)
	returnID := id("SELECT gen_random_uuid()")
	returnPath := salesPath + "/" + saleID + "/returns/" + returnID
	saleable := false
	expectStatus(t, salesCall("PUT", returnPath, sales.ReversalInput{Reason: "returned damaged",
		Items: []sales.ReturnItem{{Line: 1, Quantity: "1", Saleable: &saleable}}}), 200)
	expectStatus(t, salesCall("POST", returnPath+"/approve", reason), 403)
	exec(`INSERT INTO hive.tenant_role_location_actions VALUES($1,$2,'sales.returns.approve')`, tenant, approvalRole)
	expectStatus(t, salesCall("POST", returnPath+"/approve", reason), 403)
	ownerReturnPath := "/api/v1/app/tenants/" + tenant + "/locations/" + outlet + "/sales/" + saleID + "/returns/" + returnID + "/approve"
	expectStatus(t, accessRequest(handler, "POST", ownerReturnPath, appOrigin, "", reason, owner), 200)
	expectStatus(t, accessRequest(handler, "POST", ownerReturnPath, appOrigin, "", reason, foreign), 401)
	t.Run("offline journal", func(t *testing.T) {
		offlinePath := base(device) + "/offline"
		ownerPath := "/api/v1/app/tenants/" + tenant + "/offline"
		rev := previous.Revision
		issue := func() *httptest.ResponseRecorder {
			proof := challenge(device, devices.OperationTarget{Operation: "context.read", ContextID: previous.ID, Revision: &rev})
			return send(handler, "POST", offlinePath+"/lease", proof, nil, cookies[0])
		}
		response := issue()
		expectStatus(t, response, 200)
		var signed offline.SignedLease
		if err := json.Unmarshal(response.Body.Bytes(), &signed); err != nil {
			t.Fatal(err)
		}
		pub, _ := base64.RawURLEncoding.DecodeString(signed.PublicKey)
		sig, _ := base64.RawURLEncoding.DecodeString(signed.Signature)
		if !ed25519.Verify(pub, []byte("hive.offline.lease.v1."+signed.Payload), sig) {
			t.Fatal("invalid server lease signature")
		}
		var lease offline.Lease
		decoded, _ := base64.RawURLEncoding.DecodeString(signed.Payload)
		if err := json.Unmarshal(decoded, &lease); err != nil || lease.Operator != member || lease.Device != device || lease.Outlet != outlet ||
			lease.ContextRevision != rev || lease.Expires.Sub(lease.Issued) > 24*time.Hour {
			t.Fatal("bad lease scope", lease, err)
		}
		again := issue()
		expectStatus(t, again, 200)
		var repeated offline.SignedLease
		if err := json.Unmarshal(again.Body.Bytes(), &repeated); err != nil || signed.Payload != repeated.Payload {
			t.Fatal("retry extended lease", err)
		}
		prefix := offlinePath + "/leases/" + lease.ID
		signedFor := func(kind, eachID string, body any) string {
			raw, _ := json.Marshal(body)
			digest := sha256.Sum256(raw)
			return sign(map[string]string{"purpose": "hive.offline." + kind + ".v1", "tenant_id": tenant, "device_id": device,
				"lease_id": lease.ID, "id": eachID, "digest": hex.EncodeToString(digest[:])})
		}
		pathFor := func(sale string) string { return prefix + "/sales/" + sale }
		post := func(sale string, in offline.SaleInput) *httptest.ResponseRecorder {
			return send(handler, "PUT", pathFor(sale), signedFor("sale", sale, in), in, nil)
		}
		newBatch := id("SELECT gen_random_uuid()")
		recv := id("SELECT gen_random_uuid()")
		if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			scope := inventory.Scope{Tenant: tenant, Actor: ownerID}
			draft := inventory.Receiving{Item: inventory.Measure{SKU: sku, Batch: newBatch, Quantity: "3", Unit: "g"}, Lot: "offline test", Origin: "fixture", ReceivedOn: "2026-01-01"}
			if err := inventoryFixture.DraftReceiving(ctx, tx, scope, recv, outlet, draft); err != nil {
				return err
			}
			return inventoryFixture.PostReceiving(ctx, tx, scope, recv, outlet)
		}); err != nil {
			t.Fatal(err)
		}
		input := saleInput
		input.Items = append([]sales.CheckoutItem(nil), saleInput.Items...)
		input.Items[0].Batch = newBatch
		packet := offline.SaleInput{Sequence: 1, Occurred: time.Now().UTC(), Checkout: input, Quotes: []string{"100"}}
		queued := id("SELECT gen_random_uuid()")
		proof := signedFor("sale", queued, packet)
		expectStatus(t, send(handler, "PUT", pathFor(queued), "", packet, nil), 401)
		expectStatus(t, send(handler, "PUT", strings.Replace(pathFor(queued), tenant, foreignTenant, 1), proof, packet, nil), 401)
		concurrent := make(chan *httptest.ResponseRecorder, 2)
		for range 2 {
			go func() { concurrent <- send(handler, "PUT", pathFor(queued), proof, packet, nil) }()
		}
		for range 2 {
			result := <-concurrent
			expectStatus(t, result, 200)
			var ack offline.Receipt
			if err := json.Unmarshal(result.Body.Bytes(), &ack); err != nil || ack.State != "posted" || ack.Received.Before(packet.Occurred) {
				t.Fatal(result.Body.String(), err)
			}
		}
		if n := id(`SELECT count(*)::text FROM hive.inventory_postings WHERE tenant_id=$1 AND kind='sale' AND document_id=$2`, tenant, queued); n != "1" {
			t.Fatal("offline replay duplicated ledger posting", n)
		}

		expectStatus(t, salesCall("PUT", salesPath+"/"+queued, input), 409)
		changed := packet
		changed.Quotes = []string{"99"}
		expectStatus(t, post(queued, changed), 409)
		shortage := id("SELECT gen_random_uuid()")
		shortageBody := packet
		shortageBody.Sequence = 2
		shortageBody.Checkout.Items = append([]sales.CheckoutItem(nil), packet.Checkout.Items...)
		shortageBody.Checkout.Items[0].Quantity = "3"
		moreCash := "400"
		shortageBody.Checkout.Tendered = &moreCash
		denied := post(shortage, shortageBody)
		expectStatus(t, denied, 200)
		if !strings.Contains(denied.Body.String(), "stock_shortage") {
			t.Fatal(denied.Body.String())
		}
		revoked := id("SELECT gen_random_uuid()")
		next := packet
		next.Sequence = 3
		exec(`UPDATE hive.devices SET revoked_at=clock_timestamp() WHERE id=$1`, device)
		res := post(revoked, next)
		expectStatus(t, res, 200)
		if !strings.Contains(res.Body.String(), "device_revoked") {
			t.Fatal(res.Body.String())
		}
		exec(`UPDATE hive.devices SET revoked_at=NULL WHERE id=$1`, device)

		expired := id("SELECT gen_random_uuid()")
		next.Sequence = 5
		next.Occurred = lease.Expires
		res = post(expired, next)
		expectStatus(t, res, 200)
		if !strings.Contains(res.Body.String(), "lease_invalid") {
			t.Fatal(res.Body.String())
		}
		next.Sequence = 6
		next.Occurred = packet.Occurred
		rollback := id("SELECT gen_random_uuid()")
		exec(`ALTER TABLE hive.offline_sales ADD CONSTRAINT offline_test_fault CHECK (id<>'` + rollback + `') NOT VALID`)
		expectStatus(t, post(rollback, next), 503)
		if n := id(`SELECT count(*)::text FROM hive.sales_orders WHERE tenant_id=$1 AND id=$2`, tenant, rollback); n != "0" {
			t.Fatal("offline rollback left sale", n)
		}
		exec(`ALTER TABLE hive.offline_sales DROP CONSTRAINT offline_test_fault`)
		expectStatus(t, post(rollback, next), 200)
		suspended := id("SELECT gen_random_uuid()")
		next.Sequence = 4
		exec(`UPDATE hive.tenants SET status='suspended' WHERE id=$1`, tenant)
		res = post(suspended, next)
		expectStatus(t, res, 200)
		if !strings.Contains(res.Body.String(), "tenant_suspended") {
			t.Fatal(res.Body.String())
		}
		exec(`UPDATE hive.tenants SET status='active' WHERE id=$1`, tenant)
		auditID := id("SELECT gen_random_uuid()")
		attempt := offline.LoginInput{Occurred: packet.Occurred, Outcome: "success"}
		logPath := prefix + "/logins/" + auditID
		logProof := signedFor("login", auditID, attempt)
		exec(`ALTER TABLE hive.operator_login_history ADD CONSTRAINT offline_test_fault CHECK (attempt_id<>'` + auditID + `') NOT VALID`)
		expectStatus(t, send(handler, "PUT", logPath, logProof, attempt, nil), 503)
		exec(`ALTER TABLE hive.operator_login_history DROP CONSTRAINT offline_test_fault`)
		for range 2 {
			expectStatus(t, send(handler, "PUT", logPath, logProof, attempt, nil), 200)
		}
		if n := id(`SELECT count(*)::text FROM hive.operator_login_history WHERE tenant_id=$1 AND attempt_id=$2 AND offline_occurred_at IS NOT NULL`, tenant, auditID); n != "1" {
			t.Fatal("login history dedup", n)
		}
		expectStatus(t, accessRequest(handler, "GET", ownerPath+"/review", appOrigin, "", nil, foreign), 401)
		expectStatus(t, accessRequest(handler, "GET", ownerPath+"/review", appOrigin, "", nil, owner), 200)
		end := offline.SealInput{Count: 6}
		expectStatus(t, send(handler, "PUT", prefix+"/seal", signedFor("seal", lease.ID, end), end, nil), 204)
		switchPath := ownerPath + "/devices/" + device + "/outlet"
		expectStatus(t, accessRequest(handler, "PUT", switchPath, appOrigin, "", map[string]any{"outlet_id": otherOutlet, "expected_revision": rev}, owner), 409)
		closePath := ownerPath + "/leases/" + lease.ID + "/close"
		requestClose := offline.CloseInput{Reason: "Review uji"}
		expectStatus(t, accessRequest(handler, "PUT", closePath, appOrigin, "", requestClose, owner), 409)
		refund := offline.Resolution{Decision: "manual_refund_recorded", Reason: "Pelanggan dikembalikan tunai", Reference: "refund-test", Refund: "100.00"}
		excess := refund
		excess.Refund = "101.00"
		expectStatus(t, accessRequest(handler, "PUT", ownerPath+"/sales/"+revoked+"/resolution", appOrigin, "", excess, owner), 409)
		for _, sale := range []string{shortage, revoked, suspended, expired} {
			resolution := ownerPath + "/sales/" + sale + "/resolution"
			expectStatus(t, accessRequest(handler, "PUT", resolution, appOrigin, "", refund, owner), 204)
			expectStatus(t, accessRequest(handler, "PUT", resolution, appOrigin, "", refund, owner), 204)
		}
		expectStatus(t, accessRequest(handler, "PUT", closePath, appOrigin, "", requestClose, owner), 204)
		expectStatus(t, accessRequest(handler, "PUT", closePath, appOrigin, "", requestClose, owner), 204)
		untouched := makeDevice("offline-switch-fence")
		emptySwitch := ownerPath + "/devices/" + untouched + "/outlet"
		expectStatus(t, accessRequest(handler, "GET", emptySwitch, appOrigin, "", nil, foreign), 401)
		expectStatus(t, accessRequest(handler, "GET", emptySwitch, appOrigin, "", nil, owner), 200)
		cmd := map[string]any{"outlet_id": otherOutlet, "expected_revision": 0}
		expectStatus(t, accessRequest(handler, "PUT", emptySwitch, appOrigin, "", cmd, owner), 200)
		expectStatus(t, accessRequest(handler, "PUT", emptySwitch, appOrigin, "", cmd, owner), 200)
		if state := id(`SELECT last_outlet_id::text||':'||context_revision FROM hive.devices WHERE id=$1`, untouched); state != otherOutlet+":1" {
			t.Fatal("outlet switch did not fence context", state)
		}
		orphan := id(`INSERT INTO hive.offline_leases(tenant_id,device_id,outlet_id,operator_id,context_id,
            device_revision,context_revision,credential_revision,discount_allowed,tenant_epoch,rbac_revision,transactions_revision,payments_revision,issued_at,expires_at)
            SELECT tenant_id,$3,$4,operator_id,gen_random_uuid(),device_revision,1,credential_revision,
            discount_allowed,tenant_epoch,rbac_revision,transactions_revision,payments_revision,issued_at,expires_at
            FROM hive.offline_leases WHERE tenant_id=$1 AND id=$2 RETURNING id`, tenant, lease.ID, untouched, otherOutlet)
		lostPath := ownerPath + "/leases/" + orphan + "/close"
		lost := offline.CloseInput{Lost: true, Reason: "Perangkat hilang. Bukti lokal tidak dapat dipulihkan."}
		expectStatus(t, accessRequest(handler, "PUT", lostPath, appOrigin, "", lost, owner), 409)
		revokePath := "/api/v1/app/tenants/" + tenant + "/devices/" + untouched + "/revoke"
		expectStatus(t, accessRequest(handler, "POST", revokePath, appOrigin, "offline-test-revoke", map[string]int64{"expected_revision": 1}, owner), 200)
		expectStatus(t, accessRequest(handler, "PUT", lostPath, appOrigin, "", lost, owner), 204)
		expectStatus(t, accessRequest(handler, "PUT", lostPath, appOrigin, "", lost, owner), 204)
		if n := id(`SELECT count(*)::text FROM hive.offline_control_audit WHERE tenant_id=$1 AND lease_id=$2 AND action='lease.lost'`, tenant, orphan); n != "1" {
			t.Fatal("lost device was not audited exactly once", n)
		}

		lateID := id("SELECT gen_random_uuid()")
		next.Sequence = 7
		res = post(lateID, next)
		expectStatus(t, res, 200)
		if !strings.Contains(res.Body.String(), "tenant_suspended") {
			t.Fatal("suspend restored the old lease", res.Body.String())
		}

		if n := id(`SELECT count(*)::text FROM hive.sales_orders WHERE tenant_id=$1 AND id IN ($2,$3,$4,$5)`, tenant, shortage, revoked, suspended, expired); n != "0" {
			t.Fatal("quarantine posted stock", n)
		}
		if n := id(`SELECT count(*)::text FROM hive.offline_control_audit WHERE tenant_id=$1 AND lease_id=$2`, tenant, lease.ID); n != "1" {
			t.Fatal("closure audit replay", n)
		}
	})

	exec(`DELETE FROM hive.tenant_role_location_actions WHERE tenant_id=$1 AND role_id=$2 AND action='sales.checkout.post'`, tenant, approvalRole)
	expectStatus(t, salesCall("PUT", salesPath+"/"+saleID, saleInput), 403)
	expectStatus(t, salesCall("POST", ackPath, saleInput), 403)

	switched := login(device, outlet, second, "098765", 1, 201)
	oldRead(401)
	rev := previous.Revision
	lateProof := challenge(device, devices.OperationTarget{Operation: "context.logout", ContextID: previous.ID, Revision: &rev})
	late := send(handler, "POST", base(device)+"/context/logout", lateProof, nil, cookies[0])
	expectStatus(t, late, 204)
	if len(late.Result().Cookies()) != 0 {
		t.Fatal("late logout mutated the new cookie")
	}
	newCookie := switched.Result().Cookies()[0]
	expectStatus(t, accessRequest(handler, "GET", "/api/v1/app/tenants/"+tenant+"/operator-login-history", appOrigin, "", nil, newCookie), 401)
	expectStatus(t, accessRequest(handler, "GET", "/api/v1/app/tenants/"+tenant+"/operator-login-history?limit=1&member_id="+member, appOrigin, "", nil, owner), 200)
	hidden := accessRequest(handler, "GET", "/api/v1/app/tenants/"+foreignTenant+"/operator-login-history", appOrigin, "", nil, foreign)
	expectStatus(t, hidden, 200)
	if bytes.Contains(hidden.Body.Bytes(), []byte(member)) {
		t.Fatal("cross-tenant audit leak")
	}
	revision := int64(1)
	if _, err := svc.PIN.Set(ctx, tenant, second, owner.Value, "999999", &revision); err != nil {
		t.Fatal(err)
	}
	if count := id("SELECT count(*)::text FROM hive.operational_contexts WHERE tenant_id=$1 AND device_id=$2 AND ended_at IS NULL", tenant, device); count != "0" {
		t.Fatal("reset left a live context")
	}
	// A rollback binary can change lifecycle without updating the newly added credential table.
	exec(`UPDATE hive.tenant_memberships SET active=false,lifecycle_revision=lifecycle_revision+1 WHERE tenant_id=$1 AND principal_id=$2;
    `, tenant, second)
	exec(`UPDATE hive.tenant_memberships SET active=true,lifecycle_revision=lifecycle_revision+1 WHERE tenant_id=$1 AND principal_id=$2`, tenant, second)
	login(device, outlet, second, "999999", 2, 401)
	one, inputOne := loginRequest(otherDevice, outlet, member, "012345", 0)
	two, inputTwo := loginRequest(otherDevice, outlet, member, "012345", 0)
	go func() { results <- send(handler, "POST", base(otherDevice)+"/context", one, inputOne, nil).Code }()
	go func() { results <- send(otherHandler, "POST", base(otherDevice)+"/context", two, inputTwo, nil).Code }()
	successes := 0
	for range 2 {
		switch status := <-results; status {
		case 201:
			successes++
		case 409, 429:
		default:
			t.Errorf("unexpected competing issuance status %d", status)
		}
	}
	if successes != 1 {
		t.Fatalf("competing issuance returned %d successes", successes)
	}
	// Either the refresh commits before deactivation and is revoked, or
	// deactivation commits first and the narrowly scoped login is refused.
	proof, input := loginRequest(otherDevice, outlet, member, "012345", 1)
	deactivate := "/api/v1/app/tenants/" + tenant + "/members/" + member + "/deactivate"
	lifecycle := map[string]any{"revision": 0, "reason_code": "administrative_action"}
	mutation := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		mutation <- accessRequest(handler, "POST", deactivate, appOrigin, "dv3-live-deactivation", lifecycle, owner)
	}()
	raced := send(otherHandler, "POST", base(otherDevice)+"/context", proof, input, nil)
	if raced.Code != 201 && raced.Code != 401 {
		t.Fatalf("unexpected login/lifecycle ordering: %d", raced.Code)
	}
	expectStatus(t, <-mutation, 200)
	if count := id("SELECT count(*)::text FROM hive.operational_contexts WHERE tenant_id=$1 AND member_id=$2 AND ended_at IS NULL", tenant, member); count != "0" {
		t.Fatal("deactivation and issuance left a live context")
	}

	// Start two live contexts in reverse lock order to cover cancellation racing
	// for the same member's contexts across devices.
	freshMember := makeMember("pin-outlet-lifecycle", "456789")
	orderedDevices := []string{device, otherDevice}
	slices.Sort(orderedDevices)
	var fresh *httptest.ResponseRecorder
	for _, deviceID := range []string{orderedDevices[1], orderedDevices[0]} {
		var revision int64
		if err := db.QueryRow(ctx, `SELECT context_revision FROM hive.devices WHERE tenant_id=$1 AND id=$2`, tenant, deviceID).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		response := login(deviceID, outlet, freshMember, "456789", revision, 201)
		if deviceID == device {
			fresh = response
		}
	}
	var active devices.OperationalContext
	if err := json.Unmarshal(fresh.Body.Bytes(), &active); err != nil {
		t.Fatal(err)
	}
	loc := locations.Service{DB: db, AuthorizeOwner: identity.AuthorizeTenantOwner, EndOutletContexts: func(ctx context.Context, tx pgx.Tx, tenant, outlet string) error {
		// A heap scan encounters the two fixtures in insertion order.
		if _, err := tx.Exec(ctx, `SET LOCAL enable_indexscan=off; SET LOCAL enable_bitmapscan=off`); err != nil {
			return err
		}
		return svc.EndOutletContexts(ctx, tx, tenant, outlet)
	}}
	first, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	if _, err = first.Exec(ctx, `SELECT id FROM hive.operational_contexts
        WHERE tenant_id=$1 AND device_id=$2 AND ended_at IS NULL FOR UPDATE`, tenant, orderedDevices[0]); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		revision := int64(0)
		_, err := loc.SetStatus(ctx, tenant, owner.Value, outlet, "location-state-inactive", locations.StatusInput{Status: "inactive", Revision: &revision})
		ended <- err
	}()
	waitForBlocked(t, db, first.Conn().PgConn().PID())
	// With consistent ordering the outlet writer waits for the first context
	// before it can lock the second one.
	if _, err = first.Exec(ctx, `SELECT id FROM hive.operational_contexts
        WHERE tenant_id=$1 AND device_id=$2 AND ended_at IS NULL FOR UPDATE NOWAIT`, tenant, orderedDevices[1]); err != nil {
		t.Fatal("outlet cancellation acquired context locks out of order", err)
	}
	if err = first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-ended; err != nil {
		t.Fatal(err)
	}
	if count := id(`SELECT count(*)::text FROM hive.operational_contexts WHERE tenant_id=$1 AND outlet_id=$2 AND ended_at IS NULL`, tenant, outlet); count != "0" {
		t.Fatal("deactivation left an active context")
	}
	for revision, status := range []string{"inactive", "active"} {
		rev := int64(revision)
		if _, err := loc.SetStatus(ctx, tenant, owner.Value, outlet, "location-state-"+status, locations.StatusInput{Status: status, Revision: &rev}); err != nil {
			t.Fatal(err)
		}
	}
	afterRestore := challenge(device, devices.OperationTarget{Operation: "context.read", ContextID: active.ID, Revision: &active.Revision})
	expectStatus(t, send(handler, "GET", base(device)+"/context", afterRestore, nil, fresh.Result().Cookies()[0]), 401)
	exec(`UPDATE hive.operator_login_history SET occurred_at=statement_timestamp()-interval '168 hours' WHERE tenant_id=$1`, tenant)
	expired := accessRequest(handler, "GET", "/api/v1/app/tenants/"+tenant+"/operator-login-history", appOrigin, "", nil, owner)
	expectStatus(t, expired, 200)
	if !bytes.Contains(expired.Body.Bytes(), []byte(`"events":[]`)) {
		t.Fatal("expired audit remained visible")
	}
	if _, err := app.LoginHistory.CleanupOperators(ctx); err != nil {
		t.Fatal(err)
	}
	if count := id("SELECT count(*)::text FROM hive.operator_login_history WHERE tenant_id=$1", tenant); count != "0" {
		t.Fatal("expired audit remained after cleanup")
	}

}
