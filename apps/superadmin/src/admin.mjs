import { origin } from "./config.mjs";

const $ = id => document.getElementById(id);
const uuid = /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/;
const code = /^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+$/;
const keys = new Map();
let nextTenant, nextAudit, auditTenant;

function element(tag,text) {
  const result = document.createElement(tag);
  result.textContent=text;
  return result;
}
async function request(path, method="GET", data) {
  const headers = {};
  const body = data === undefined ? undefined : JSON.stringify(data);
  const scope = method + " " + path + " " + (body ?? "");
  if (body !== undefined) headers["Content-Type"]="application/json";
  if (!["GET","HEAD"].includes(method)) {
    if(!keys.has(scope)) keys.set(scope,crypto.randomUUID());
    headers["Idempotency-Key"]=keys.get(scope);
  }
  const response=await fetch(origin+"/api/v1/admin"+path,{method,headers,body,credentials:"include",cache:"no-store",redirect:"error",signal:AbortSignal.timeout(15000)});
  if (!response.ok) throw Object.assign(new Error(response.status===401 ? "Sesi Superadmin diperlukan." :
    response.status===403 ? "Permission platform tidak mencakup operasi ini." : "Operasi ditolak server ("+response.status+")."),{status:response.status});
  return response.status===204 ? null : response.json();
}
function action(fn) {
  return async event => {
    event.preventDefault();
    const trigger=event.submitter ?? (event.currentTarget instanceof HTMLButtonElement ? event.currentTarget : null);
    if (trigger) trigger.disabled=true;
    try {$("notice").textContent=""; await fn(event);}
    catch (error) {
      if ([401,403].includes(error.status)) clear();
      $("notice").textContent=error.message;
    } finally {if(trigger) trigger.disabled=false;}
  };
}
function clear() {
  $("tenants").replaceChildren();$("audit-events").replaceChildren();$("entitlement").textContent="";
  $("audit-next").hidden=$("directory-next").hidden=true;
  nextTenant=nextAudit=auditTenant=undefined;
  keys.clear();
}
function selected() {
  const id=$("tenant").value.trim();
  if (!uuid.test(id)) throw new Error("Masukkan ID tenant yang sah.");
  return id;
}
function entitlement() {
  const cap=$("capability").value.trim();
  if (!code.test(cap)) throw new Error("Masukkan ID capability yang sah.");
  return "/tenants/"+selected()+"/entitlements/"+encodeURIComponent(cap);
}
async function directory(next=false) {
  const body=await request("/tenants"+(next && nextTenant ? "?after="+encodeURIComponent(nextTenant) : ""));
  $("tenants").replaceChildren(...body.tenants.map(row=>{
    const item=element("li",row.name+" · "+row.status+(row.primary_owner_id ? " · Owner "+row.primary_owner_id : ""));
    const choose=element("button","Pilih "+row.name);
    choose.addEventListener("click",()=>{$("tenant").value=row.tenant_id; $("audit-events").replaceChildren();$("audit-next").hidden=true;$("entitlement").textContent="";});
    item.append(" ",choose);return item;
  }));
  nextTenant=body.next;$("directory-next").hidden=!nextTenant;
}
async function audit(next=false) {
  const tenant=selected();
  const query=next && nextAudit && tenant===auditTenant ? "?"+new URLSearchParams({before:nextAudit.occurred_at,before_id:nextAudit.id}) : "";
  const body=await request("/tenants/"+tenant+"/audit"+query);
  $("audit-events").replaceChildren(...body.events.map(row=>element("li",new Date(row.occurred_at).toLocaleString("id-ID")+" · "+row.action+" · "+row.outcome)));
  nextAudit=body.next;auditTenant=tenant;$("audit-next").hidden=!nextAudit;
}
$("login").addEventListener("click",action(async()=>{
  const {authorization_url}=await request("/login","POST");
  const target=new URL(authorization_url);
  if(target.origin!=="https://accounts.google.com") throw new Error("Origin Google tidak sah.");
  location.assign(target);
}));
$("logout").addEventListener("click",action(async()=>{await request("/logout","POST");clear();$("notice").textContent="Sesi platform ditutup.";}));
$("directory").addEventListener("click",action(()=>directory()));
$("directory-next").addEventListener("click",action(()=>directory(true)));
$("audit").addEventListener("click",action(()=>audit()));
$("audit-next").addEventListener("click",action(()=>audit(true)));
$("reissue").addEventListener("click",action(async()=>{
  await request("/tenants/"+selected()+"/owner-invitation","POST");
  $("notice").textContent="Permintaan kirim ulang diproses.";
}));
$("read-entitlement").addEventListener("click",action(async()=>{
  $("entitlement").textContent=JSON.stringify(await request(entitlement()),null,2);
}));
document.querySelectorAll("form[data-operation]").forEach(form=>form.addEventListener("submit",action(async()=>{
  const data=Object.fromEntries(new FormData(form));
  switch(form.dataset.operation) {
    case "create": {
      const created=await request("/tenants","POST",data);
      $("tenant").value=created.tenant_id;
      $("notice").textContent="Tenant dibuat. Status "+created.tenant_status+".";
      break;
    }
    case "activate":
      data.verification_method="verified-organization-contact";
      await request("/tenants/"+selected()+"/activate","POST",data);
      $("notice").textContent="Tenant aktif.";
      break;
    case "grant":
      data.effective_at=new Date(data.effective_at).toISOString();
      data.expires_at=new Date(data.expires_at).toISOString();
      data.revision=Number(data.revision);
      $("entitlement").textContent=JSON.stringify(await request(entitlement(),"PUT",data),null,2);
      break;
    case "revoke":
      data.revision=Number(data.revision);
      $("entitlement").textContent=JSON.stringify(await request(entitlement()+"/revoke","POST",data),null,2);
      break;
  }
})));
