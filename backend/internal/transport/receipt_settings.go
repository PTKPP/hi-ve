package transport

import (
	"errors"
	"net/http"

	"hi-ve/backend/internal/settings"
)

func (a API) receiptRoutes(mux *http.ServeMux) {
	const path = "/api/v1/app/tenants/{tenant_id}/settings/receipt"
	mux.HandleFunc("GET "+path, a.origin(a.AppOrigin, a.receiptSettings))
	mux.HandleFunc("PUT "+path, a.origin(a.AppOrigin, a.receiptSettings))
}

func (a API) receiptSettings(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	if !uuid.MatchString(tenant) {
		fail(w, 400, "INVALID_REQUEST")
		return
	}
	ctx, token := r.Context(), cookie(r, tenantCookie)
	var result settings.Receipt
	var err error
	if r.Method == http.MethodPut {
		key := r.Header.Get("Idempotency-Key")
		if !keyPattern.MatchString(key) {
			fail(w, 400, "INVALID_REQUEST")
			return
		}
		var input settings.ReceiptUpdate
		if !decode(w, r, &input) {
			return
		}
		result, err = a.Settings.ReplaceReceipt(ctx, tenant, token, key, input)
	} else {
		result, err = a.Settings.Receipt(ctx, tenant, token)
	}
	switch {
	case err == nil:
		respond(w, 200, result)
	case errors.Is(err, settings.ErrInput):
		fail(w, 400, "INVALID_REQUEST")
	case errors.Is(err, settings.ErrConflict):
		fail(w, 409, "RECEIPT_SETTINGS_CONFLICT")
	default:
		handleError(w, err)
	}
}
