package transport

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/platform/entitlements"
)

func (a API) inventoryRoutes(mux *http.ServeMux) {
	if a.InventoryDB == nil {
		return
	}
	const base = "/api/v1/app/tenants/{tenant_id}/locations/{location_id}/inventory/"
	mux.HandleFunc("PUT "+base+"receiving/{id}", a.origin(a.AppOrigin, a.inventoryDocument(true, false)))
	mux.HandleFunc("POST "+base+"receiving/{id}/post", a.origin(a.AppOrigin, a.inventoryDocument(true, true)))
	mux.HandleFunc("PUT "+base+"repacking/{id}", a.origin(a.AppOrigin, a.inventoryDocument(false, false)))
	mux.HandleFunc("POST "+base+"repacking/{id}/post", a.origin(a.AppOrigin, a.inventoryDocument(false, true)))
}

// Keep route mounting opt-in for instances on pre-Inventory schema.
func (a API) inventoryDocument(receiving, post bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, location, id := r.PathValue("tenant_id"), r.PathValue("location_id"), r.PathValue("id")
		if !uuid.MatchString(tenant) || !uuid.MatchString(location) || !uuid.MatchString(id) || r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		var receivingInput inventory.Receiving
		var repackInput inventory.Repacking
		if !post {
			if receiving {
				if !decode(w, r, &receivingInput) {
					return
				}
			} else if !decode(w, r, &repackInput) {
				return
			}
		}
		ctx := r.Context()
		err := pgx.BeginFunc(ctx, a.InventoryDB, func(tx pgx.Tx) error {
			actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, cookie(r, tenantCookie))
			if err != nil {
				return err
			}
			scope := inventory.Scope{Tenant: tenant, Actor: actor}
			capability := "inventory.repacking"
			if receiving {
				capability = "inventory.receiving"
			}
			service := inventory.Service{Authorize: func(_ context.Context, tx pgx.Tx, calledScope inventory.Scope, _ string, calledLocation string) error {
				if calledScope != scope || calledLocation != location {
					return identity.ErrForbidden
				}
				return a.Entitlements.CheckWrite(ctx, tx, tenant, capability)
			}}
			switch {
			case receiving && post:
				return service.PostReceiving(ctx, tx, scope, id, location)
			case receiving:
				return service.DraftReceiving(ctx, tx, scope, id, location, receivingInput)
			case post:
				return service.PostRepacking(ctx, tx, scope, id, location)
			default:
				return service.DraftRepacking(ctx, tx, scope, id, location, repackInput)
			}
		})
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, inventory.ErrInput), errors.Is(err, catalog.ErrInput):
			fail(w, 400, "INVALID_REQUEST")
		case errors.Is(err, inventory.ErrConflict), errors.Is(err, inventory.ErrShortage):
			fail(w, 409, "STATE_CONFLICT")
		case errors.Is(err, locations.ErrOutletUnavailable), errors.Is(err, catalog.ErrUnavailable):
			fail(w, 404, "NOT_FOUND")
		case errors.Is(err, entitlements.ErrDenied):
			fail(w, 403, "ENTITLEMENT_DENIED")
		default:
			handleError(w, err)
		}
	}
}
