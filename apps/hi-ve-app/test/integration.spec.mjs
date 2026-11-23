import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";

const tenant = "90000000-0000-4000-8000-000000000001";
const outlet = "90000000-0000-4000-8000-000000000002";
const sale = "90000000-0000-4000-8000-000000000003";
const base = "/api/v1/app/tenants/" + tenant;
const totals = {gross:"100",discount:"0",returns:"0",voids:"0",net:"100",received:"100",pending:"0",refund_recorded:"0"};

test("Owner dashboard drills into a receipt and applies live events once", async ({page}) => {
  let socket, reads=0, revised=false;
  await page.route("**/api/v1/app/**", async route => {
    const path=new URL(route.request().url()).pathname;
    if(path===base+"/reports/capabilities") return route.fulfill({json:["reports.dashboard","reports.sales","realtime.events","realtime.notifications"]});
    if(path===base+"/reports/dashboard") {
      reads++;
      const current = revised ? {...totals,net:"125"} : totals;
      return route.fulfill({json:{updated_at:new Date().toISOString(),total:current,groups:[{...current,outlet_name:"Outlet Uji",outlet_id:outlet,method:""}]}});
    }
    if(path===base+"/realtime/events") return route.fulfill({json:{cursor:"1",events:[],sale_notifications:true}});
    if(path===base+"/reports/sales") return route.fulfill({json:{rows:[{...totals,id:sale,outlet_id:outlet,created_at:new Date().toISOString(),method:"cash",payment_state:"cash_received"}]}});
    if(path===base+"/reports/sales/"+sale) return route.fulfill({json:{id:sale,items:[{name:"Madu",quantity:"1",unit:"pcs",unit_price:"100",discount:"0",net:"100"}]}});
    return route.fulfill({status:404});
  });
  await page.routeWebSocket("**/realtime/socket**", connection => {socket=connection;});
  await page.goto("/");
  await page.getByText("Dashboard dan laporan Owner",{exact:true}).click();
  await page.getByLabel("Tenant laporan").fill(tenant);
  await page.getByRole("button",{name:"Muat akses laporan"}).click();
  await expect(page.getByRole("button",{name:"Outlet Uji"})).toBeVisible();
  await expect(page.locator("#realtime-state")).toHaveText("Terhubung");
  await expect.poll(()=>reads).toBe(2);
  revised=true;
  const notification=JSON.stringify({cursor:"2",events:[{id:"2",kind:"sale.received"}],sale_notifications:true});
  socket.send(notification);
  await expect(page.locator("#report-data")).toContainText("125");
  await expect(page.locator("#report-notice")).toHaveText("1 penjualan diterima server.");
  socket.send(notification);
  await page.getByRole("button",{name:"Outlet Uji"}).click();
  await page.getByRole("button",{name:"Lihat 90000000"}).click();
  await expect(page.locator("#report-detail")).toContainText("Madu");
  expect(reads).toBe(3);
  await expect(page.locator("#report-filter select[name=kind] option")).toHaveCount(2);
});

test("static Superadmin uses platform routes and clears records when access is rejected", async ({page}) => {
  const root = "http://127.0.0.1:4182";
  let denied=false, directoryCalls=0, delayed;
  await page.route(root+"/**", async route => {
    const path=new URL(route.request().url()).pathname;
    if (path==="/api/v1/admin/tenants") {
      directoryCalls++;
      expect(route.request().method()).toBe("GET");
      if (directoryCalls===3) await new Promise(resolve=>{delayed=resolve;});
      return denied ? route.fulfill({status:401}) : route.fulfill({json:{tenants:[{tenant_id:tenant,name:"Tenant Satu",status:"active"}]}});
    }
    if (path==="/api/v1/admin/logout") return route.fulfill({status:204});
    const file={"/":"index.html","/style.css":"style.css","/assets/admin.mjs":"assets/admin.mjs","/assets/config.mjs":"assets/config.mjs"}[path];
    if(!file) return route.fulfill({status:404});
    const contentType = file.endsWith(".mjs") ? "text/javascript" : file.endsWith(".css") ? "text/css" : "text/html";
    return route.fulfill({contentType,body:await readFile(new URL("../../superadmin/dist/"+file,import.meta.url))});
  });
  await page.goto(root);
  await page.getByRole("button",{name:"Muat tenant",exact:true}).click();
  await expect(page.getByRole("button",{name:"Pilih Tenant Satu"})).toBeVisible();
  await page.getByRole("button",{name:"Pilih Tenant Satu"}).click();
  await expect(page.getByLabel("ID tenant")).toHaveValue(tenant);
  denied=true;
  await page.getByRole("button",{name:"Muat tenant",exact:true}).click();
  await expect(page.getByText("Sesi Superadmin diperlukan.")).toBeVisible();
  await expect(page.getByRole("button",{name:"Pilih Tenant Satu"})).toHaveCount(0);
  expect(directoryCalls).toBe(2);
  denied=false;
  await page.getByRole("button",{name:"Muat tenant",exact:true}).click();
  await expect.poll(()=>Boolean(delayed)).toBe(true);
  await page.getByRole("button",{name:"Keluar",exact:true}).click();
  await expect(page.getByText("Sesi platform ditutup.")).toBeVisible();
  delayed();
  await expect(page.getByRole("button",{name:"Muat tenant",exact:true})).toBeEnabled();
  await expect(page.getByRole("button",{name:"Pilih Tenant Satu"})).toHaveCount(0);
  await expect(page.getByLabel("ID tenant")).toHaveValue("");
});

test.describe("Owner report timezone and export", () => {
  test.use({timezoneId:"Asia/Jakarta"});
  test("timestamps and CSV use the rendered report scope", async ({page}) => {
    let delayNext, exported;
    const other = "90000000-0000-4000-8000-000000000004";
    await page.route("**/api/v1/app/**", async route => {
      const url=new URL(route.request().url());
      if (url.pathname===base+"/reports/capabilities") return route.fulfill({json:["reports.sales"]});
      if (url.searchParams.get("format")==="csv") {
        exported=url.searchParams.get("location");
        return route.fulfill({body:"id\nfixture",contentType:"text/csv"});
      }
      if (url.searchParams.get("location")===other) await new Promise(resolve=>{delayNext=resolve;});
      return route.fulfill({json:{rows:[{...totals,id:sale,outlet_id:url.searchParams.get("location")||outlet,
        created_at:"2026-11-01T01:00:00Z",method:"cash",payment_state:"cash_received"}]}});
    });
    await page.goto("/");
    await page.getByText("Dashboard dan laporan Owner",{exact:true}).click();
    await page.getByLabel("Tenant laporan").fill(tenant);
    await page.getByRole("button",{name:"Muat akses laporan"}).click();
    const report = page.locator("#report-data");
    await expect(report).toContainText("01.00.00");
    await page.locator("#report-filter [name=location]").fill(other);
    await page.getByRole("button",{name:"Muat laporan",exact:true}).click();
    const exportButton=page.locator("#report-export");
    await expect.poll(()=>Boolean(delayNext)).toBe(true);
    await expect(exportButton).toBeDisabled();
    await expect(report).toContainText(outlet);
    delayNext();
    await expect(report).toContainText(other);
    await exportButton.click();
    await expect.poll(()=>exported).toBe(other);
  });
});

test("Owner can start the existing invitation claim from the static app", async ({page}) => {
  let body;
  await page.route("**/api/v1/app/owner-claim",route=>{
    expect(route.request().method()).toBe("POST");
    body=route.request().postDataJSON();
    return route.fulfill({json:{authorization_url:"https://accounts.google.com/o/oauth2/v2/auth"}});
  });
  await page.route("https://accounts.google.com/**",route=>route.fulfill({status:204}));
  await page.goto("/");
  await page.getByText("Dashboard dan laporan Owner",{exact:true}).click();
  const invitation=page.getByLabel("Kode undangan Primary Owner");
  await invitation.fill("synthetic-invitation-code");
  await page.getByRole("button",{name:"Tautkan akun Google"}).click();
  await expect.poll(()=>body).toEqual({invitation:"synthetic-invitation-code"});
  await expect(invitation).toHaveValue("");
});

test("Owner dashboard falls back to polling after a WebSocket disconnect", async ({page}) => {
  let polled=0;
  await page.route("**/api/v1/app/**",route=>{
    const url=new URL(route.request().url());
    if(url.pathname===base+"/reports/capabilities") return route.fulfill({json:["reports.dashboard","realtime.events"]});
    if(url.pathname===base+"/realtime/events") {
      if(url.searchParams.has("after")) polled++;
      return route.fulfill({json:{cursor:polled ? "2" : "1",events:polled ? [{id:"2",kind:"sale.received"}] : [],sale_notifications:false}});
    }
    if(url.pathname===base+"/reports/dashboard") return route.fulfill({json:{updated_at:new Date().toISOString(),total:{...totals,net:polled ? "120" : "100"},groups:[]}});
    return route.fulfill({status:404});
  });
  await page.routeWebSocket("**/realtime/socket**",socket=>socket.close());
  await page.goto("/");
  await page.getByText("Dashboard dan laporan Owner",{exact:true}).click();
  await page.getByLabel("Tenant laporan").fill(tenant);
  await page.getByRole("button",{name:"Muat akses laporan"}).click();
  await expect(page.locator("#realtime-state")).toContainText("Polling");
  await expect(page.locator("#report-data")).toContainText("120",{timeout:12000});
  expect(polled).toBeGreaterThan(0);
  await expect(page.locator("#report-notice")).toBeEmpty();
});

test("Web Push requires opt-in and can be revoked for the browser", async ({page}) => {
  let methods=[];
  await page.addInitScript(() => {
    Notification.requestPermission=async()=>"granted";
    const subscription={endpoint:"https://fcm.googleapis.com/fcm/send/fixture",
      toJSON:()=>({endpoint:"https://fcm.googleapis.com/fcm/send/fixture",keys:{p256dh:"fixture",auth:"fixture"}}),
      unsubscribe:async()=>true};
    PushManager.prototype.getSubscription=async()=>window.testPushSubscription??null;
    PushManager.prototype.subscribe=async()=>window.testPushSubscription=subscription;
  });
  await page.route("**/api/v1/app/**",route=>{
    const path=new URL(route.request().url()).pathname;
    if(path===base+"/reports/capabilities") return route.fulfill({json:["reports.dashboard","realtime.notifications"]});
    if(path===base+"/reports/dashboard") return route.fulfill({json:{updated_at:new Date().toISOString(),total:totals,groups:[]}});
    if(path===base+"/realtime/push"){
      methods.push(route.request().method());
      return route.request().method()==="GET" ? route.fulfill({json:{public_key:"AQ"}}) : route.fulfill({status:204});
    }
    return route.fulfill({status:404});
  });
  await page.goto("/");
  await page.getByText("Dashboard dan laporan Owner",{exact:true}).click();
  await page.getByLabel("Tenant laporan").fill(tenant);
  await page.getByRole("button",{name:"Muat akses laporan"}).click();
  await expect(page.getByRole("button",{name:"Aktifkan Web Push di browser ini"})).toBeVisible();
  expect(methods).toEqual(["GET"]);
  await page.getByRole("button",{name:"Aktifkan Web Push di browser ini"}).click();
  await expect(page.locator("#report-message")).toHaveText("Web Push aktif di browser ini.");
  await page.getByRole("button",{name:"Matikan Web Push di browser ini"}).click();
  await expect(page.locator("#report-message")).toHaveText("Web Push dinonaktifkan.");
  expect(methods).toEqual(["GET","PUT","DELETE"]);
});
