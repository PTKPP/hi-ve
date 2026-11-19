package transport

import (
	"errors"
	"net/http"

	"hi-ve/backend/internal/locations"
)

func (a API) locationRoutes(mux *http.ServeMux) {
	const path = "/api/v1/app/tenants/{tenant_id}/locations"
	mux.HandleFunc("GET "+path, a.origin(a.AppOrigin, a.listLocations))
	mux.HandleFunc("POST "+path, a.origin(a.AppOrigin, a.createLocation))
}

func locationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, locations.ErrInput):
		fail(w, 400, "INVALID_REQUEST")
	case errors.Is(err, locations.ErrDuplicate):
		fail(w, 409, "STATE_CONFLICT")
	case errors.Is(err, locations.ErrReplay):
		fail(w, 409, "IDEMPOTENCY_CONFLICT")
	default:
		handleError(w, err)
	}
}

func (a API) listLocations(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant_id")
	if !uuid.MatchString(tenant) {
		locationError(w, locations.ErrInput)
		return
	}
	list, err := a.Locations.List(r.Context(), tenant, cookie(r, tenantCookie))
	if err != nil {
		locationError(w, err)
		return
	}
	respond(w, 200, map[string][]locations.Location{"locations": list})
}

func (a API) createLocation(w http.ResponseWriter, r *http.Request) {
	tenant, key := r.PathValue("tenant_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !keyPattern.MatchString(key) {
		locationError(w, locations.ErrInput)
		return
	}
	var body locations.CreateInput
	if !decode(w, r, &body) {
		return
	}
	row, err := a.Locations.Create(r.Context(), tenant, cookie(r, tenantCookie), key, body)
	if err != nil {
		locationError(w, err)
		return
	}
	respond(w, 201, row)
}
