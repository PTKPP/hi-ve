import { apiOrigin } from "./config.mjs";
import { pairingKey, catalogCommandDigest } from "./devices/pairing-key.mjs";

export class APIError extends Error {
  constructor(status, code) {
    super(code || "API_ERROR");
    this.status = status;
    this.code = code;
  }
}

export async function request(path, { method = "GET", body, proof, key } = {}) {
  const headers = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (proof) headers["Device-Proof"] = proof;
  if (key) headers["Idempotency-Key"] = key;
  const response = await fetch(apiOrigin + path, {
    method, body, headers, credentials: "include", cache: "no-store", redirect: "error",
    signal: AbortSignal.timeout(25000)
  });
  if (response.status === 204) return null;
  const payload = await response.json().catch(() => undefined);
  if (!response.ok) throw new APIError(response.status, payload?.error?.code ?? payload?.code ?? payload?.error);
  if (payload === undefined) throw new Error("Respons API tidak dapat dibaca");
  return payload;
}

export function deviceBase(scope) {
  return `/api/v1/operator/tenants/${scope.tenant}/devices/${scope.device}`;
}
export function salesBase(scope) {
  return `${deviceBase(scope)}/locations/${scope.outlet}/sales`;
}

export async function operation(scope, target, relativePath, options) {
  const keys = await pairingKey();
  const base = deviceBase(scope);
  const claims = { tenant_id: scope.tenant, device_id: scope.device, ...target };
  const bootstrap = await keys.challengeProof(claims);
  const challenge = await request(base + "/challenges", {
    method: "POST", body: JSON.stringify(target), proof: bootstrap
  });
  return request(base + relativePath, { ...options, proof: await keys.operationProof(challenge, claims) });
}

export function sessionOperation(scope, operationName, path, options) {
  return operation(scope, {
    operation: operationName, context_id: scope.context, expected_revision: scope.revision
  }, path, options);
}

export async function salesWrite(scope, method, path, payload, key) {
  const body = JSON.stringify(payload);
  const absolutePath = deviceBase(scope) + path;
  return operation(scope, {
    operation: "sales.write", context_id: scope.context, expected_revision: scope.revision,
    command_digest: await catalogCommandDigest(method, absolutePath, key, body)
  }, path, { method, body, key });
}

export function startPairing(key) {
  return pairingKey().then(keys => request("/api/v1/devices/pairings", {
    method: "POST", key, body: JSON.stringify({ public_key_jwk: keys.public_key_jwk })
  }));
}

export async function redeemPairing(pairing, key) {
  const keys = await pairingKey();
  return request(`/api/v1/devices/pairings/${pairing.pairing_id}/redeem`, {
    method: "POST", key, body: JSON.stringify({ proof: await keys.proof(pairing, key) })
  });
}
