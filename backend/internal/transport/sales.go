package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/devices"
	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/locations"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/sales"
)

type salesCommand func(*http.Request, pgx.Tx, sales.Scope, sales.Service) (any, error)

func (a API) salesRoutes(mux *http.ServeMux) {
	if !a.Devices.SalesCommands || a.SalesDB == nil {
		return
	}
	const operator = "/api/v1/operator/tenants/{tenant_id}/devices/{device_id}/locations/{location_id}/sales"
	const app = "/api/v1/app/tenants/{tenant_id}/locations/{location_id}/sales"
	register := func(method, base, path string, isOperator bool, cmd salesCommand) {
		mux.HandleFunc(method+" "+base+path, a.origin(a.AppOrigin, a.salesEndpoint(isOperator, cmd)))
	}
	register("PUT", operator, "/{id}", true, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
		var in sales.CheckoutInput
		if !decodeJSON(r, &in) {
			return nil, sales.ErrInput
		}
		return s.Checkout(r.Context(), tx, scope, r.PathValue("id"), in)
	})
	register("POST", operator, "/{id}/ack", true, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
		var in sales.CheckoutInput
		if !decodeJSON(r, &in) {
			return nil, sales.ErrInput
		}
		return s.AcknowledgeCash(r.Context(), tx, scope, r.PathValue("id"), in)
	})

	register("POST", operator, "/{id}/confirm-manual", true, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
		var in sales.ManualConfirmation
		if !decodeJSON(r, &in) {
			return nil, sales.ErrInput
		}
		return s.ConfirmManual(r.Context(), tx, scope, r.PathValue("id"), in)
	})
	register("GET", operator, "/items/search", true, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 1 || len(query["q"]) != 1 {
			return nil, sales.ErrInput
		}
		return s.Search(r.Context(), tx, scope, query.Get("q"))
	})
	for _, base := range []string{operator, app} {
		for _, kind := range []string{"returns", "voids"} {
			const target = "/{id}/"
			suffix := target + kind + "/{reversal_id}"
			register("PUT", base, suffix, base == operator, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
				var in sales.ReversalInput
				if !decodeJSON(r, &in) {
					return nil, sales.ErrInput
				}
				return s.RequestReversal(r.Context(), tx, scope, r.PathValue("id"), r.PathValue("reversal_id"), kind == "voids", in)
			})
			register("POST", base, suffix+"/approve", base == operator, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
				var in struct {
					Reason string `json:"reason"`
				}
				if !decodeJSON(r, &in) {
					return nil, sales.ErrInput
				}
				return s.ApproveReversal(r.Context(), tx, scope, r.PathValue("id"), r.PathValue("reversal_id"), in.Reason, kind == "voids")
			})
		}
		register("POST", base, "/{id}/refunds/{reversal_id}/confirm", base == operator, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
			var in sales.ManualConfirmation
			if !decodeJSON(r, &in) {
				return nil, sales.ErrInput
			}
			return s.ConfirmRefund(r.Context(), tx, scope, r.PathValue("id"), r.PathValue("reversal_id"), in.Reference)
		})
	}

	for _, base := range []string{operator, app} {
		register("GET", base, "", base == operator, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
			filter, err := salesHistoryFilter(r.URL.RawQuery)
			if err != nil {
				return nil, err
			}
			return s.History(r.Context(), tx, scope, filter)
		})
		for _, suffix := range []string{"/{id}", "/{id}/receipt"} {
			register("GET", base, suffix, base == operator, func(r *http.Request, tx pgx.Tx, scope sales.Scope, s sales.Service) (any, error) {
				if r.URL.RawQuery != "" {
					return nil, sales.ErrInput
				}
				return s.Detail(r.Context(), tx, scope, r.PathValue("id"), suffix == "/{id}/receipt")
			})
		}
	}
}

func (a API) salesEndpoint(operator bool, command salesCommand) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, outlet, device := r.PathValue("tenant_id"), r.PathValue("location_id"), r.PathValue("device_id")
		if !uuid.MatchString(tenant) || !uuid.MatchString(outlet) || operator && !uuid.MatchString(device) ||
			r.Method != "GET" && r.URL.RawQuery != "" {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32768))
		if err != nil {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		ctx := r.Context()
		var out any
		execute := func(tx pgx.Tx, actor string) error {
			scope := sales.Scope{Tenant: tenant, Outlet: outlet, Actor: actor, Device: device}
			service := sales.Service{Catalog: a.Catalog, IsPrimaryOwner: func(_ context.Context, _ pgx.Tx, caller sales.Scope) (bool, error) {
				// Owner is reachable only after the Identity live Google Owner guard.
				return !operator && caller == scope, nil
			}, Authorize: func(ctx context.Context, tx pgx.Tx, caller sales.Scope, action string) error {
				if caller != scope || !identity.IsSalesLocationAction(action) {
					return identity.ErrForbidden
				}
				if operator {
					if err := identity.AuthorizeLocationGrant(ctx, tx, tenant, actor, outlet, action); err != nil {
						return err
					}
				}
				capability := action[:strings.LastIndexByte(action, '.')]
				if action == sales.CheckoutPermission {
					capability = "sales.transactions"
					if err := a.Entitlements.CheckWrite(ctx, tx, tenant, "sales.payments"); err != nil {
						return err
					}
				}
				return a.Entitlements.CheckWrite(ctx, tx, tenant, capability)
			}}
			var err error
			out, err = command(r, tx, scope, service)
			return err
		}
		if operator {
			proof, token := r.Header.Get("Device-Proof"), cookie(r, operatorCookie)
			if proof == "" || len(proof) > 2048 {
				deviceError(w, devices.ErrProof)
				return
			}
			run := func(tx pgx.Tx, c devices.OperationalContext) error {
				if c.Outlet != outlet {
					return identity.ErrForbidden
				}
				return execute(tx, c.Member)
			}
			if r.Method == "GET" {
				err = a.Devices.ReadContext(ctx, tenant, device, proof, token, run)
			} else {
				key := r.Header.Get("Idempotency-Key")
				if !keyPattern.MatchString(key) {
					fail(w, 400, "INVALID_REQUEST")
					return
				}
				err = a.Devices.SalesCommand(ctx, tenant, device, proof, token, commandDigest(r.Method, r.URL.EscapedPath(), key, raw), run)
			}
		} else {
			err = pgx.BeginFunc(ctx, a.SalesDB, func(tx pgx.Tx) error {
				actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, cookie(r, tenantCookie))
				if err != nil {
					return err
				}
				return execute(tx, actor)
			})
		}
		switch {
		case err == nil:
			respond(w, 200, out)
		case errors.Is(err, sales.ErrInput), errors.Is(err, catalog.ErrInput), errors.Is(err, inventory.ErrInput):
			fail(w, 400, "INVALID_REQUEST")
		case errors.Is(err, sales.ErrTender):
			fail(w, 409, "INSUFFICIENT_TENDER")
		case errors.Is(err, inventory.ErrShortage):
			fail(w, 409, "INSUFFICIENT_STOCK")
		case errors.Is(err, sales.ErrConflict), errors.Is(err, inventory.ErrConflict):
			fail(w, 409, "STATE_CONFLICT")
		case errors.Is(err, sales.ErrUnavailable), errors.Is(err, catalog.ErrUnavailable), errors.Is(err, locations.ErrOutletUnavailable):
			fail(w, 404, "NOT_FOUND")
		case errors.Is(err, sales.ErrSelfApproval):
			fail(w, 403, "SELF_APPROVAL_FORBIDDEN")
		case errors.Is(err, entitlements.ErrDenied):
			fail(w, 403, "ENTITLEMENT_DENIED")
		case errors.Is(err, devices.ErrProof):
			deviceError(w, err)
		default:
			handleError(w, err)
		}
	}
}

func salesHistoryFilter(raw string) (sales.HistoryFilter, error) {
	var f sales.HistoryFilter
	values, err := url.ParseQuery(raw)
	if err != nil {
		return f, sales.ErrInput
	}
	for key, value := range values {
		if len(value) != 1 || value[0] == "" {
			return f, sales.ErrInput
		}
		switch key {
		case "sale_id":
			f.ID = value[0]
		case "operator_id":
			f.Operator = value[0]
		case "total":
			f.Total = value[0]
		case "before_id":
			f.BeforeID = value[0]
		case "from", "to", "before":
			parsed, err := time.Parse(time.RFC3339Nano, value[0])
			if err != nil {
				return f, sales.ErrInput
			}
			switch key {
			case "from":
				f.From = &parsed
			case "to":
				f.To = &parsed
			case "before":
				f.Before = &parsed
			}
		default:
			return f, sales.ErrInput
		}
	}
	return f, nil
}
