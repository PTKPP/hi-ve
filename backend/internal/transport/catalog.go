package transport

import (
	"errors"
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
