package transport

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"

	"hi-ve/backend/internal/devices"
)

const operatorCookie = "__Host-hive-operator"

func (a API) operationalRoutes(mux *http.ServeMux) {
	const base = "/api/v1/operator/tenants/{tenant_id}/devices/{device_id}"
	if a.Devices.PIN != nil {
		mux.HandleFunc("GET "+base+"/profiles", a.origin(a.AppOrigin, a.operatorProfiles))
	}
	mux.HandleFunc("OPTIONS /api/v1/operator/{rest...}", a.origin(a.AppOrigin, preflight))
	mux.HandleFunc("POST "+base+"/challenges", a.origin(a.AppOrigin, a.operationChallenge))
	mux.HandleFunc("GET "+base+"/state", a.origin(a.AppOrigin, a.useOperation("state")))
	mux.HandleFunc("GET "+base+"/context", a.origin(a.AppOrigin, a.useOperation("context.read")))
	mux.HandleFunc("POST "+base+"/context/logout", a.origin(a.AppOrigin, a.useOperation("context.logout")))
	mux.HandleFunc("PUT /api/v1/app/tenants/{tenant_id}/devices/{device_id}/operator-assignments", a.origin(a.AppOrigin, a.replaceOperators))
}

func (a API) operationChallenge(w http.ResponseWriter, r *http.Request) {
	tenant, device := r.PathValue("tenant_id"), r.PathValue("device_id")
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	peer, err := netip.ParseAddr(host)
	if err != nil || !uuid.MatchString(tenant) || !uuid.MatchString(device) || r.URL.RawQuery != "" {
		deviceError(w, devices.ErrInput)
		return
	}
	var target devices.OperationTarget
	if !decode(w, r, &target) {
		return
	}
	result, err := a.Devices.StartOperation(r.Context(), tenant, device, peer, target, r.Header.Get("Device-Proof"))
	if err != nil {
		deviceError(w, err)
		return
	}
	respond(w, 201, result)
}

func (a API) useOperation(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, device := r.PathValue("tenant_id"), r.PathValue("device_id")
		if !uuid.MatchString(tenant) || !uuid.MatchString(device) || r.URL.RawQuery != "" {
			deviceError(w, devices.ErrInput)
			return
		}
		proof := r.Header.Get("Device-Proof")
		if len(proof) == 0 || len(proof) > 2048 {
			deviceError(w, devices.ErrProof)
			return
		}
		state, current, err := a.Devices.UseOperation(r.Context(), tenant, device, operation, proof, cookie(r, operatorCookie))
		if err != nil {
			deviceError(w, err)
			return
		}
		switch operation {
		case "state":
			respond(w, 200, state)
		case "context.read":
			respond(w, 200, current)
		case "context.logout":
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func (a API) replaceOperators(w http.ResponseWriter, r *http.Request) {
	tenant, device, key := r.PathValue("tenant_id"), r.PathValue("device_id"), r.Header.Get("Idempotency-Key")
	if !uuid.MatchString(tenant) || !uuid.MatchString(device) || !keyPattern.MatchString(key) {
		deviceError(w, devices.ErrInput)
		return
	}
	var in struct {
		Expected    int64                        `json:"expected_revision"`
		Assignments []devices.OperatorAssignment `json:"assignments"`
	}
	if !decode(w, r, &in) {
		return
	}
	result, err := a.Devices.ReplaceOperators(r.Context(), tenant, cookie(r, tenantCookie), device, key, in.Expected, in.Assignments)
	if err != nil {
		deviceError(w, err)
		return
	}
	respond(w, 200, result)
}

func (a API) operatorProfiles(w http.ResponseWriter, r *http.Request) {
	tenant, device, outlet, after := r.PathValue("tenant_id"), r.PathValue("device_id"), r.URL.Query().Get("outlet_id"), r.URL.Query().Get("page_after")
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !uuid.MatchString(tenant) || !uuid.MatchString(device) || !uuid.MatchString(outlet) ||
		(after != "" && !uuid.MatchString(after)) || len(query["outlet_id"]) != 1 || len(query["page_after"]) > 1 ||
		len(query) > 1+len(query["page_after"]) {
		deviceError(w, devices.ErrInput)
		return
	}
	page, err := a.Devices.Profiles(r.Context(), tenant, device, outlet, after, r.Header.Get("Device-Proof"))
	if err != nil {
		deviceError(w, err)
		return
	}
	respond(w, 200, page)
}
