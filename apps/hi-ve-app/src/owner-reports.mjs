import { request } from "./api.mjs";
import { apiOrigin } from "./config.mjs";
import { readMeta } from "./storage.mjs";

const $ = id => document.getElementById(id);
const connect = $("report-connect"), filters = $("report-filter");
const datasets = {
  dashboard: ["reports.dashboard", "Dashboard omzet"],
  sales: ["reports.sales", "Penjualan dan pembayaran"],
  inventory: ["reports.inventory", "Saldo stok dan batch"],
  movements: ["reports.stock_movements", "Mutasi dan repacking"],
  transfers: ["reports.stock_movements", "Transfer dan discrepancy"],
};
const metrics = {
  gross: "Bruto", discount: "Diskon", returns: "Retur", voids: "Void", net: "Neto",
  received: "Pembayaran tercatat", pending: "Pembayaran pending", refund_recorded: "Refund tercatat",
};
const uuid = /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/;
const reconnectDelay = 5000;
let workspace, generation = 0;

function element(tag, text) {
  const item = document.createElement(tag);
  if (text !== undefined) item.textContent = text;
  return item;
}
function message(text) { $("report-message").textContent = text; }
function wsState(text) { $("realtime-state").textContent = text; }

async function beginGoogle(path, body) {
  const {authorization_url} = await request(path, {method:"POST",body});
  const target = new URL(authorization_url);
  if (target.origin !== "https://accounts.google.com") throw new Error("Origin Google tidak sah.");
  window.location.assign(target);
}

function act(work) {
  return async event => {
    event.preventDefault();
    try { await work(event); }
    catch (error) {
      if ([401, 403].includes(error.status)) {
        closeWorkspace();
        message("Sesi Google Owner atau hak laporan tidak tersedia.");
      } else message(error.status ? "Server tidak dapat memuat laporan." : error.message);
    }
  };
}
function closeWorkspace() {
  generation++;
  if (workspace) {
    workspace.socket?.close();
    clearTimeout(workspace.retry);
    clearInterval(workspace.poll);
  }
  workspace = undefined;
  wsState("Belum terhubung");
  filters.hidden = true;
  for (const id of ["report-data", "report-detail", "report-notice"]) $(id).replaceChildren();
  $("report-next").hidden = true;
  $("report-export").hidden = true;
}
function setPreset() {
  try {
    const tz=filters.elements.timezone.value.trim();
    const fields=new Intl.DateTimeFormat("en-CA",{timeZone:tz,year:"numeric",month:"2-digit",day:"2-digit"})
      .formatToParts(new Date());
    const parts=Object.fromEntries(fields.map(part=>[part.type,part.value]));
    const day=new Date(parts.year+"-"+parts.month+"-"+parts.day+"T00:00:00Z");
    if(filters.elements.range.value==="Kemarin") day.setUTCDate(day.getUTCDate()-1);
    if(filters.elements.range.value!=="Rentang tanggal"){
      filters.elements.from.value=day.toISOString().slice(0,10);
      filters.elements.through.value=filters.elements.from.value;
    }
  } catch { message("Zona waktu IANA tidak sah."); }
}
function query(kind) {
  const q = new URLSearchParams();
  const fields = filters.elements;
  if (kind !== "inventory") {
    if (!fields.from.value || !fields.through.value || fields.from.value > fields.through.value) throw new Error("Pilih rentang tanggal yang sah.");
    q.set("date_from",fields.from.value);
    q.set("date_through",fields.through.value);
    q.set("timezone",fields.timezone.value.trim());
  }
  const keys = ["location", ...(["movements","inventory"].includes(kind) ? ["sku"] : []), ...(kind === "movements" ? ["actor","movement"] : [])];
  for (const key of keys) if (fields[key].value.trim()) q.set(key,fields[key].value.trim());
  return q;
}
function table(labels, values) {
  const wrapper = element("div");
  wrapper.className = "report-scroll";
  const result = element("table");
  const heading = element("tr");
  labels.forEach(label => heading.append(element("th", label)));
  const head = element("thead");
  head.append(heading);
  result.append(head);
  const body = element("tbody");
  for (const cells of values) {
    const row = element("tr");
    for (const value of cells) {
      const cell = element("td");
      if (value instanceof Element) cell.append(value);
      else cell.textContent = value ?? "";
      row.append(cell);
    }
    body.append(row);
  }
  result.append(body);
  wrapper.append(result);
  return wrapper;
}

const columns = {
  inventory: { location_id:"Lokasi", sku_id:"SKU", batch_id:"Batch", lot:"Lot", bucket:"Bucket", quantity:"Kuantitas" },
  movements: { posted_at:"Waktu", kind:"Operasi", document_id:"Dokumen", location_id:"Lokasi", sku_id:"SKU", batch_id:"Batch", source_batch_id:"Batch asal", bucket:"Bucket", quantity_delta:"Perubahan", actor_id:"Operator" },
  transfers: { id:"Transfer", source_location_id:"Asal", destination_location_id:"Tujuan", dispatched:"Dikirim", received:"Diterima", transit:"Transit", discrepancy:"Selisih" },
};

function render(current,data) {
  const root = $("report-data");
  const timestamp = value => new Date(value).toLocaleString("id-ID", {timeZone: current.query.get("timezone") || "UTC"});
  root.replaceChildren();
  if (current.kind === "dashboard") {
    root.append(element("p", "Diperbarui " + timestamp(data.updated_at)),
      table(["Outlet", "Metode", ...Object.values(metrics)], [
        ["Gabungan", "Semua", ...Object.keys(metrics).map(key => data.total[key])],
        ...data.groups.map(g => {
          let outlet = g.outlet_name;
          if (!g.method && current.caps.includes("reports.sales")) {
            outlet = element("button",g.outlet_name);
            outlet.addEventListener("click",act(async () => {
              filters.elements.kind.value = current.kind = "sales";
              filters.elements.location.value = g.outlet_id;
              current.query = query(current.kind);
              await load(current);
            }));
          }
          return [outlet,g.method || "Semua",...Object.keys(metrics).map(key=>g[key])];
        }),
      ]));
  } else if (current.kind === "sales") {
    const sales = data.rows.map(row => {
      const detail = element("button", "Lihat " + row.id.slice(0,8));
      detail.addEventListener("click", act(async () => {
        const out = await request(current.base + "/reports/sales/" + row.id + "?location=" + row.outlet_id);
        if(workspace !== current || !root.contains(detail)) return;
        $("report-detail").replaceChildren(element("h3", "Transaksi " + out.id),
          table(["Item","Jumlah","Harga jual","Diskon","Neto"],
            out.items.map(item => [item.name,item.quantity+" "+item.unit,item.unit_price,item.discount,item.net])));
      }));
      return [detail,row.outlet_id,timestamp(row.created_at),row.method,row.payment_state,...Object.keys(metrics).map(key=>row[key])];
    });
    root.append(table(["Transaksi","Outlet","Waktu","Metode","Status",...Object.values(metrics)],sales));
  } else {
    const names = columns[current.kind];
    root.append(table(Object.values(names), data.rows.map(row => Object.keys(names).map(key => key === "posted_at" ? timestamp(row[key]) : row[key]))));
  }
  $("report-next").hidden = !data.next_cursor;
  current.next = data.next_cursor;
}

async function load(current, cursor = "") {
  const requestID = ++current.requestID;
  const kind = current.kind;
  const q = new URLSearchParams(current.query);
  if(cursor) q.set("cursor",cursor);
  const active = () => workspace === current && requestID === current.requestID;
  $("report-export").disabled = true;
  try {
    const data = await request(current.base + "/reports/" + kind + "?" + q);
    if (!active()) return;
    current.rendered = {kind, query: q};
    render(current,data);
    $("report-export").hidden = kind === "dashboard";
    $("report-export").disabled = false;
    message("");
  } catch (error) {
    if (!active()) return;
    if ([401,403].includes(error.status)) {
      closeWorkspace();
      message("Sesi Google Owner atau hak laporan tidak tersedia.");
      return;
    }
    throw error;
  }
}

function acceptFeed(current,feed) {
  if (workspace !== current || !/^[0-9]+$/.test(feed.cursor)) return;
  if (current.cursor && BigInt(feed.cursor) < BigInt(current.cursor)) return;
  current.cursor = feed.cursor;
  current.notifications = feed.sale_notifications === true;
  syncPreference(current);
  if (!feed.events.length) return;
  let notified = 0, changed = false;
  for (const event of feed.events) {
    if (!/^[0-9]+$/.test(event.id) || BigInt(event.id) <= current.lastNotice) continue;
    current.lastNotice = BigInt(event.id);
    changed = true;
    if (current.notifications && event.kind === "sale.received") notified++;
  }
  if (notified) $("report-notice").textContent = `${notified} penjualan diterima server.`;
  if (changed && (current.kind === "dashboard" || current.kind === "sales")) void load(current).catch(() => {});
}

function syncPreference(current) {
  $("report-preference").textContent = current.notifications ? "Matikan notifikasi di aplikasi" : "Aktifkan notifikasi di aplikasi";
}
const position = current => current.cursor ? "?after=" + current.cursor : "";
function socket(current) {
  if (workspace !== current) return;
  const url = new URL((apiOrigin || window.location.origin) + current.base + "/realtime/socket" + position(current));
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  try { current.socket = new WebSocket(url); } catch { wsState("Polling"); return; }
  current.socket.addEventListener("open", () => {
    if(workspace !== current) return;
    wsState("Terhubung");
    if(current.kind === "dashboard" || current.kind === "sales") void load(current).catch(() => {});
  });
  current.socket.addEventListener("message", incoming => {
    try { acceptFeed(current,JSON.parse(incoming.data)); } catch { current.socket.close(); }
  });
  current.socket.addEventListener("close", () => {
    if(workspace !== current) return;
    wsState("Polling · menyambung ulang");
    current.retry = setTimeout(() => socket(current),reconnectDelay);
  });
}

$("owner-claim").addEventListener("submit",act(async event => {
  const invitation = event.currentTarget.elements.invitation;
  const body = JSON.stringify({invitation:invitation.value.trim()});
  invitation.value = "";
  await beginGoogle("/api/v1/app/owner-claim",body);
}));
connect.addEventListener("submit",act(async event => {
  closeWorkspace();
  const expected = generation;
  const tenant = connect.elements.tenant.value.trim();
  if (!uuid.test(tenant)) throw new Error("ID tenant laporan tidak sah.");
  const base = "/api/v1/app/tenants/" + tenant;
  if (event.submitter?.value === "login") {
    await beginGoogle(base+"/login");
    return;
  }
  const caps = await request(base + "/reports/capabilities");
  if (expected !== generation) return;
  const options = Object.entries(datasets).filter(([, [cap]]) => caps.includes(cap));
  if (!options.length) {message("Tenant belum memiliki entitlement laporan."); return;}
  const current = {base,caps,kind:options[0][0],requestID:0,lastNotice:0n};
  workspace = current;
  filters.elements.kind.replaceChildren(...options.map(([kind, [,label]]) => {
    const opt = element("option",label); opt.value=kind;return opt;
  }));
  filters.hidden = false;
  $("report-preference").hidden = !caps.includes("realtime.notifications") || !caps.includes("realtime.events");
  const canPush = caps.includes("realtime.notifications") && "PushManager" in window && "Notification" in window && navigator.serviceWorker;
  $("report-push").hidden=true;
  $("report-push-off").hidden=!canPush;
  if(canPush){
    void request(base+"/realtime/push").then(key=>{
      if(workspace!==current) return;
      current.vapid=key.public_key;
      $("report-push").hidden=false;
    }).catch(()=>{});
  }
  setPreset();
  current.query = query(current.kind);
  await load(current);
  if (workspace !== current || !caps.includes("realtime.events")) return;
  acceptFeed(current,await request(base + "/realtime/events"));
  socket(current);
  current.poll = setInterval(async () => {
    if (workspace !== current || document.hidden || current.socket?.readyState === WebSocket.OPEN) return;
    try {
      acceptFeed(current,await request(base + "/realtime/events" + position(current)));
      if (current.kind === "dashboard" || current.kind === "sales") await load(current);
    } catch (error) {
      if ([401,403].includes(error.status)) {closeWorkspace();message("Akses laporan perlu dimuat ulang.");}
    }
  },7000);
}));
filters.elements.range.addEventListener("change",setPreset);
filters.elements.timezone.addEventListener("change",setPreset);
$("report-push").addEventListener("click",act(async () => {
  const current=workspace;
  if(!current?.vapid) return;
  const permission=await Notification.requestPermission();
  if(permission!=="granted"||workspace!==current) return;
  const registration=await navigator.serviceWorker.ready;
  const existing=await registration.pushManager.getSubscription();
  const key=Uint8Array.from(atob(current.vapid.replaceAll("-","+").replaceAll("_","/")),x=>x.charCodeAt(0));
  const sub=existing??await registration.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:key});
  try{
    const {endpoint,keys}=sub.toJSON();
    await request(current.base+"/realtime/push",{method:"PUT",body:JSON.stringify({endpoint,keys})});
  }catch(error){if(!existing) await sub.unsubscribe();throw error;}
  message("Web Push aktif di browser ini.");
}));
$("report-push-off").addEventListener("click",act(async () => {
  const current=workspace;
  const sub=await (await navigator.serviceWorker.ready).pushManager.getSubscription();
  if(sub){
    await request(current.base+"/realtime/push",{method:"DELETE",body:JSON.stringify({endpoint:sub.endpoint})});
    await sub.unsubscribe();
  }
  message("Web Push dinonaktifkan.");
}));
$("report-logout").addEventListener("click",act(async () => {
  await request(workspace.base+"/logout",{method:"POST"});
  closeWorkspace();
  message("Sesi Owner ditutup.");
}));
filters.addEventListener("submit",act(async () => {
  const current=workspace;
  const kind=filters.elements.kind.value;
  current.query=query(kind);
  current.kind=kind;
  $("report-detail").replaceChildren();
  await load(current);
}));
$("report-next").addEventListener("click",act(() => load(workspace,workspace.next)));
$("report-export").addEventListener("click",act(async () => {
  const current=workspace;
  const rendered = current?.rendered;
  if (!rendered) return;
  const q = new URLSearchParams(rendered.query);
  q.set("format","csv");
  const response=await fetch(apiOrigin+current.base+"/reports/"+rendered.kind+"?"+q,{credentials:"include",cache:"no-store",redirect:"error"});
  if(!response.ok) throw Object.assign(new Error("Ekspor gagal"),{status:response.status});
  const blob=await response.blob();
  if (workspace !== current || current.rendered !== rendered) return;
  const url=URL.createObjectURL(blob);
  const download=element("a");
  download.href=url;
  download.download="hi-ve-report.csv";
  download.click();
  setTimeout(()=>URL.revokeObjectURL(url),1000);
}));
$("report-preference").addEventListener("click",act(async () => {
  const current=workspace, value = !current.notifications;
  await request(current.base + "/realtime/preferences",{method:"PUT",body:JSON.stringify({sale_notifications:value})});
  current.notifications=value;
  syncPreference(current);
}));
$("owner-reports").addEventListener("toggle",event => {if(!event.currentTarget.open) closeWorkspace();});
readMeta("installation").then(info=>{if(info&&!connect.elements.tenant.value) connect.elements.tenant.value=info.tenant;}).catch(()=>{});
