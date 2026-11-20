package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/entitlements"
)

func catalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, entitlements.ErrDenied):
		fail(w, 403, "ENTITLEMENT_DENIED")
	case errors.Is(err, catalog.ErrConflict):
		fail(w, 409, "STATE_CONFLICT")
	case errors.Is(err, catalog.ErrInput):
		fail(w, 400, "INVALID_REQUEST")
	case errors.Is(err, catalog.ErrUnavailable):
		fail(w, 404, "NOT_FOUND")
	default:
		handleError(w, err)
	}
}

func (a API) catalogRoutes(mux *http.ServeMux) {
	if a.Catalog.DB == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/operator/tenants/{tenant_id}/devices/{device_id}/catalog/skus/{id}/price", a.origin(a.AppOrigin, a.operatorPrice))
	if a.Devices.CatalogCommands {
		for _, kind := range []string{"products", "skus"} {
			mux.HandleFunc("PUT /api/v1/operator/tenants/{tenant_id}/devices/{device_id}/catalog/"+kind+"/{id}", a.origin(a.AppOrigin, a.staffCatalogWrite(kind)))
		}
	}
	const root = "/api/v1/app/tenants/{tenant_id}/catalog/"
	mux.HandleFunc("GET "+root+"products", a.origin(a.AppOrigin, a.catalogList))
	mux.HandleFunc("GET "+root+"products/{product}/skus", a.origin(a.AppOrigin, a.catalogList))
	mux.HandleFunc("GET "+root+"skus/{id}", a.origin(a.AppOrigin, func(w http.ResponseWriter, r *http.Request) {
		t, id := r.PathValue("tenant_id"), r.PathValue("id")
		if !uuid.MatchString(t) || !uuid.MatchString(id) {
			catalogError(w, catalog.ErrInput)
			return
		}
		row, err := a.Catalog.ProductSKU(r.Context(), t, cookie(r, tenantCookie), id)
		if err != nil {
			catalogError(w, err)
			return
		}
		respond(w, 200, row)
	}))
	for _, path := range []string{"skus/{id}/price/history", "skus/{id}/outlets/{outlet}/price/history"} {
		mux.HandleFunc("GET "+root+path, a.origin(a.AppOrigin, a.priceHistory))
	}

	for path, kind := range map[string]string{"products/{id}": "product", "skus/{id}": "sku", "skus/{id}/price": "price", "skus/{id}/outlets/{outlet}/price": "price"} {
		mux.HandleFunc("PUT "+root+path, a.origin(a.AppOrigin, func(w http.ResponseWriter, r *http.Request) {
			tenant, id, outlet, key := r.PathValue("tenant_id"), r.PathValue("id"), r.PathValue("outlet"), r.Header.Get("Idempotency-Key")
			if !uuid.MatchString(tenant) || !uuid.MatchString(id) || outlet != "" && !uuid.MatchString(outlet) || !keyPattern.MatchString(key) {
				catalogError(w, catalog.ErrInput)
				return
			}
			token := cookie(r, tenantCookie)
			var rev int64
			var err error
			switch kind {
			case "product":
				var in catalog.ProductInput
				if !decode(w, r, &in) {
					return
				}
				rev, err = a.Catalog.PutProduct(r.Context(), tenant, token, key, id, in)
			case "sku":
				var in catalog.SKUInput
				if !decode(w, r, &in) {
					return
				}
				if !uuid.MatchString(in.Product) {
					catalogError(w, catalog.ErrInput)
					return
				}
				rev, err = a.Catalog.PutSKU(r.Context(), tenant, token, key, id, in)
			case "price":
				var in catalog.PriceInput
				if !decode(w, r, &in) {
					return
				}
				rev, err = a.Catalog.PutPrice(r.Context(), tenant, token, key, id, outlet, in)
			}
			if err != nil {
				catalogError(w, err)
				return
			}
			respond(w, 200, map[string]int64{"revision": rev})
		}))
	}
}

func (a API) operatorPrice(w http.ResponseWriter, r *http.Request) {
	tenant, device, id := r.PathValue("tenant_id"), r.PathValue("device_id"), r.PathValue("id")
	if !uuid.MatchString(tenant) || !uuid.MatchString(device) || !uuid.MatchString(id) || r.URL.RawQuery != "" {
		catalogError(w, catalog.ErrInput)
		return
	}
	proof := r.Header.Get("Device-Proof")
	if len(proof) == 0 || len(proof) > 2048 {
		deviceError(w, devices.ErrProof)
		return
	}
	var price catalog.Price
	err := a.Devices.ReadContext(r.Context(), tenant, device, proof, cookie(r, operatorCookie), func(tx pgx.Tx, current devices.OperationalContext) error {
		if err := identity.AuthorizeLocationGrant(r.Context(), tx, tenant, current.Member, current.Outlet, identity.CatalogPriceRead); err != nil {
			return err
		}
		var err error
		price, err = a.Catalog.SellingPrice(r.Context(), tx, tenant, current.Outlet, id)
		return err
	})
	if errors.Is(err, devices.ErrProof) {
		deviceError(w, err)
		return
	}
	if err != nil {
		catalogError(w, err)
		return
	}
	respond(w, 200, price)
}

func (a API) priceHistory(w http.ResponseWriter, r *http.Request) {
	tenant, id, outlet := r.PathValue("tenant_id"), r.PathValue("id"), r.PathValue("outlet")
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !uuid.MatchString(tenant) || !uuid.MatchString(id) || outlet != "" && !uuid.MatchString(outlet) || len(query) > 0 && (len(query) != 1 || len(query["before_revision"]) != 1) {
		catalogError(w, catalog.ErrInput)
		return
	}
	var before int64
	if len(query) != 0 {
		before, err = strconv.ParseInt(query.Get("before_revision"), 10, 64)
		if err != nil || before < 1 {
			catalogError(w, catalog.ErrInput)
			return
		}
	}
	rows, err := a.Catalog.PriceHistory(r.Context(), tenant, cookie(r, tenantCookie), id, outlet, before)
	if err != nil {
		catalogError(w, err)
		return
	}
	respond(w, 200, map[string][]catalog.Price{"prices": rows})
}

func (a API) catalogList(w http.ResponseWriter, r *http.Request) {
	tenant, product := r.PathValue("tenant_id"), r.PathValue("product")
	query, err := url.ParseQuery(r.URL.RawQuery)
	after := query.Get("after_id")
	if err != nil || !uuid.MatchString(tenant) || product != "" && !uuid.MatchString(product) || len(query) > 0 && (len(query) != 1 || len(query["after_id"]) != 1 || !uuid.MatchString(after)) {
		catalogError(w, catalog.ErrInput)
		return
	}
	token := cookie(r, tenantCookie)
	if product != "" {
		rows, err := a.Catalog.ProductSKUs(r.Context(), tenant, token, product, after)
		if err != nil {
			catalogError(w, err)
			return
		}
		respond(w, 200, map[string][]catalog.ProductVariant{"skus": rows})
		return
	}
	rows, err := a.Catalog.Products(r.Context(), tenant, token, after)
	if err != nil {
		catalogError(w, err)
		return
	}
	respond(w, 200, map[string][]catalog.Product{"products": rows})
}

// commandDigest binds the signed device command to the method, escaped path, receipt key and exact body.
func commandDigest(method, path, key string, body []byte) string {
	hash := sha256.New()
	io.WriteString(hash, method+"\n"+path+"\n"+key+"\n")
	hash.Write(body)
	return hex.EncodeToString(hash.Sum(nil))
}
func (a API) staffCatalogWrite(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, device, id, key := r.PathValue("tenant_id"), r.PathValue("device_id"), r.PathValue("id"), r.Header.Get("Idempotency-Key")
		if !uuid.MatchString(tenant) || !uuid.MatchString(device) || !uuid.MatchString(id) || !keyPattern.MatchString(key) || r.URL.RawQuery != "" {
			catalogError(w, catalog.ErrInput)
			return
		}
		proof := r.Header.Get("Device-Proof")
		if len(proof) == 0 || len(proof) > 2048 {
			deviceError(w, devices.ErrProof)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		if err != nil {
			catalogError(w, catalog.ErrInput)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var product catalog.ProductInput
		var sku catalog.SKUInput
		if kind == "products" {
			if !decode(w, r, &product) {
				return
			}
		} else {
			if !decode(w, r, &sku) {
				return
			}
			if !uuid.MatchString(sku.Product) {
				catalogError(w, catalog.ErrInput)
				return
			}
		}
		digest := commandDigest(r.Method, r.URL.EscapedPath(), key, raw)
		var rev int64
		err = a.Devices.CatalogCommand(r.Context(), tenant, device, proof, cookie(r, operatorCookie), digest, func(tx pgx.Tx, current devices.OperationalContext) error {
			if err := identity.AuthorizeCatalogManagement(r.Context(), tx, tenant, current.Member); err != nil {
				return err
			}
			var err error
			if kind == "products" {
				rev, err = a.Catalog.PutProductTx(r.Context(), tx, tenant, current.Member, key, id, product)
			} else {
				rev, err = a.Catalog.PutSKUTx(r.Context(), tx, tenant, current.Member, key, id, sku)
			}
			return err
		})
		if errors.Is(err, devices.ErrProof) {
			deviceError(w, err)
			return
		}
		if err != nil {
			catalogError(w, err)
			return
		}
		respond(w, 200, map[string]int64{"revision": rev})
	}
}
