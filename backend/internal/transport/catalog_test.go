package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/platform/entitlements"
)

func TestCatalogPostgres(t *testing.T) {
	ctx := t.Context()
	db := disposableDatabase(t)
	tenant, _, owner := ownerSessionFixture(t, db, "catalog-owner")
	foreign, _, other := ownerSessionFixture(t, db, "catalog-foreign")
	ent := entitlements.Service{DB: db}
	cat := catalog.Service{DB: db, AuthorizeOwner: identity.AuthorizeTenantOwner, LockOutlets: locations.LockActiveOutlets,
		CheckCapability: func(ctx context.Context, tx pgx.Tx, tenant, capability string) error {
			// Exercise pricing independently of the commercial grant allowlist.
			if capability == "catalog.pricing" {
				return nil
			}
			return ent.CheckWrite(ctx, tx, tenant, capability)
		}}
	loc := locations.Service{DB: db, AuthorizeOwner: identity.AuthorizeTenantOwner, EndOutletContexts: (devices.Service{DB: db}).EndOutletContexts}
	handler := API{Catalog: cat, Locations: loc, AppOrigin: appOrigin}.Handler()
	const product = "d96ca4f5-e677-4a7a-90ea-fbd040000001"
	const sku = "d96ca4f5-e677-4a7a-90ea-fbd040000002"
	const variant = "d96ca4f5-e677-4a7a-90ea-fbd040000003"
	root := "/api/v1/app/tenants/" + tenant
	path := root + "/catalog/"
	put := func(path, key string, body any, c *http.Cookie) *httptest.ResponseRecorder {
		return accessRequest(handler, "PUT", path, appOrigin, "catalog-fixture-"+key, body, c)
	}
	parent := map[string]any{"name": "Madu", "category": "Pangan", "revision": 0}
	products := path + "products/" + product
	expectStatus(t, put(products, "create", parent, other), 401)
	expectStatus(t, put(products, "create", parent, owner), 403)
	for _, id := range []string{tenant, foreign} {
		if _, err := db.Exec(ctx, `INSERT INTO hive.tenant_entitlements VALUES($1,'catalog.products',now(),now()+interval '1 day','fixture',NULL,1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { results <- put(products, "create", parent, owner) }()
	}
	for range 2 {
		expectStatus(t, <-results, 200)
	}
	expectStatus(t, put(products, "create", map[string]any{"name": "Lain", "category": "A", "revision": 0}, owner), 409)
	input := map[string]any{"revision": 0, "product_id": product, "code": "curah", "variant": "Curah", "barcode": "00001", "active": true, "batch_tracked": true, "unit": "g"}
	skus := path + "skus/"
	expectStatus(t, put(skus+sku, "sku", input, owner), 200)
	input["code"] = "halus"
	expectStatus(t, put(skus+variant, "dup-barcode", input, owner), 409)
	input["barcode"] = "00002"
	expectStatus(t, put(skus+variant, "variant", input, owner), 200)
	listed := accessRequest(handler, "GET", products+"/skus", appOrigin, "", nil, owner)
	expectStatus(t, listed, 200)
	var page struct {
		SKUs []catalog.ProductVariant `json:"skus"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil || len(page.SKUs) != 2 {
		t.Fatal("variant list", err)
	}
	expectStatus(t, accessRequest(handler, "GET", path+"products", appOrigin, "", nil, owner), 200)
	foreignPath := "/api/v1/app/tenants/" + foreign + "/catalog/"
	expectStatus(t, put(foreignPath+"skus/"+sku, "foreign-parent", input, other), 409)
	expectStatus(t, put(foreignPath+"products/"+product, "create", parent, other), 200)
	expectStatus(t, put(foreignPath+"skus/"+sku, "foreign-sku", input, other), 200)
	makeLocation := func(tenant, code, kind string) string {
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO hive.locations(tenant_id,code,name,kind) VALUES($1,$2,$2,$3) RETURNING id`, tenant, code, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	north, south, warehouse := makeLocation(tenant, "north", "outlet"), makeLocation(tenant, "south", "outlet"), makeLocation(tenant, "central", "warehouse")
	foreignOutlet := makeLocation(foreign, "north", "outlet")
	amount := func(rev int, n string) map[string]any { return map[string]any{"amount": n, "revision": rev} }
	prices := skus + sku
	liveCatalog := cat
	liveCatalog.CheckCapability = ent.CheckWrite
	liveApp := API{Catalog: liveCatalog, AppOrigin: appOrigin}.Handler()
	expectStatus(t, accessRequest(liveApp, "PUT", prices+"/price", appOrigin, "price-without-entitlement", amount(0, "100"), owner), 403)
	expectStatus(t, put(prices+"/price", "decimal", amount(0, "1e3"), owner), 400)
	expectStatus(t, put(prices+"/price", "base", amount(0, "9900.0001"), owner), 200)
	for _, bad := range []string{warehouse, foreignOutlet} {
		expectStatus(t, put(prices+"/outlets/"+bad+"/price", "bad-outlet", amount(0, "1"), owner), 404)
	}
	override := prices + "/outlets/" + north + "/price"
	expectStatus(t, put(override, "override", amount(0, "8800.1234"), owner), 200)
	quote := func(outlet string) (catalog.Price, error) {
		var price catalog.Price
		err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			var err error
			price, err = cat.SellingPrice(ctx, tx, tenant, outlet, sku)
			return err
		})
		return price, err
	}
	original, err := quote(north)
	if err != nil || original.Amount != "8800.1234" {
		t.Fatal("override", original, err)
	}
	fallback, err := quote(south)
	if err != nil || fallback.Amount != "9900.0001" {
		t.Fatal("fallback", fallback, err)
	}
	for _, key := range []string{"race-a", "race-b"} {
		go func() { results <- put(override, key, amount(1, "9100"), owner) }()
	}
	a, b := <-results, <-results
	if a.Code+b.Code != 609 {
		t.Fatalf("price race %d/%d", a.Code, b.Code)
	}
	history := accessRequest(handler, "GET", override+"/history?before_revision=2", appOrigin, "", nil, owner)
	expectStatus(t, history, 200)
	var versions struct {
		Prices []catalog.Price `json:"prices"`
	}
	if err := json.Unmarshal(history.Body.Bytes(), &versions); err != nil || len(versions.Prices) != 1 || versions.Prices[0].ID != original.ID {
		t.Fatal("history cursor", err)
	}
	for _, q := range []string{"UPDATE hive.catalog_prices SET amount=0", "DELETE FROM hive.catalog_prices", "TRUNCATE hive.catalog_prices"} {
		_, err := db.Exec(ctx, q)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatal("mutable historical price", err)
		}
	}
	var old string
	if err = db.QueryRow(ctx, `SELECT amount::text FROM hive.catalog_prices WHERE tenant_id=$1 AND id=$2`, tenant, original.ID).Scan(&old); err != nil || old != original.Amount {
		t.Fatal("old price changed", err)
	}
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		q, err := catalog.QuantityForSKU(ctx, tx, tenant, sku, "1.000000000000000000001", "kg")
		if err != nil || q != "1000.000000000000000001000" {
			t.Fatalf("SKU quantity %q %v", q, err)
		}
		_, err = locations.PrimaryInventory(ctx, tx, tenant, warehouse, true)
		if !errors.Is(err, locations.ErrOutletUnavailable) {
			t.Fatal("warehouse used by cashier")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := loc.InventoryMapping(ctx, tenant, owner.Value, north)
	if err != nil || mapping.Revision != 0 || mapping.InventoryLocation == "" {
		t.Fatal(mapping, err)
	}
	statusPath := root + "/locations/" + north + "/status"
	inactive := map[string]any{"revision": 0, "status": "inactive"}
	expectStatus(t, put(statusPath, "disable", inactive, other), 401)
	expectStatus(t, put(statusPath, "disable", inactive, owner), 200)
	expectStatus(t, put(statusPath, "disable", inactive, owner), 200)
	if _, err = quote(north); !errors.Is(err, locations.ErrOutletUnavailable) && !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatal("inactive outlet", err)
	}
	expectStatus(t, put(statusPath, "enable", map[string]any{"revision": 1, "status": "active"}, owner), 200)
	restored, err := loc.InventoryMapping(ctx, tenant, owner.Value, north)
	if err != nil || restored.InventoryLocation != mapping.InventoryLocation || restored.Revision != 2 {
		t.Fatal("mapping changed after reactivation", err)
	}
	input["code"] = "curah"
	input["barcode"] = "00001"
	input["revision"] = 1
	input["active"] = false
	expectStatus(t, put(skus+sku, "deactivate", input, owner), 200)
	if _, err = quote(north); !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatal("inactive SKU saleable", err)
	}
	expectStatus(t, accessRequest(handler, "GET", skus+sku, appOrigin, "", nil, owner), 200)
	if _, err = db.Exec(ctx, `UPDATE hive.tenant_entitlements SET revoked_at=now() WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, put(products, "create", parent, owner), 403)
}
