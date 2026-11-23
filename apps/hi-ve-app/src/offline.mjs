import { deviceBase, request } from "./api.mjs";
import { pairingKey } from "./devices/pairing-key.mjs";
import { deleteLogin, listCash, pendingLogins, readMeta, updateCash, writeJournal } from "./storage.mjs";
import { cashPayload } from "./sync.mjs";

export const iterations = 600_000;
const hour = 60 * 60 * 1000;
const encoder = new TextEncoder();
const decoder = new TextDecoder();
let anchor, active, sending;

const exclusive = work => {
  if (!navigator.locks) throw new Error("Browser ini tidak mendukung penguncian jurnal offline");
  return navigator.locks.request("hi-ve-cash-journal", work);
};

function bytes(encoded) {
  return Uint8Array.from(atob(encoded.replaceAll("-", "+").replaceAll("_", "/")), ch => ch.charCodeAt(0));
}

async function verified(signed) {
  const key = await crypto.subtle.importKey("raw", bytes(signed.public_key), "Ed25519", false, ["verify"]);
  if (!await crypto.subtle.verify("Ed25519", key, bytes(signed.signature), encoder.encode("hive.offline.lease.v1." + signed.payload))) {
    throw new Error("Tanda tangan lease tidak sah");
  }
  return JSON.parse(decoder.decode(bytes(signed.payload)));
}

export function trustedTime(base, wall, monotonic, last) {
  const elapsed = monotonic - base.monotonic;
  const adjustedWall = base.server + wall - base.wall;
  if (elapsed < 0 || wall < last.wall || adjustedWall + 1000 < base.server + elapsed) {
    throw new Error("Perubahan jam terdeteksi. Sambungkan kembali untuk memverifikasi waktu.");
  }
  return Math.max(base.server + elapsed, adjustedWall, last.time);
}

function timeFor(journal) {
  if (anchor?.id !== journal.id || anchor.claims !== JSON.stringify(journal.claims) ||
      anchor.payload !== journal.signed.payload || anchor.publicKey !== journal.signed.public_key || journal.sealed || journal.denied) throw new Error("Akses offline perlu verifikasi online");
  const wall = Date.now();
  const now = trustedTime(anchor, wall, performance.now(), journal.last);
  if (now >= Date.parse(journal.claims.expires_at)) throw new Error("Izin offline kedaluwarsa");
  return { time: Math.floor(now), wall };
}

async function derive(pin, salt, binding) {
  const key = await crypto.subtle.importKey("raw", encoder.encode(pin), "PBKDF2", false, ["deriveBits"]);
  const input = new Uint8Array(salt.length + encoder.encode(binding).length);
  input.set(salt);
  input.set(encoder.encode(binding), salt.length);
  return crypto.subtle.deriveBits({ name: "PBKDF2", hash: "SHA-256", salt: input, iterations }, key, 256);
}

export async function enroll(signed, scope, pin) {
  return exclusive(async () => {
    const l = await verified(signed);
    const server = Date.parse(signed.server_time);
    const issued = Date.parse(l.issued_at);
    const expires = Date.parse(l.expires_at);
    if (!l.id || !Number.isFinite(server) || !(issued <= server && server < expires && expires - issued <= 24 * hour) ||
        l.tenant_id !== scope.tenant || l.device_id !== scope.device || l.context_id !== scope.context ||
        l.context_revision !== scope.revision || l.operator_id !== scope.operator || l.outlet_id !== scope.outlet ||
        !/^\d{6}$/.test(pin)) throw new Error("Cakupan izin offline tidak sah");
    const before = await readMeta("offline-lease");
    if (before?.id === l.id) {
      await refresh(signed, l, before, server);
      return;
    }
    if (before && !before.sealed) throw new Error("Jurnal sebelumnya masih terbuka");
    const salt = crypto.getRandomValues(new Uint8Array(32));
    const verifierKey = await crypto.subtle.generateKey({ name: "HMAC", hash: "SHA-256" }, false, ["sign", "verify"]);
    const verifier = await crypto.subtle.sign("HMAC", verifierKey, await derive(pin, salt, l.id));
    anchor = anchorFor(l, signed, server);
    await writeJournal(before?.id ?? null, {
      id: l.id, claims: l, signed, salt, verifierKey, verifier, failures: 0, lockedUntil: 0, sequence: 0,
      quotes: [], last: { wall: anchor.wall, time: server }
    });
  });
}

function anchorFor(claims, signed, server) {
  return { id: claims.id, claims: JSON.stringify(claims), payload: signed.payload, publicKey: signed.public_key,
    server, wall: Date.now(), monotonic: performance.now() };
}

async function refresh(signed, claims, before, server) {
  if (before.denied || before.sealed || before.signed.public_key !== signed.public_key ||
      before.signed.payload !== signed.payload || !Number.isFinite(server) || server < before.last.time ||
      server >= Date.parse(claims.expires_at)) throw new Error("Jurnal offline perlu verifikasi Owner");
  anchor = anchorFor(claims, signed, server);
  await writeJournal(claims.id, { ...before, last: { wall: anchor.wall, time: server } });
}

export async function reanchor(scope, load) {
  if (!navigator.locks) return;
  const prior = await readMeta("offline-lease");
  if (!prior || prior.sealed || prior.denied || prior.claims.context_id !== scope.context || anchor?.id === prior.id) return;
  const signed = await load();
  await exclusive(async () => {
    const before = await readMeta("offline-lease");
    if (before?.id !== prior.id) return;
    const claims = await verified(signed);
    await refresh(signed, claims, before, Date.parse(signed.server_time));
  });
}

async function envelope(journal, kind, data, id = crypto.randomUUID()) {
  const scope = installationScope(journal.claims);
  const body = JSON.stringify(data);
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", encoder.encode(body)));
  const hash = Array.from(digest, byte => byte.toString(16).padStart(2, "0")).join("");
  const key = await pairingKey();
  const proof = await key.journalProof(kind, scope.tenant, scope.device, journal.id, id, hash);
  return { id, lease: journal.id, proof, body };
}

function installationScope(claims) {
  return { tenant: claims.tenant_id, device: claims.device_id, outlet: claims.outlet_id, operator: claims.operator_id,
    context: claims.context_id, revision: claims.context_revision };
}

export async function offlineLogin(pin, installation) {
  active = undefined;
  if (!/^\d{6}$/.test(pin)) throw new Error("Masukkan PIN enam digit");
  return exclusive(async () => {
    const original = await readMeta("offline-lease");
    if (!original || original.claims.device_id !== installation.device || original.claims.tenant_id !== installation.tenant ||
        original.claims.outlet_id !== installation.outlet) throw new Error("Izin offline belum tersedia untuk outlet ini");
    const l = await verified(original.signed);
    if (JSON.stringify(l) !== JSON.stringify(original.claims)) throw new Error("Isi lease berubah");
    const now = timeFor(original);
    if (original.lockedUntil > now.time) throw new Error("PIN dikunci sementara");
    // Consume this attempt before KDF. Crashes cannot provide additional guesses.
    const count = original.lockedUntil ? 1 : original.failures + 1;
    const attempt = { ...original, last: now, failures: count, lockedUntil: count >= 5 ? now.time + 15 * 60_000 : 0 };
    const logged = await envelope(attempt, "login", {
      occurred_at: new Date(now.time).toISOString(), outcome: count >= 5 ? "lockout" : "denied"
    });
    await writeJournal(original.id, attempt, "login_events", logged);
    const good = await crypto.subtle.verify("HMAC", original.verifierKey, original.verifier, await derive(pin, original.salt, original.id));
    if (!good) throw new Error(count >= 5 ? "PIN dikunci selama 15 menit" : "PIN tidak sesuai");
    const eventAt = timeFor(attempt);
    const success = await envelope(attempt, "login", {
      occurred_at: new Date(eventAt.time).toISOString(), outcome: "success"
    }, logged.id);
    await writeJournal(attempt.id, { ...attempt, last: eventAt, failures: 0, lockedUntil: 0 }, "login_events", success, true);
    active = attempt.id;
    return installationScope(attempt.claims);
  });
}

export function lockOffline() { active = undefined; }

export function clearAnchor() { anchor = undefined; lockOffline(); }

export async function denyOffline() {
  clearAnchor();
  if (!navigator.locks) return;
  await exclusive(async () => {
    const journal = await readMeta("offline-lease");
    if (journal && !journal.denied) await writeJournal(journal.id, { ...journal, denied: true });
  });
}

async function currentJournal(requiredActive = true) {
  const journal = await readMeta("offline-lease");
  if (!journal || requiredActive && active !== journal.id) throw new Error("Masuk dengan PIN offline sebelum melanjutkan");
  const now = timeFor(journal);
  if (requiredActive && journal.lockedUntil > now.time) throw new Error("PIN dikunci sementara");
  return [journal, now];
}

export async function cacheQuote(scope, item) {
  if (!anchor || !navigator.locks) return;
  await exclusive(async () => {
    const [journal, last] = await currentJournal(false);
    if (journal.claims.context_id !== scope.context) return;
    const { sku_id, code, barcode, name, variant, unit, price_id, unitPrice, priceUpdatedAt, batch_tracked } = item;
    const quotes = journal.quotes.filter(q => q.sku_id !== sku_id).slice(-199);
    quotes.push({ sku_id, code, barcode, name, variant, unit, price_id, unitPrice, priceUpdatedAt, batch_tracked });
    await writeJournal(journal.id, { ...journal, last, quotes });
  });
}

export async function searchQuotes(query) {
  return exclusive(async () => {
    const [journal] = await currentJournal();
    const value = query.toLowerCase();
    return journal.quotes.filter(item => [item.name, item.code, item.barcode].some(v => v?.toLowerCase().includes(value)));
  });
}

export async function saveOfflineSale(scope, items, tendered) {
  return exclusive(async () => {
    const [journal, last] = await currentJournal();
    if (journal.claims.context_id !== scope.context || journal.claims.outlet_id !== scope.outlet) throw new Error("Sesi offline tidak cocok");
    if (!journal.claims.discount_allowed && items.some(item => /[1-9]/.test(item.discount))) throw new Error("Izin offline tidak mencakup diskon");
    const checkout = cashPayload(items, tendered);
    const recordedAt = new Date(last.time).toISOString();
    const sequence = journal.sequence + 1;
    const packet = await envelope(journal, "sale", { sequence, occurred_at: recordedAt, checkout, quote_amounts: items.map(item => item.unitPrice) });
    const record = {
      id: packet.id, tenant: scope.tenant, device: scope.device, operator: scope.operator, outlet: scope.outlet,
      source: "offline_sale", state: "pending", recordedAt, body: checkout, packet, sequence,
      snapshots: items.map(({ name, variant, unitPrice, priceUpdatedAt }) => ({ name, variant, unitPrice, priceUpdatedAt }))
    };
    await writeJournal(journal.id, { ...journal, last, sequence }, "sales", record);
    return record;
  });
}

function root(scope, lease) { return deviceBase(scope) + "/offline/leases/" + lease; }

async function submit(scope, path, packet) {
  return request(root(scope, packet.lease) + path, { method: "PUT", body: packet.body, proof: packet.proof });
}

export async function flushOffline(installation) {
  if (sending) return sending;
  sending = (async () => {
    const journal = await readMeta("offline-lease");
    if (!journal || journal.claims.device_id !== installation.device || journal.claims.tenant_id !== installation.tenant) return;
    const scope = installationScope(journal.claims);
    const audit = async () => {
      for (const event of (await exclusive(pendingLogins)).slice(0, 50)) {
        const ack = await submit(scope, "/logins/" + event.id, event);
        if (ack?.id !== event.id || ack.lease_id !== event.lease || ack.state !== "recorded") throw new Error("ACK login tidak cocok");
        await deleteLogin(event.id);
      }
    };
    const sales = async () => {
      const records = (await listCash(scope)).filter(r => r.source === "offline_sale");
      const pending = records.filter(r => !r.offlineACK).sort((a, b) => a.sequence - b.sequence).slice(0, 25);
      if (pending.length === 0) {
        pending.push(...records.filter(r => r.offlineACK === "needs_review").sort((a, b) =>
          (a.acknowledgedAt ?? "").localeCompare(b.acknowledgedAt ?? "")).slice(0, 5));
      }
      for (const row of pending) {
        const ack = await submit(scope, "/sales/" + row.id, row.packet);
        if (ack?.id !== row.id || ack.lease_id !== row.packet.lease || !["posted", "needs_review", "manual_refund_recorded"].includes(ack.state) ||
            !Number.isFinite(Date.parse(ack.received_at))) throw new Error("ACK penjualan tidak cocok");
        await updateCash(row.id, before => ({ ...before, offlineACK: ack.state, state: ack.state === "posted" ? "synced" : ack.state,
          reason: ack.reason, acknowledgedAt: new Date().toISOString(), serverReceivedAt: ack.received_at }));
      }
    };
    const results = await Promise.allSettled([audit(), sales()]);
    const failure = results.find(result => result.status === "rejected");
    if (failure) throw failure.reason;
  })().finally(() => { sending = undefined; });
  return sending;
}

export async function sealOffline(installation) {
  const journal = await exclusive(async () => {
    const previous = await readMeta("offline-lease");
    if (!previous || previous.claims.device_id !== installation.device || previous.claims.tenant_id !== installation.tenant) {
      throw new Error("Jurnal perangkat tidak tersedia");
    }
    if (previous.seal) return previous;
    const evidence = await listCash(installationScope(previous.claims));
    if (evidence.some(row => row.source === "offline_draft" || row.source === "online_attempt" && row.state !== "synced")) {
      throw new Error("Bukti checkout lama masih memerlukan tinjauan sebelum jurnal dapat disegel");
    }
    const seal = await envelope(previous, "seal", { sale_count: previous.sequence }, previous.id);
    const frozen = { ...previous, sealed: true, seal };
    await writeJournal(previous.id, frozen);
    lockOffline();
    return frozen;
  });
  await flushOffline(installation);
  await submit(installation, "/seal", journal.seal);
}
