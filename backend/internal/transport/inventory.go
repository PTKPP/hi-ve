package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/platform/entitlements"
)

type inventoryCommand func(*http.Request, pgx.Tx, inventory.Service, inventory.Scope, string, string) (any, error)

func inventoryInput[T any](r *http.Request) (T, error) {
	var input T
	if !decodeJSON(r, &input) {
		return input, inventory.ErrInput
	}
	return input, nil
}

func (a API) inventoryRoutes(mux *http.ServeMux) {
	if a.InventoryDB == nil {
		return
	}
	ownerBase := "/api/v1/app/tenants/{tenant_id}/locations/{location_id}/inventory/"
	operatorBase := "/api/v1/operator/tenants/{tenant_id}/devices/{device_id}/locations/{location_id}/inventory/"
	register := func(method, path string, latest bool, command inventoryCommand) {
		if latest && !a.Devices.InventoryCommands {
			return
		}
		mux.HandleFunc(method+" "+ownerBase+path, a.origin(a.AppOrigin, a.inventoryEndpoint(false, command)))
		if a.Devices.InventoryCommands {
			mux.HandleFunc(method+" "+operatorBase+path, a.origin(a.AppOrigin, a.inventoryEndpoint(true, command)))
		}
	}
	register("PUT", "receiving/{id}", false, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		in, err := inventoryInput[inventory.Receiving](r)
		if err != nil {
			return nil, err
		}
		return nil, s.DraftReceiving(r.Context(), tx, scope, id, location, in)
	})
	register("POST", "receiving/{id}/post", false, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		return nil, s.PostReceiving(r.Context(), tx, scope, id, location)
	})
	register("PUT", "repacking/{id}", false, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		in, err := inventoryInput[inventory.Repacking](r)
		if err != nil {
			return nil, err
		}
		return nil, s.DraftRepacking(r.Context(), tx, scope, id, location, in)
	})
	register("POST", "repacking/{id}/post", false, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		return nil, s.PostRepacking(r.Context(), tx, scope, id, location)
	})
	register("PUT", "transfers/{id}", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		in, err := inventoryInput[inventory.Transfer](r)
		if err != nil {
			return nil, err
		}
		return nil, s.DraftTransfer(r.Context(), tx, scope, id, location, in)
	})
	register("POST", "transfers/{id}/dispatch", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		return nil, s.DispatchTransfer(r.Context(), tx, scope, id, location)
	})
	register("PUT", "transfers/{id}/receipts/{operation_id}", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		var in struct {
			Source   string `json:"source_id"`
			Quantity string `json:"quantity"`
			Unit     string `json:"unit"`
			Reason   string `json:"discrepancy_reason"`
		}
		if !decodeJSON(r, &in) {
			return nil, inventory.ErrInput
		}
		return nil, s.ReceiveTransfer(r.Context(), tx, scope, r.PathValue("operation_id"), inventory.TransferReceipt{
			Transfer: id, Source: in.Source, Destination: location, Quantity: in.Quantity, Unit: in.Unit, DiscrepancyReason: in.Reason,
		})
	})
	register("PUT", "adjustments/{id}", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		in, err := inventoryInput[inventory.Adjustment](r)
		if err != nil {
			return nil, err
		}
		return nil, s.DraftAdjustment(r.Context(), tx, scope, id, location, in)
	})
	register("POST", "adjustments/{id}/approve", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		var in struct {
			Reason string `json:"reason"`
		}
		if !decodeJSON(r, &in) {
			return nil, inventory.ErrInput
		}
		return nil, s.ApproveAdjustment(r.Context(), tx, scope, id, location, in.Reason)
	})
	register("PUT", "counts/{id}", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		var in struct {
			Items []inventory.CountItem `json:"items"`
		}
		if !decodeJSON(r, &in) {
			return nil, inventory.ErrInput
		}
		return nil, s.StartCount(r.Context(), tx, scope, id, location, in.Items)
	})
	register("PUT", "counts/{id}/observations/{operation_id}", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		in, err := inventoryInput[inventory.Measure](r)
		if err != nil {
			return nil, err
		}
		return s.RecordCount(r.Context(), tx, scope, id, r.PathValue("operation_id"), location, in)
	})
	register("POST", "counts/{id}/approve", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		in, err := inventoryInput[inventory.CountApproval](r)
		if err != nil {
			return nil, err
		}
		return nil, s.ApproveCount(r.Context(), tx, scope, id, location, in)
	})
	for _, path := range []string{"adjustments", "counts"} {
		register("POST", path+"/{id}/reject", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
			var in struct {
				Reason string `json:"reason"`
			}
			if !decodeJSON(r, &in) {
				return nil, inventory.ErrInput
			}
			return nil, s.RejectCorrection(r.Context(), tx, scope, id, location, in.Reason, path == "counts")
		})
	}
	register("POST", "discrepancies/{id}/resolve", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		var in struct {
			Reason string `json:"reason"`
			Found  *bool  `json:"found"`
		}
		if !decodeJSON(r, &in) || in.Found == nil {
			return nil, inventory.ErrInput
		}
		return nil, s.ResolveDiscrepancy(r.Context(), tx, scope, id, location, inventory.DiscrepancyResolution{Reason: in.Reason, Found: *in.Found})
	})
	register("GET", "balances/{id}", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		return s.Stock(r.Context(), tx, scope, location, id)
	})
	register("GET", "batches/{id}/movements", true, func(r *http.Request, tx pgx.Tx, s inventory.Service, scope inventory.Scope, id, location string) (any, error) {
		return s.BatchHistory(r.Context(), tx, scope, location, id, nil)
	})
}

func (a API) inventoryEndpoint(operator bool, command inventoryCommand) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, location, id := r.PathValue("tenant_id"), r.PathValue("location_id"), r.PathValue("id")
		device, operation := r.PathValue("device_id"), r.PathValue("operation_id")
		if !uuid.MatchString(tenant) || !uuid.MatchString(location) || !uuid.MatchString(id) || r.URL.RawQuery != "" ||
			(operator && !uuid.MatchString(device)) || (operation != "" && !uuid.MatchString(operation)) {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		if err != nil {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		ctx := r.Context()
		var result any
		execute := func(tx pgx.Tx, actor string) error {
			scope := inventory.Scope{Tenant: tenant, Actor: actor}
			service := inventory.Service{Authorize: func(ctx context.Context, tx pgx.Tx, called inventory.Scope, action, scopedLocation string) error {
				if called != scope || !identity.IsInventoryLocationAction(action) {
					return identity.ErrForbidden
				}
				if operator {
					if err := identity.AuthorizeLocationGrant(ctx, tx, tenant, actor, scopedLocation, action); err != nil {
						return err
					}
				}
				capability := action[:strings.LastIndexByte(action, '.')]
				return a.Entitlements.CheckWrite(ctx, tx, tenant, capability)
			}}
			var err error
			result, err = command(r, tx, service, scope, id, location)
			return err
		}
		if operator {
			proof, token := r.Header.Get("Device-Proof"), cookie(r, operatorCookie)
			if len(proof) == 0 || len(proof) > 2048 {
				deviceError(w, devices.ErrProof)
				return
			}
			authorized := func(tx pgx.Tx, current devices.OperationalContext) error {

				return execute(tx, current.Member)
			}
			if r.Method == "GET" {
				err = a.Devices.ReadContext(ctx, tenant, device, proof, token, authorized)
			} else {
				key := r.Header.Get("Idempotency-Key")
				if !keyPattern.MatchString(key) {
					fail(w, 400, "INVALID_REQUEST")
					return
				}
				err = a.Devices.InventoryCommand(ctx, tenant, device, proof, token, commandDigest(r.Method, r.URL.EscapedPath(), key, raw), authorized)
			}
		} else {
			err = pgx.BeginFunc(ctx, a.InventoryDB, func(tx pgx.Tx) error {
				actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, cookie(r, tenantCookie))
				if err != nil {
					return err
				}
				return execute(tx, actor)
			})
		}
		switch {
		case err == nil && result != nil:
			respond(w, 200, result)
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, inventory.ErrInput), errors.Is(err, catalog.ErrInput):
			fail(w, 400, "INVALID_REQUEST")
		case errors.Is(err, inventory.ErrStaleSnapshot):
			fail(w, 409, "STALE_SNAPSHOT")
		case errors.Is(err, inventory.ErrConflict), errors.Is(err, inventory.ErrShortage):
			fail(w, 409, "STATE_CONFLICT")
		case errors.Is(err, inventory.ErrSelfApproval):
			fail(w, 403, "SELF_APPROVAL_FORBIDDEN")
		case errors.Is(err, locations.ErrOutletUnavailable), errors.Is(err, catalog.ErrUnavailable):
			fail(w, 404, "NOT_FOUND")
		case errors.Is(err, entitlements.ErrDenied):
			fail(w, 403, "ENTITLEMENT_DENIED")
		case errors.Is(err, devices.ErrProof):
			deviceError(w, err)
		default:
			handleError(w, err)
		}
	}
}

func decodeJSON(r *http.Request, out any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(out) == nil && decoder.Decode(new(any)) == io.EOF
}
