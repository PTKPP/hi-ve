package transport

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"regexp"

	"hi-ve/backend/internal/devices"
)

var pairingID = regexp.MustCompile(`^[0-9a-f]{48}$`)

func (a API) deviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("OPTIONS /api/v1/devices/{rest...}", a.origin(a.AppOrigin, preflight))
	mux.HandleFunc("POST /api/v1/devices/pairings", a.origin(a.AppOrigin, a.startPairing))
	mux.HandleFunc("POST /api/v1/devices/pairings/{pairing_id}/redeem", a.origin(a.AppOrigin, a.redeemPairing))
	mux.HandleFunc("POST /api/v1/app/tenants/{tenant_id}/device-pairings/{pairing_id}/approve", a.origin(a.AppOrigin, a.approvePairing))
	mux.HandleFunc("POST /api/v1/app/tenants/{tenant_id}/devices/{device_id}/revoke", a.origin(a.AppOrigin, a.revokeDevice))
}

func deviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, devices.ErrInput):
		fail(w, http.StatusBadRequest, "INVALID_REQUEST")
	case errors.Is(err, devices.ErrProof):
		fail(w, http.StatusUnauthorized, "INVALID_PAIRING_PROOF")
	case errors.Is(err, devices.ErrLimited):
		fail(w, http.StatusTooManyRequests, "RATE_LIMITED")
	case errors.Is(err, devices.ErrUnavailable):
		fail(w, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, devices.ErrConflict):
		fail(w, http.StatusConflict, "STATE_CONFLICT")
	default:
		handleError(w, err)
	}
}

func (a API) startPairing(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	peer, err := netip.ParseAddr(host)
	if !keyPattern.MatchString(key) || err != nil {
		deviceError(w, devices.ErrInput)
		return
	}
	var input struct {
		Key devices.PublicKey `json:"public_key_jwk"`
	}
	if !decode(w, r, &input) {
		return
	}
	result, err := a.Devices.Start(r.Context(), peer, key, input.Key)
	if err != nil {
		deviceError(w, err)
		return
	}
	respond(w, http.StatusCreated, result)
}

func (a API) approvePairing(w http.ResponseWriter, r *http.Request) {
	tenant, id, key := r.PathValue("tenant_id"), r.PathValue("pairing_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !pairingID.MatchString(id) || !keyPattern.MatchString(key) {
		deviceError(w, devices.ErrInput)
		return
	}
	var input devices.Approval
	if !decode(w, r, &input) {
		return
	}
	if err := a.Devices.Approve(r.Context(), tenant, cookie(r, tenantCookie), id, key, input); err != nil {
		deviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a API) redeemPairing(w http.ResponseWriter, r *http.Request) {
	id, key := r.PathValue("pairing_id"), r.Header.Get("Idempotency-Key")
	if !pairingID.MatchString(id) || !keyPattern.MatchString(key) {
		deviceError(w, devices.ErrInput)
		return
	}
	var input struct {
		Proof string `json:"proof"`
	}
	if !decode(w, r, &input) {
		return
	}
	result, err := a.Devices.Redeem(r.Context(), id, key, input.Proof)
	if err != nil {
		deviceError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (a API) revokeDevice(w http.ResponseWriter, r *http.Request) {
	tenant, device, key := r.PathValue("tenant_id"), r.PathValue("device_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !uuid.MatchString(device) || !keyPattern.MatchString(key) {
		deviceError(w, devices.ErrInput)
		return
	}
	var input struct {
		Revision int64 `json:"expected_revision"`
	}
	if !decode(w, r, &input) {
		return
	}
	result, err := a.Devices.Revoke(r.Context(), tenant, cookie(r, tenantCookie), device, key, input.Revision)
	if err != nil {
		deviceError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
