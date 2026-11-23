import { request } from "./api.mjs";
import { readMeta } from "./storage.mjs";

const uuid = /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/;
const form = document.getElementById("owner-journal-form");
const status = document.getElementById("owner-journal-status");
const results = document.getElementById("owner-journal-results");
const switchForm = document.getElementById("owner-switch-form");
const switchResults = document.getElementById("owner-switch-results");
let tenant, loading = false;

function node(tag, text) {
  const element = document.createElement(tag);
  if (text) element.textContent = text;
  return element;
}

function field(text, name, type = "text") {
  const label = node("label", text), input = node("input");
  input.type = type;
  input.name = name;
  input.required = name === "reason";
  label.append(input);
  return label;
}

function base() {
  if (!uuid.test(tenant)) throw new Error("Masukkan ID tenant yang sah.");
  return `/api/v1/app/tenants/${tenant}`;
}

function act(work) {
  return async event => {
    event.preventDefault();
    if (loading) return;
    loading = true;
    status.textContent = "";
    try { await work(event); }
    catch (error) {
      status.textContent = error.status === 401 || error.status === 403 ? "Diperlukan sesi Google Primary Owner tenant aktif." :
        error.code === "OFFLINE_JOURNAL_OUTSTANDING" ? "Jurnal belum aman. Selesaikan exception dan segel atau pulihkan perangkat." :
        error.code === "INSUFFICIENT_STOCK" ? "Stok belum cukup. Selesaikan stok melalui Inventory atau catat refund manual." :
        error.status === 409 ? "Bukti atau versi berubah. Periksa keputusan terakhir." :
        error.status >= 500 ? "Server belum dapat menyimpan perubahan." : error.message;
    } finally { loading = false; }
  };
}

const put = (path, data) => request(base() + "/offline" + path, { method: "PUT", body: JSON.stringify(data) });

function journalForm(journal) {
  const review = node("form");
  review.append(node("p", `Perangkat ${journal.device_id} · Outlet ${journal.outlet_id}`),
    node("p", `Bukti diterima ${journal.received_count}, perlu tinjauan ${journal.unresolved_count}, ${journal.sealed_count === null ? "belum disegel" : "disegel"}`),
    field("Alasan penutupan", "reason"), field("Perangkat sudah dicabut dan bukti lokal hilang", "lost", "checkbox"),
    node("button", "Tutup jurnal setelah ditinjau"));
  review.addEventListener("submit", act(async () => {
    await put(`/leases/${journal.id}/close`, { reason: review.elements.reason.value, lost: review.elements.lost.checked });
    await loadReview();
    status.textContent = "Penutupan jurnal tersimpan dalam audit.";
  }));
  return review;
}

function saleForm(sale) {
  const review = node("form");
  review.append(node("p", `${sale.id} · ${sale.reason}`), node("p", `Outlet ${sale.outlet_id} · Operator ${sale.operator_id}`),
    node("p", `Terjadi ${new Date(sale.occurred_at).toLocaleString("id-ID")} · Diterima ${new Date(sale.received_at).toLocaleString("id-ID")}`));
  for (const [index, item] of sale.checkout.items.entries()) {
    review.append(node("p", `SKU ${item.sku_id} · ${item.quantity} ${item.unit} · harga ${sale.quote_amounts[index]} · diskon ${item.discount || "0"}`));
  }
  const post = node("button", "Posting penjualan dan stok");
  post.value = "posted";
  const refund = node("button", "Catat refund manual");
  refund.value = "manual_refund_recorded";
  review.append(field("Alasan keputusan", "reason"), field("Referensi refund yang telah dilakukan", "reference"),
    field("Nominal yang dikembalikan", "amount"), post, refund);
  review.addEventListener("submit", act(async event => {
    const decision = event.submitter.value;
    const data = { reason: review.elements.reason.value, decision };
    if (decision === "manual_refund_recorded") {
      data.reference = review.elements.reference.value;
      data.refund_amount = review.elements.amount.value;
    }
    await put(`/sales/${sale.id}/resolution`, data);
    await loadReview();
    status.textContent = "Keputusan rekonsiliasi tersimpan.";
  }));
  return review;
}

async function loadReview(afterJournal = "", afterSale = "") {
  const data = await request(base() + "/offline/review?" + new URLSearchParams({ after_journal: afterJournal, after_sale: afterSale }));
  results.replaceChildren(node("h3", "Jurnal"));
  if (data.journals.length) results.append(...data.journals.map(journalForm));
  else results.append(node("p", "Tidak ada jurnal terbuka."));
  results.append(node("h3", "Penjualan perlu tinjauan"));
  if (data.sales.length) results.append(...data.sales.map(saleForm));
  else results.append(node("p", "Tidak ada penjualan dalam tinjauan."));
  if (data.journals.length === 50 || data.sales.length === 50) {
    const next = node("button", "Halaman rekonsiliasi berikutnya");
    next.addEventListener("click", act(() => loadReview(data.journals.at(-1)?.id ?? afterJournal, data.sales.at(-1)?.id ?? afterSale)));
    results.append(next);
  }
}

function selectTenant() {
  const selected = form.elements.tenant.value.trim();
  if (tenant !== selected) { results.replaceChildren(); switchResults.replaceChildren(); }
  tenant = selected;
}

form.addEventListener("submit", act(async event => {
  selectTenant();
  if (event.submitter.value === "login") {
    const data = await request(base() + "/login", { method: "POST" });
    const target = new URL(data.authorization_url);
    if (target.origin !== "https://accounts.google.com") throw new Error("Origin Google tidak sah.");
    window.location.assign(target);
  } else await loadReview();
}));

switchForm.addEventListener("submit", act(async () => {
  selectTenant();
  const id = switchForm.elements.device.value.trim();
  if (!uuid.test(id)) throw new Error("Masukkan ID perangkat yang sah.");
  const path = `/devices/${id}/outlet`;
  const state = await request(base() + "/offline" + path);
  const choices = node("select");
  choices.setAttribute("aria-label", "Outlet tujuan");
  for (const available of state.assigned_outlets) {
    const option = node("option", available);
    option.value = available;
    option.selected = available === state.outlet_id;
    choices.append(option);
  }
  const button = node("button", "Pindahkan outlet");
  button.disabled = state.revoked || choices.options.length < 1;
  button.addEventListener("click", act(async () => {
    const result = await put(path, { outlet_id: choices.value, expected_revision: state.revision });
    switchResults.replaceChildren();
    status.textContent = `Outlet diperbarui. Revision perangkat ${result.revision}.`;
  }));
  const revoke = node("button", "Cabut perangkat hilang");
  revoke.disabled = state.revoked;
  const revokeKey = crypto.randomUUID();
  revoke.addEventListener("click", act(async () => {
    if (!window.confirm("Cabut perangkat ini secara permanen sebelum mencatat kehilangan jurnal?")) return;
    await request(base() + `/devices/${id}/revoke`, { method: "POST", key: revokeKey,
      body: JSON.stringify({ expected_revision: state.device_revision }) });
    switchResults.replaceChildren();
    status.textContent = "Perangkat dicabut. Tinjau exception yang diterima dan catat kehilangan pada jurnal.";
  }));
  switchResults.replaceChildren(node("p", `Outlet aktif ${state.outlet_id ?? "belum dipilih"}`), choices, button, revoke);
}));

readMeta("installation").then(installation => {
  if (!installation) return;
  if (!form.elements.tenant.value) form.elements.tenant.value = installation.tenant;
  if (!switchForm.elements.device.value) switchForm.elements.device.value = installation.device;
}).catch(() => {});
