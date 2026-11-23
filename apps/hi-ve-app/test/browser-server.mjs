import { createHash, createPublicKey, randomUUID, verify } from "node:crypto";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";

const tenant = "90000000-0000-4000-8000-000000000001";
const outlet = "90000000-0000-4000-8000-000000000002";
const device = "90000000-0000-4000-8000-000000000003";
const actor = "90000000-0000-4000-8000-000000000004";
const context = "90000000-0000-4000-8000-000000000005";
const sku = "90000000-0000-4000-8000-000000000006";
const price = "90000000-0000-4000-8000-000000000007";
let state;
function reset() {
  state = { orders: new Map(), challenges: new Map(), key: null, context: false, puts: 0, acks: 0, logouts: 0, mode: "" };
}
reset();
const op = `/api/v1/operator/tenants/${tenant}/devices/${device}`;
const sales = `${op}/locations/${outlet}/sales`;
const types = { html: "text/html", mjs: "text/javascript", js: "text/javascript", css: "text/css",
  png: "image/png", webmanifest: "application/manifest+json" };
const time = () => new Date().toISOString();

async function rawBody(req) {
  let body = "";
  for await (const chunk of req) body += chunk;
  return body;
}
function send(res, status, body) {
  res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" });
  res.end(body === undefined ? "" : JSON.stringify(body));
}
function signed(token) {
  if (!state.key || !token) return null;
  const [header, body, signature] = token.split(".");
  try {
    if (!verify("sha256", Buffer.from(`${header}.${body}`),
      { key: state.key, dsaEncoding: "ieee-p1363" }, Buffer.from(signature, "base64url"))) return null;
    return JSON.parse(Buffer.from(body, "base64url").toString());
  } catch { return null; }
}
function compatible(claims, target) {
  return ["operation", "context_id", "expected_revision", "command_digest", "outlet_id", "member_id", "page_after"]
    .every(key => claims[key] === target[key]);
}
async function api(req, res, url) {
  const { pathname } = url;
  const source = await rawBody(req);
  const body = source ? JSON.parse(source) : {};
  const proof = req.headers["device-proof"];
  const error = (status, code) => send(res, status, { error: { code } });
  if (pathname === "/api/v1/devices/pairings") {
    state.key = createPublicKey({ key: body.public_key_jwk, format: "jwk" });
    return send(res, 201, { pairing_id: "a".repeat(48), code: "12345678", nonce: "pair-nonce", expires_at: time() });
  }
  if (pathname.endsWith("/redeem")) {
    const claims = signed(body.proof);
    if (claims?.purpose !== "hive.device.pair.redeem.v1" || claims.idempotency_key !== req.headers["idempotency-key"]) {
      return error(401, "INVALID_PAIRING_PROOF");
    }
    return send(res, 200, { device_id: device });
  }
  if (!pathname.startsWith(op)) return error(404, "NOT_FOUND");
  if (state.mode === "api_unavailable") return error(503, "SERVICE_UNAVAILABLE");
  if (state.mode === "revoked" || state.mode === "suspended") return error(401, "INVALID_PAIRING_PROOF");
  const claims = signed(proof);
  if (pathname === op + "/challenges") {
    if (!claims || claims.tenant_id !== tenant || claims.device_id !== device ||
        claims.purpose !== "hive.device.challenge.bootstrap.v1" || !compatible(claims, body)) {
      return error(401, "INVALID_PAIRING_PROOF");
    }
    const challenge = { challenge_id: randomUUID(), nonce: randomUUID(), device_revision: 1, expires_at: time() };
    state.challenges.set(challenge.challenge_id, { challenge, target: body });
    return send(res, 201, challenge);
  }
  const saved = state.challenges.get(claims?.challenge_id);
  if (!saved || claims.purpose !== "hive.device.operation.v1" || claims.tenant_id !== tenant ||
      claims.device_id !== device || !compatible(claims, saved.target) ||
      Object.keys(saved.challenge).some(key => claims[key] !== saved.challenge[key])) {
    return error(401, "INVALID_PAIRING_PROOF");
  }
  state.challenges.delete(claims.challenge_id);
  if (claims.operation === "state" && pathname.endsWith("/state")) {
    return send(res, 200, { context_revision: state.context ? 1 : 0, last_outlet_id: outlet });
  }
  if (claims.operation === "profiles" && claims.outlet_id === outlet && url.searchParams.get("outlet_id") === outlet) {
    return send(res, 200, { profiles: [{ member_id: actor, display_name: "Operator Uji" }] });
  }
  if (claims.operation === "context.login" && claims.outlet_id === outlet && claims.member_id === actor &&
      claims.member_id === body.member_id && body.pin === "654321") {
    state.context = true;
    return send(res, 201, { context_id: context, member_id: actor, outlet_id: outlet, revision: 1 });
  }
  if (!state.context || claims.context_id !== context || claims.expected_revision !== 1 || state.mode === "session_expired") {
    return error(401, "INVALID_PAIRING_PROOF");
  }
  if (pathname.endsWith("/context/logout") && claims.operation === "context.logout") {
    state.context = false;
    state.logouts++;
    return send(res, 204);
  }
  if (claims.operation === "context.read") {
    if (pathname === op + "/context") return send(res, 200, { context_id: context, member_id: actor, outlet_id: outlet, revision: 1 });
    if (pathname === sales + "/items/search") return send(res, 200, [
      { sku_id: sku, code: "madu", name: "Madu Randu", variant: "Botol", unit: "pcs", batch_tracked: false }
    ]);
    if (pathname.startsWith(op + "/catalog/skus/")) return send(res, 200, { price_id: price, amount: "12.50", revision: 1, updated_at: time() });
    if (pathname === sales) return send(res, 200, [...state.orders.values()].map(row => row.receipt));
    if (pathname.endsWith("/receipt")) {
      const record = state.orders.get(pathname.split("/").at(-2));
      return record ? send(res, 200, record.receipt) : error(404, "NOT_FOUND");
    }
  }
  if (claims.operation === "sales.write" && pathname.startsWith(sales + "/")) {
    const key = req.headers["idempotency-key"];
    const digest = createHash("sha256").update([req.method, pathname, key, source].join("\n")).digest("hex");
    if (digest !== claims.command_digest) return error(401, "INVALID_PAIRING_PROOF");
    const [, id, suffix] = pathname.slice(sales.length).split("/");
    if (suffix === "ack") {
      state.acks++;
      if (state.mode === "entitlement_expired") return error(403, "ENTITLEMENT_DENIED");
      const order = state.orders.get(id);
      if (!order || order.body !== source) return error(404, "NOT_FOUND");
      const { receipt } = order;
      return send(res, 200, { id, outlet_id: outlet, operator_id: actor, payment_state: "cash_received", posted_at: receipt.created_at });
    }
    if (suffix === "confirm-manual") {
      const record = state.orders.get(id);
      if (!record) return error(404, "NOT_FOUND");
      record.receipt.payment_state = "manually_confirmed";
      return send(res, 200, record.receipt);
    }
    state.puts++;
    if (state.mode === "shortage") return error(409, "INSUFFICIENT_STOCK");
    if (state.mode === "stale") return error(409, "STATE_CONFLICT");
    if (state.mode === "entitlement_expired") return error(403, "ENTITLEMENT_DENIED");
    const receipt = { id, outlet_id: outlet, operator_id: actor, created_at: time(), total: "12.50",
      change: body.method === "cash" ? "7.50" : null, outlet_name: "Outlet Uji",
      payment_state: body.method === "cash" ? "cash_received" : "pending_manual_confirmation",
      items: [{ name: "Madu Randu", quantity: "1", unit: "pcs", net: "12.50" }], receipt_footer: "Terima kasih" };
    if (state.mode !== "lose_before_commit") state.orders.set(id, { body: source, receipt });
    if (["lose_response", "lose_before_commit"].includes(state.mode)) {
      res.writeHead(200, { "Content-Type": "application/json", "Content-Length": "1024" });
      res.write("{");
      // Send incomplete headers/body after the simulated commit. Chromium must
      // observe a failed response rather than silently retry an unacknowledged PUT.
      setTimeout(() => req.socket.destroy(), 20);
      return;
    }
    return send(res, 200, receipt);
  }
  return error(404, "NOT_FOUND");
}

createServer(async (req, res) => {
  const url = new URL(req.url, "http://127.0.0.1");
  try {
    if (url.pathname === "/__fixture/blank") { res.writeHead(200, { "Content-Type": "text/html" }); return res.end("<title>Fixture</title>"); }
    if (url.pathname === "/__fixture/state") {
      if (req.method === "POST") {
        const body = JSON.parse(await rawBody(req));
        if (body.reset) reset();
        if (body.mode !== undefined) state.mode = body.mode;
      }
      return send(res, 200, { puts: state.puts, acks: state.acks, orders: state.orders.size, logouts: state.logouts });
    }
    if (url.pathname.startsWith("/api/")) return await api(req, res, url);
    if (url.pathname === "/index.html") {
      res.writeHead(307, { Location: "/" });
      return res.end();
    }
    const path = url.pathname === "/" ? "index.html" : url.pathname.slice(1);
    if (!/^[a-z0-9/._-]+$/i.test(path) || path.includes("..") || path === "_headers") return send(res, 404, {});
    const body = await readFile(new URL(`../dist/${path}`, import.meta.url));
    res.writeHead(200, { "Content-Type": types[path.split(".").at(-1)] ?? "application/octet-stream" });
    res.end(body);
  } catch {
    if (!res.headersSent) send(res, 404, { error: { code: "NOT_FOUND" } });
    else res.end();
  }
}).listen(4179, "127.0.0.1");
