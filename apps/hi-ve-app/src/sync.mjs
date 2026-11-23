import { addCash, listCash, updateCash } from "./storage.mjs";

export function cashPayload(items, tendered) {
  return {
    method: "cash",
    tendered,
    items: items.map(({ sku_id, batch_id, quantity, unit, price_id, discount }) => ({
      sku_id, ...(batch_id ? { batch_id } : {}), quantity, unit, price_id, discount
    }))
  };
}

export function matchesACK(record, ack) {
  return ack?.id === record.id && ack.outlet_id === record.outlet && ack.operator_id === record.operator &&
    ack.payment_state === "cash_received" && typeof ack.posted_at === "string" && Number.isFinite(Date.parse(ack.posted_at));
}

export async function stageCash(scope, items, tendered, source, uuid = crypto.randomUUID()) {
  if (source !== "online_attempt" && source !== "offline_draft") throw new TypeError("Sumber antrean tidak sah");
  const record = {
    id: uuid,
    tenant: scope.tenant, device: scope.device, operator: scope.operator, outlet: scope.outlet,
    source, state: "pending", recordedAt: new Date().toISOString(),
    body: cashPayload(items, tendered),
    snapshots: items.map(({ name, variant, unitPrice, priceUpdatedAt }) => ({ name, variant, unitPrice, priceUpdatedAt }))
  };
  await addCash(record);
  return record;
}

export async function recordACK(record, ack) {
  if (record.source !== "online_attempt" || !matchesACK(record, ack)) throw new Error("ACK server tidak cocok dengan bukti lokal");
  const saved = await updateCash(record.id, original => ({
    ...original, state: "synced", reason: null,
    acknowledgedAt: new Date().toISOString(), serverPostedAt: ack.posted_at
  }));
  if (!saved) throw new Error("Bukti lokal hilang sebelum ACK dapat disimpan");
  return saved;
}

export function failureReason(error) {
  if (error?.code === "INSUFFICIENT_STOCK") return "Stok tidak cukup. Bukti disimpan untuk ditinjau.";
  if (error?.code === "STATE_CONFLICT") return "Harga, cakupan, atau isi transaksi berubah. Perlu tinjauan.";
  if (error?.status === 401 || error?.status === 403) return "Sesi, perangkat, tenant, atau izin tidak lagi berlaku.";
  if (error?.status === 404) return "Server belum mengembalikan bukti transaksi yang cocok.";
  if (error?.status === 400) return "Server menolak input transaksi. Bukti lokal tetap tersimpan.";
  if (error?.status === 429) return "Server membatasi permintaan. Coba lagi nanti.";
  return "Komunikasi server belum pasti. Bukti lokal tetap tersimpan.";
}

export async function recordFailure(record, error) {
  const terminal = [400, 401, 403, 409].includes(error?.status);
  return updateCash(record.id, original => ({
    ...original, state: terminal ? (error.status === 400 ? "failed" : "needs_review") : "pending",
    reason: failureReason(error)
  }));
}

// Reconnect only looks up persisted orders. It never posts offline drafts or
// creates a second sale when the original network outcome is unknown.
export async function reconcileCash(scope, lookup) {
  const rows = await listCash(scope);
  const candidates = rows.filter(row => row.source === "online_attempt" && ["pending", "needs_review"].includes(row.state));
  candidates.sort((a, b) => (a.lastCheckedAt ?? "").localeCompare(b.lastCheckedAt ?? "") ||
    a.recordedAt.localeCompare(b.recordedAt));
  for (const record of candidates.slice(0, 10)) {
    let attempted = false;
    try {
      const saved = await updateCash(record.id, original => ({ ...original, lastCheckedAt: new Date().toISOString() }));
      if (!saved || saved.state === "synced" || saved.state === "failed") continue;
      attempted = true;
      const ack = await lookup(record);
      await recordACK(record, ack);
    } catch (error) {
      if (!attempted) throw error;
      if (!error.status || error.status === 429 || error.status >= 500) {
        await recordFailure(record, error);
        break;
      }
      await updateCash(record.id, original => ({
        ...original, state: "needs_review", reason: failureReason(error)
      }));
      if (error.status === 401 || error.status === 403) break;
    }
  }
}
