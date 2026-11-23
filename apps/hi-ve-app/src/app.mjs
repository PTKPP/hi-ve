import { operation, sessionOperation, salesWrite, startPairing, redeemPairing } from "./api.mjs";
import { clearLockedSession, listCash, openStorage, readMeta, writeMeta } from "./storage.mjs";
import { cashPayload, stageCash, reconcileCash, recordACK, recordFailure, failureReason } from "./sync.mjs";
import { cacheQuote, clearAnchor, denyOffline, enroll, flushOffline, lockOffline, offlineLogin, reanchor,
  saveOfflineSale, searchQuotes, sealOffline } from "./offline.mjs";
import { formatMoney, lineTotal, minorUnits } from "./money.mjs";

const byId = id => document.getElementById(id);
const $ = new Proxy({}, { get: (_, id) => byId(id) });
const uuid = /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/;
const installationMarker = "hi-ve-app-has-installation";
const statusLabels = {
  pending: "Pending", synced: "Tersinkron", failed: "Gagal", needs_review: "Perlu tinjauan",
  pending_manual_confirmation: "Menunggu konfirmasi manual", manually_confirmed: "Dikonfirmasi manual",
  cash_received: "Tunai diterima", manual_refund_recorded: "Refund manual tercatat"
};

let installation, scope, pairing, pairStartKey, redeemKey;
let cart = [], profiles = [], history = [];
let apiReady = false, checkoutBusy = false, syncWork, reconnectWork, storageLost = false;
let manualAttempt, deferredInstall;
let sessionEpoch = 0, usingOffline = false, hasJournal = false;

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}
function say(text = "") {
  $.notice.textContent = text;
  $.notice.hidden = !text;
}
function checkAPIAvailability(error) {
  if (error instanceof TypeError || error?.name === "TimeoutError" || error?.status >= 500) {
    apiStatus("Tidak dapat dijangkau");
  }
}
function handler(fn) {
  return async event => {
    event.preventDefault();
    try { await fn(event); } catch (error) {
      checkAPIAvailability(error);
      if (error?.status === 401 || error?.status === 403) say("Akses ditolak oleh server. Periksa sesi dan izin.");
      else if (error?.status) say(failureReason(error));
      else say(error instanceof TypeError ? "Koneksi ke API tidak tersedia." : error.message);
    }
  };
}
function bind(id, event, fn) {
  byId(id).addEventListener(event, handler(fn));
}
function apiStatus(label, ready = false) {
  $.api.textContent = label;
  apiReady = ready;
  updateButtons();
}
function updateButtons() {
  $.network.textContent = navigator.onLine ? "Terhubung" : "Offline";
  const online = navigator.onLine && apiReady;
  $.checkout.disabled = checkoutBusy || storageLost || !scope || !cart.length || (!usingOffline && !online);
  $.checkout.textContent = usingOffline ? "Simpan penjualan tunai pending" : "Checkout";
  byId("offline-unlock").hidden = !installation || storageLost || online || usingOffline || !hasJournal;
  $.seal.hidden = !scope || !hasJournal || !online || usingOffline;
  $.pay.elements.method.options[1].disabled = usingOffline || !online;
  byId("save-draft").hidden = !scope || usingOffline || online;
  byId("save-draft").disabled = checkoutBusy || storageLost || !cart.length || $.pay.elements.method.value !== "cash";
  byId("tender-label").hidden = $.pay.elements.method.value !== "cash";
}
function showContext() {
  $.setup.hidden = Boolean(installation) || storageLost;
  $.login.hidden = !installation || Boolean(scope) || storageLost;
  $.workspace.hidden = !scope || storageLost;
  if (scope) {
    const operator = profiles.find(profile => profile.member_id === scope.operator);
    $.scope.textContent = `Outlet ${scope.outlet} · Operator ${operator?.display_name ?? scope.operator}`;
  } else {
    $.receipt.hidden = true;
    byId("receipt-data").replaceChildren();
    $.queue.replaceChildren();
    $.products.replaceChildren();
    byId("history-list").replaceChildren();
    byId("sync-status").textContent = "—";
    history = [];
    cart = [];
    manualAttempt = null;
  }
  renderCart();
}
function total() {
  return cart.reduce((sum, item) => sum + lineTotal(item.quantity, item.unitPrice, item.discount), 0n);
}
function renderTotal() {
  try { $.total.textContent = formatMoney(total()); }
  catch { $.total.textContent = "Periksa jumlah dan diskon"; }
  updateButtons();
}
function renderCart() {
  $.cart.replaceChildren();
  if (!cart.length) $.cart.append(element("p", "Keranjang kosong."));
  for (const item of cart) {
    const row = element("div", undefined, "row");
    row.append(element("strong", `${item.name} · ${item.variant}`));
    const fields = element("div", undefined, "line-fields");
    let batchChoices;
    for (const [field, label] of [["quantity", "Jumlah"], ["discount", "Diskon"], ["batch_id", "ID batch"]]) {
      if (field === "batch_id" && !item.batch_tracked) continue;
      const fieldLabel = element("label", label);
      const input = element("input");
      input.value = item[field] || "";
      input.inputMode = field === "batch_id" ? "text" : "decimal";
      if (field === "batch_id") {
        batchChoices = element("datalist");
        batchChoices.id = `batches-${item.rowId}`;
        input.setAttribute("list", batchChoices.id);
        fieldLabel.append(batchChoices);
      }
      input.addEventListener("input", () => { item[field] = input.value.trim(); renderTotal(); });
      fieldLabel.append(input);
      fields.append(fieldLabel);
    }
    const remove = element("button", "Hapus");
    remove.addEventListener("click", () => { cart = cart.filter(i => i !== item); renderCart(); });
    row.append(fields);
    if (batchChoices) {
      const loadBatches = element("button", "Muat batch tersedia");
      loadBatches.addEventListener("click", handler(async () => {
        if (!scope || !apiReady || !navigator.onLine) throw new Error("Daftar batch memerlukan API aktif");
        const current = scope;
        const rows = await sessionOperation(current, "context.read", `/locations/${current.outlet}/inventory/balances/${item.sku_id}`);
        if (scope !== current) return;
        batchChoices.replaceChildren();
        for (const batch of rows ?? []) {
          if (batch.bucket !== "available" || !uuid.test(batch.batch_id) || !/[1-9]/.test(batch.quantity)) continue;
          const option = element("option");
          option.value = batch.batch_id;
          option.label = `${batch.quantity} ${item.unit}`;
          batchChoices.append(option);
        }
        say(batchChoices.children.length ? "Pilih batch pada isian ID batch." : "Tidak ada batch tersedia.");
      }));
      row.append(loadBatches);
    }
    row.append(remove);
    $.cart.append(row);
  }
  renderTotal();
}
function checkoutPath(current) {
  return `/locations/${current.outlet}/sales`;
}
function fingerprintRequest(payload) {
  return JSON.stringify(payload);
}
function validateCheckout(method) {
  if (!scope || !cart.length || storageLost) throw new Error("Keranjang atau penyimpanan belum siap");
  for (const item of cart) {
    if (item.batch_tracked && !uuid.test(item.batch_id ?? "")) throw new Error("Masukkan ID batch yang sah");
  }
  const net = total();
  if (method === "cash") {
    const tendered = $.pay.elements.tendered.value.trim();
    if (minorUnits(tendered) < net) throw new Error("Uang diterima kurang dari total");
    return cashPayload(cart, tendered);
  }
  const body = cashPayload(cart, "");
  delete body.tendered;
  return { ...body, method };
}
function renderReceipt(data, local = false, queued = false) {
  const section = byId("receipt-data");
  section.replaceChildren();
  section.append(element("strong", local ? (queued ? "TUNAI PENDING — belum diterima server" : "DRAF LOKAL — belum diterima server") : statusLabels[data.payment_state] ?? data.payment_state));
  section.append(element("p", data.id));
  if (data.outlet_name || data.outlet_id) section.append(element("p", `Outlet ${data.outlet_name ?? data.outlet_id}`));
  if (data.operator_id) section.append(element("p", `Operator ${data.operator_id}`));
  if (data.created_at) section.append(element("p", new Date(data.created_at).toLocaleString("id-ID")));
  if (data.total) section.append(element("p", `Total ${formatMoney(minorUnits(data.total))}`));
  if (data.change !== undefined && data.change !== null) section.append(element("p", `Kembalian ${formatMoney(minorUnits(data.change))}`));
  for (const [idx, item] of (data.items ?? []).entries()) {
    const label = item.name ?? data.snapshots?.[idx]?.name ?? item.sku_id;
    section.append(element("p", `${label} · ${item.quantity} ${item.unit}${item.net ? " · " + formatMoney(minorUnits(item.net)) : ""}`));
  }
  if (data.receipt_footer) section.append(element("p", data.receipt_footer));
  $.receipt.hidden = false;
}

function renderLocalEvidence(row) {
  const items = row.body.items.map((item, index) => {
    const snapshot = row.snapshots[index];
    const net = lineTotal(item.quantity, snapshot.unitPrice, item.discount);
    return { ...item, name: snapshot.name, net };
  });
  return renderReceipt({
    id: row.id, outlet_id: row.outlet, operator_id: row.operator, created_at: row.recordedAt,
    items: items.map(item => ({ ...item, net: minorString(item.net) })),
    total: minorString(items.reduce((sum, item) => sum + item.net, 0n))
  }, true, row.source === "offline_sale");
}

function minorString(amount) {
  return `${amount / 100n}.${(amount % 100n).toString().padStart(2, "0")}`;
}

async function refreshQueue(current = scope) {
  if (!current) return;
  const rows = await listCash(current);
  if (current !== scope) return;
  $.queue.replaceChildren();
  if (!rows.length) $.queue.append(element("p", "Antrean kosong."));
  const pending = rows.filter(row => row.state === "pending").length;
  const review = rows.filter(row => ["failed", "needs_review"].includes(row.state)).length;
  byId("sync-status").textContent = `${pending} pending · ${review} tinjauan`;
  const last = rows.map(row => row.acknowledgedAt).filter(Boolean).sort().at(-1);
  byId("last-sync").textContent = last ? `Sinkronisasi terakhir ${new Date(last).toLocaleString("id-ID")}` : "Belum ada sinkronisasi.";
  for (const row of rows) {
    const block = element("div", undefined, "row");
    block.append(element("strong", statusLabels[row.state] ?? row.state));
    block.append(element("p", `${row.source === "offline_draft" ? "Draf lokal · " : ""}${row.id}`));
    if (row.reason) block.append(element("p", row.reason));
    if (row.state !== "synced") {
      const evidence = element("button", "Lihat bukti lokal");
      evidence.addEventListener("click", handler(() => renderLocalEvidence(row)));
      block.append(evidence);
    }
    $.queue.append(block);
  }
}
function sync() {
  if (syncWork || !scope || !apiReady || !navigator.onLine || checkoutBusy) return syncWork;
  const current = scope;
  syncWork = reconcileCash(current, record => {
    if (current !== scope) throw new Error("Sesi berubah");
    return salesWrite(current, "POST", `${checkoutPath(current)}/${record.id}/ack`, record.body, record.id).catch(error => {
      if (current === scope) checkAPIAvailability(error);
      throw error;
    });
  }).then(() => refreshQueue(current)).finally(() => { syncWork = undefined; });
  return syncWork;
}
async function recoverSession() {
  if (!installation || checkoutBusy) return;
  const epoch = sessionEpoch;
  const deviceState = await operation(installation, { operation: "state" }, "/state");
  if (epoch !== sessionEpoch) return;
  lockOffline();
  usingOffline = false;
  apiStatus("Tersedia", true);
  if (uuid.test(deviceState.last_outlet_id) && installation.outlet !== deviceState.last_outlet_id) {
    scope = undefined;
    clearAnchor();
    installation = { ...installation, outlet: deviceState.last_outlet_id };
    await writeMeta("installation", installation);
    await writeMeta("session", null);
    showContext();
    return;
  }
  const pointer = await readMeta("session");
  if (epoch !== sessionEpoch) return;
  if (!pointer || pointer.tenant !== installation.tenant || pointer.device !== installation.device) {
    if (scope) { scope = undefined; showContext(); }
    return;
  }
  if (pointer.locked) {
    if (scope) { scope = undefined; showContext(); }
    try { await sessionOperation(pointer, "context.logout", "/context/logout", { method: "POST" }); }
    catch (error) { if (![401, 403].includes(error.status)) throw error; }
    await clearLockedSession(pointer);
    return;
  }
  const live = await sessionOperation(pointer, "context.read", "/context");
  if (epoch !== sessionEpoch) return;
  if (!validSession(live, installation)) throw new Error("Cakupan outlet server tidak cocok");
  if (scope && (scope.context !== live.context_id || scope.operator !== live.member_id)) {
    scope = undefined;
    showContext();
  }
  scope = sessionFrom(live);
  showContext();
  await refreshQueue();
  await reanchor(scope, () => leaseFor(scope)).catch(() => {});
  await sync();
}
function validSession(data, pairingContext) {
  return uuid.test(data?.context_id) && uuid.test(data?.member_id) && data?.outlet_id === pairingContext.outlet &&
    Number.isSafeInteger(data?.revision) && data.revision > 0;
}
function sessionFrom(data) {
  return {
    ...installation, context: data.context_id, revision: data.revision,
    operator: data.member_id, outlet: data.outlet_id
  };
}
async function reconnect() {
  updateButtons();
  if (!navigator.onLine) { apiStatus("Tidak dapat dijangkau"); return; }
  if (reconnectWork) return reconnectWork;
  reconnectWork = (async () => {
    if (installation) {
      try { await flushOffline(installation); }
      catch { say("Antrean offline belum seluruhnya diterima server."); }
    }
    await recoverSession();
  })().catch(async error => {
    apiStatus(error.status ? "Menolak akses" : "Tidak dapat dijangkau");
    if (error.status === 401 || error.status === 403) {
      await denyOffline();
      usingOffline = false;
      scope = undefined;
      showContext();
      say("Sesi atau izin harus diperiksa kembali. Bukti lokal tetap tersimpan.");
    }
  }).finally(() => { reconnectWork = undefined; });
  return reconnectWork;
}

bind("pair-start", "click", async () => {
  say();
  pairStartKey ??= crypto.randomUUID();
  pairing = await startPairing(pairStartKey);
  redeemKey = crypto.randomUUID();
  apiStatus("Tersedia", true);
  byId("pair-code").textContent = pairing.code;
  byId("pair-redeem").hidden = false;
});
bind("pair-redeem", "submit", async event => {
  const values = new FormData(event.currentTarget);
  const tenant = String(values.get("tenant")).trim(), outlet = String(values.get("outlet")).trim();
  if (!uuid.test(tenant) || !uuid.test(outlet)) throw new Error("ID tenant atau outlet tidak sah");
  const result = await redeemPairing(pairing, redeemKey);
  if (!uuid.test(result.device_id)) throw new Error("ID perangkat dari server tidak sah");
  const candidate = { tenant, outlet, device: result.device_id };
  // Verify that the entered tenant owns the redeemed device before storing it.
  await operation(candidate, { operation: "state" }, "/state");
  await writeMeta("installation", candidate);
  localStorage.setItem(installationMarker, "1");
  installation = candidate;
  pairing = undefined;
  byId("pair-code").textContent = "";
  byId("pair-redeem").reset();
  showContext();
  say("Perangkat terpasang. Muat profil untuk melanjutkan.");
});
function showOfflineEnrollment(state) {
  const enabled = state.offline_enabled === true;
  const checkbox = byId("pin-login").elements.enroll;
  byId("offline-enroll").hidden = !enabled;
  checkbox.disabled = !enabled;
  if (!enabled) checkbox.checked = false;
}

bind("profiles", "click", async () => {
  const state = await operation(installation, { operation: "state" }, "/state");
  if (state.last_outlet_id && state.last_outlet_id !== installation.outlet) throw new Error("Outlet terikat pada konteks server. Perpindahan outlet menunggu kebijakan recovery.");
  showOfflineEnrollment(state);
  profiles = [];
  const select = byId("pin-login").elements.member;
  select.replaceChildren();
  let cursor;
  do {
    const target = { operation: "profiles", outlet_id: installation.outlet, ...(cursor ? { page_after: cursor } : {}) };
    const suffix = cursor ? `&page_after=${encodeURIComponent(cursor)}` : "";
    const page = await operation(installation, target, `/profiles?outlet_id=${installation.outlet}${suffix}`);
    profiles.push(...(page.profiles ?? []));
    cursor = page.next_cursor;
  } while (cursor && profiles.length < 300);
  for (const profile of profiles) {
    const option = element("option", profile.display_name);
    option.value = profile.member_id;
    select.append(option);
  }
  apiStatus("Tersedia", true);
  say(profiles.length ? "" : "Tidak ada profil yang ditugaskan pada outlet ini.");
});
function leaseFor(current) {
  return sessionOperation(current, "context.read", "/offline/lease", { method: "POST" });
}
bind("pin-login", "submit", async event => {
  const form = event.currentTarget;
  const member = form.elements.member.value, pin = form.elements.pin.value, prepare = form.elements.enroll.checked;
  form.elements.pin.value = "";
  if (!uuid.test(member) || !/^\d{6}$/.test(pin)) throw new Error("Pilih operator dan masukkan PIN enam digit");
  const state = await operation(installation, { operation: "state" }, "/state");
  if (state.last_outlet_id && state.last_outlet_id !== installation.outlet) throw new Error("Outlet tidak cocok");
  const target = { operation: "context.login", outlet_id: installation.outlet, member_id: member, expected_revision: state.context_revision };
  const epoch = ++sessionEpoch;
  const result = await operation(installation, target, "/context", {
    method: "POST", body: JSON.stringify({
      outlet_id: installation.outlet, member_id: member, pin, expected_revision: state.context_revision
    })
  });
  if (epoch !== sessionEpoch) return;
  if (!validSession(result, installation) || result.member_id !== member) throw new Error("Cakupan sesi server tidak sah");
  const current = sessionFrom(result);
  await writeMeta("session", current);
  scope = current;
  usingOffline = false;
  apiStatus("Tersedia", true);
  showContext();
  say();
  if (prepare && state.offline_enabled === true) {
    try {
      if (!navigator.locks || !globalThis.crypto?.subtle) throw new Error("Browser tidak mendukung akses offline");
      await enroll(await leaseFor(current), current, pin);
      hasJournal = true;
      updateButtons();
      say("PIN offline siap untuk operator dan outlet ini.");
    } catch (error) {
      say(error.code === "OFFLINE_JOURNAL_OUTSTANDING" ? "Owner perlu menutup jurnal sebelumnya sebelum izin baru dibuat." :
        "Izin offline belum siap. Kasir online tetap tersedia.");
    }
  }
  await refreshQueue();
  await sync();
});
bind("offline-login", "submit", async event => {
  if (navigator.onLine && apiReady) return;
  const field = event.currentTarget.elements.pin;
  const pin = field.value;
  field.value = "";
  const current = await offlineLogin(pin, installation);
  if (scope?.context !== current.context) { scope = undefined; showContext(); }
  scope = current;
  usingOffline = true;
  $.pay.elements.method.value = "cash";
  showContext();
  await refreshQueue();
  say("Kasir tunai offline aktif. Transaksi menunggu penerimaan server.");
});
bind("seal", "click", async () => {
  if (!installation || !apiReady || !navigator.onLine) return;
  await sealOffline(installation);
  await refreshQueue();
  say("Jurnal disegel. Minta Owner meninjau dan menutupnya sebelum pindah outlet.");
});
bind("logout", "click", async () => {
  const prior = scope;
  sessionEpoch++;
  usingOffline = false;
  lockOffline();
  scope = undefined;
  showContext();
  await writeMeta("session", prior ? { ...prior, locked: true } : null);
  if (prior && navigator.onLine) {
    try {
      await sessionOperation(prior, "context.logout", "/context/logout", { method: "POST" });
      await clearLockedSession(prior);
    }
    catch { say("Sesi lokal dikunci. Penutupan sesi server belum dapat diverifikasi."); }
  } else {
    say("Sesi lokal dikunci. Penutupan sesi server menunggu koneksi.");
  }
});
bind("search", "submit", async event => {
  const current = scope, term = String(new FormData(event.currentTarget).get("query")).trim();
  if (!current || !usingOffline && (!apiReady || !navigator.onLine)) throw new Error("Pencarian produk memerlukan sesi aktif");
  const offline = usingOffline;
  const items = offline ? await searchQuotes(term) :
    await sessionOperation(current, "context.read", `${checkoutPath(current)}/items/search?q=${encodeURIComponent(term)}`);
  if (scope !== current) return;
  $.products.replaceChildren();
  if (!items?.length) $.products.append(element("p", "Produk tidak ditemukan."));
  for (const item of items ?? []) {
    const block = element("div", undefined, "row");
    block.append(element("strong", `${item.name} · ${item.variant}`));
    block.append(element("p", `${item.code} · ${item.unit}`));
    const pick = element("button", "Lihat harga dan tambahkan");
    pick.addEventListener("click", handler(async () => {
      if (scope !== current) return;
      const price = offline ? { price_id: item.price_id, amount: item.unitPrice, updated_at: item.priceUpdatedAt } :
        await sessionOperation(current, "context.read", `/catalog/skus/${item.sku_id}/price`);
      if (scope !== current) return;
      const row = {
        rowId: crypto.randomUUID(), sku_id: item.sku_id, name: item.name, variant: item.variant, unit: item.unit, code: item.code, barcode: item.barcode,
        batch_tracked: item.batch_tracked === true, price_id: price.price_id, unitPrice: price.amount,
        priceUpdatedAt: price.updated_at, quantity: "1", discount: "0"
      };
      cart.push(row);
      if (!offline) await cacheQuote(current, row).catch(() => {});
      renderCart();
    }));
    block.append(pick);
    $.products.append(block);
  }
});
bind("pay", "change", updateButtons);

bind("pay", "submit", async () => {
  if (checkoutBusy || !scope || !usingOffline && (!navigator.onLine || !apiReady)) return;
  const current = scope, method = $.pay.elements.method.value;
  const payload = validateCheckout(method);
  checkoutBusy = true;
  updateButtons();
  say();
  try {
    if (usingOffline) {
      if (method !== "cash") throw new Error("Hanya tunai tersedia offline");
      const row = await saveOfflineSale(current, cart, payload.tendered);
      if (current === scope) {
        cart = [];
        renderCart();
        renderLocalEvidence(row);
        say("Penjualan tunai tersimpan. Menunggu penerimaan server.");
      }
    } else if (method === "cash") {
      // Require a live session immediately before creating an online-attempt row.
      await sessionOperation(current, "context.read", "/context");
      if (current !== scope) return;
      if (!navigator.onLine) throw new Error("Koneksi terputus. Gunakan simpan draf tunai.");
      let record;
      try { record = await stageCash(current, cart, payload.tendered, "online_attempt"); }
      catch { throw new Error("Bukti transaksi gagal disimpan. Checkout tidak dikirim."); }
      cart = [];
      renderCart();
      await refreshQueue(current);
      if (current !== scope) return;
      let result;
      try {
        result = await salesWrite(current, "PUT", `${checkoutPath(current)}/${record.id}`, record.body, record.id);
        await recordACK(record, { ...result, posted_at: result.created_at });
      } catch (error) {
        if (current === scope) checkAPIAvailability(error);
        await recordFailure(record, error);
        if (current === scope) say(failureReason(error));
        return;
      }
      if (current === scope) {
        renderReceipt(result);
        say("Penjualan tunai diterima server.");
      }
    } else {
      const body = fingerprintRequest(payload);
      if (manualAttempt && manualAttempt.body !== body) {
        throw new Error("Checkout sebelumnya belum pasti. Periksa histori atau ulangi keranjang yang sama.");
      }
      manualAttempt ??= { id: crypto.randomUUID(), body };
      const id = manualAttempt.id;
      const result = await salesWrite(current, "PUT", `${checkoutPath(current)}/${id}`, payload, id);
      if (current !== scope) return;
      if (result?.id !== id || result.outlet_id !== current.outlet || result.operator_id !== current.operator) {
        throw new Error("Cakupan balasan checkout tidak cocok");
      }
      manualAttempt = null;
      cart = [];
      renderCart();
      renderReceipt(result);
      say("Pembayaran nontunai menunggu pencatatan konfirmasi.");
    }
  } finally {
    checkoutBusy = false;
    updateButtons();
    await refreshQueue(current);
  }
});
bind("save-draft", "click", async () => {
  if (checkoutBusy || !scope || (navigator.onLine && apiReady) || $.pay.elements.method.value !== "cash") return;
  const current = scope;
  const payload = validateCheckout("cash");
  checkoutBusy = true;
  updateButtons();
  try {
    let record;
    try { record = await stageCash(current, cart, payload.tendered, "offline_draft"); }
    catch { throw new Error("Penyimpanan lokal gagal. Draf belum tersimpan."); }
    if (current !== scope) return;
    cart = [];
    renderCart();
    await refreshQueue(current);
    renderLocalEvidence(record);
    say("Draf tunai tersimpan. Transaksi offline belum diotorisasi.");
  } finally {
    checkoutBusy = false;
    updateButtons();
  }
});
bind("sync", "click", reconnect);
bind("reconnect", "click", reconnect);
bind("persist", "click", async () => {
  const granted = await navigator.storage?.persist?.();
  say(granted ? "Browser memberi penyimpanan persisten." : "Browser belum memberi penyimpanan persisten.");
});

function renderHistory(current) {
  const root = byId("history-list");
  root.replaceChildren();
  if (!history.length) root.append(element("p", "Belum ada transaksi."));
  for (const sale of history) {
    const row = element("div", undefined, "row");
    row.append(element("strong", `${formatMoney(minorUnits(sale.total))} · ${statusLabels[sale.payment_state] ?? sale.payment_state}`));
    row.append(element("p", `${new Date(sale.created_at).toLocaleString("id-ID")} · ${sale.id}`));
    const buttons = element("div", undefined, "row-actions");
    const receipt = element("button", "Lihat struk");
    receipt.addEventListener("click", handler(async () => {
      const result = await sessionOperation(current, "context.read", `${checkoutPath(current)}/${sale.id}/receipt`);
      if (scope === current) renderReceipt(result);
    }));
    buttons.append(receipt);
    if (sale.payment_state === "pending_manual_confirmation") {
      const confirm = element("button", "Catat pembayaran manual");
      confirm.addEventListener("click", handler(async () => {
        const reference = window.prompt("Referensi pembayaran manual");
        if (!reference?.trim() || scope !== current || !apiReady || !navigator.onLine) return;
        const result = await salesWrite(current, "POST", `${checkoutPath(current)}/${sale.id}/confirm-manual`, {
          reference: reference.trim()
        }, sale.id);
        if (scope === current) {
          renderReceipt(result);
          await loadHistory(false);
        }
      }));
      buttons.append(confirm);
    }
    row.append(buttons);
    root.append(row);
  }
}
async function loadHistory(next) {
  const current = scope;
  if (!current || !navigator.onLine || !apiReady) throw new Error("Histori server memerlukan koneksi");
  const last = next ? history.at(-1) : null;
  const query = last ? "?" + new URLSearchParams({ before: last.created_at, before_id: last.id }) : "";
  const page = await sessionOperation(current, "context.read", checkoutPath(current) + query) ?? [];
  if (scope !== current) return;
  history = next ? [...history, ...page] : page;
  byId("history-more").hidden = page.length < 50;
  renderHistory(current);
}
bind("history", "click", () => loadHistory(false));
bind("history-more", "click", () => loadHistory(true));
bind("print", "click", () => window.print());

window.addEventListener("offline", () => { apiStatus("Tidak dapat dijangkau"); updateButtons(); });
window.addEventListener("online", () => { void reconnect(); });
document.addEventListener("visibilitychange", () => { if (!document.hidden) void reconnect(); });
setInterval(() => {
  if (document.hidden || !installation || !navigator.onLine || checkoutBusy) return;
  if (!apiReady) void reconnect();
  else if (scope && !usingOffline) {
    void flushOffline(installation).then(() => refreshQueue()).catch(() => say("Antrean offline belum seluruhnya diterima server."));
    void sync()?.catch(() => say("Pemeriksaan antrean belum selesai."));
  }
}, 30000);
window.addEventListener("beforeinstallprompt", event => {
  event.preventDefault();
  deferredInstall = event;
  $.install.hidden = false;
});
bind("install", "click", async () => {
  if (!deferredInstall) return;
  await deferredInstall.prompt();
  deferredInstall = undefined;
  $.install.hidden = true;
});
window.addEventListener("appinstalled", () => { $.install.hidden = true; });

async function init() {
  try {
    await openStorage();
    installation = await readMeta("installation");
    hasJournal = Boolean(await readMeta("offline-lease"));
    storageLost = Boolean(localStorage.getItem(installationMarker)) && !installation;
    if (storageLost) say("Penyimpanan perangkat hilang. Hubungi Owner untuk recovery. Pairing dan posting dikunci.");
  } catch {
    storageLost = true;
    say("Penyimpanan perangkat tidak dapat dibuka. Posting dikunci.");
  }
  showContext();
  updateButtons();
  if (!storageLost) await reconnect();
}
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => say("Shell offline tidak dapat dipasang pada browser ini."));
}
void init();
