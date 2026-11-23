import { expect, test } from "@playwright/test";

const tenant = "90000000-0000-4000-8000-000000000001";
const outlet = "90000000-0000-4000-8000-000000000002";

async function fixture(page, body) {
  const response = body ? await page.request.post("/__fixture/state", { data: body }) : await page.request.get("/__fixture/state");
  return response.json();
}
async function ready(page) {
  await fixture(page, { reset: true });
  await page.goto("/");
  await page.getByRole("button", { name: "Buat kode pairing" }).click();
  await expect(page.locator("#pair-code")).toHaveText("12345678");
  await page.getByLabel("ID tenant").fill(tenant);
  await page.getByLabel("ID outlet").fill(outlet);
  await page.getByRole("button", { name: "Selesaikan pairing" }).click();
  await page.getByRole("button", { name: "Muat profil" }).click();
  await expect(page.getByRole("option", { name: "Operator Uji" })).toBeAttached();
  await page.getByLabel("PIN", { exact: true }).fill("654321");
  await page.getByRole("button", { name: "Masuk", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Penjualan", exact: true })).toBeVisible();
}
async function addCart(page) {
  await page.getByLabel("Nama, SKU, atau barcode").fill("madu");
  await page.getByRole("button", { name: "Cari", exact: true }).click();
  await page.getByRole("button", { name: "Lihat harga dan tambahkan" }).click();
  await expect(page.locator("#total")).toHaveText("Rp 12,50");
  await page.getByLabel("Uang diterima").fill("20");
}
async function stored(page, store) {
  return page.evaluate(async store => new Promise((resolve, reject) => {
    const open = indexedDB.open("hi-ve-app");
    open.onerror = () => reject(open.error);
    open.onsuccess = () => {
      const req = open.result.transaction(store).objectStore(store).getAll();
      req.onsuccess = () => { resolve(req.result); open.result.close(); };
      req.onerror = () => reject(req.error);
    };
  }), store);
}

test("mobile pairing, offline shell and server cash receipt", async ({ page }) => {
  await ready(page);
  await addCart(page);
  await page.getByRole("button", { name: "Checkout" }).click();
  await expect(page.locator("#notice")).toHaveText("Penjualan tunai diterima server.");
  await expect(page.locator("#receipt-data")).toContainText("Kembalian Rp 7,50");
  await expect(page.locator("#queue")).toContainText("Tersinkron");
  await page.evaluate(() => { window.print = () => { window.testPrinted = true; }; });
  await page.getByRole("button", { name: "Cetak" }).click();
  expect(await page.evaluate(() => window.testPrinted)).toBe(true);
  const cached = await page.evaluate(async () => {
    await navigator.serviceWorker.ready;
    return (await Promise.all((await caches.keys()).map(async name => (await (await caches.open(name)).keys()).map(r => r.url)))).flat();
  });
  expect(cached.some(url => url.endsWith("/index.html"))).toBe(true);
  expect(cached.every(url => !url.includes("/api/"))).toBe(true);
  expect(JSON.stringify(await stored(page, "meta"))).not.toContain("654321");
  expect(await page.locator("body").evaluate(node => node.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole("button", { name: "Muat histori" }).click();
  await expect(page.locator("#history-list")).toContainText("Tunai diterima");
});

test("durable cash evidence survives response loss and restart without repost", async ({ page, context }) => {
  await ready(page);
  await addCart(page);
  await fixture(page, { mode: "lose_response" });
  await page.getByRole("button", { name: "Checkout" }).click();
  await expect(page.locator("#queue")).toContainText("Pending");
  await expect(page.locator("#notice")).toContainText("belum pasti");
  const original = (await stored(page, "sales"))[0];
  expect(original.state).toBe("pending");
  await fixture(page, { mode: "" });
  await page.close();
  const reopened = await context.newPage();
  await reopened.goto("/");
  await expect(reopened.locator("#queue")).toContainText("Tersinkron");
  await reopened.getByRole("button", { name: "Cek bukti server" }).click();
  expect(await fixture(reopened)).toMatchObject({ puts: 1, orders: 1, acks: 1 });
  expect((await stored(reopened, "sales"))[0]).toMatchObject({ id: original.id, state: "synced" });
});

test("offline draft persists and never posts on reconnect", async ({ page, context }) => {
  await ready(page);
  await addCart(page);
  await page.evaluate(() => navigator.serviceWorker.ready.then(() => true));
  await context.setOffline(true);
  await expect(page.locator("#network")).toHaveText("Offline");
  await expect(page.getByRole("button", { name: "Checkout" })).toBeDisabled();
  await expect(page.getByRole("option", { name: "Nontunai manual" })).toBeDisabled();
  await page.getByRole("button", { name: "Simpan draf tunai" }).click();
  await expect(page.locator("#receipt-data")).toContainText("belum diterima server");
  await expect(page.locator("#receipt-data")).toContainText("Total Rp 12,50");
  const record = (await stored(page, "sales"))[0];
  expect(record).toMatchObject({ source: "offline_draft", state: "pending" });
  expect(await stored(page, "login_events")).toHaveLength(0);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Masuk sebagai operator" })).toBeVisible();
  await context.setOffline(false);
  await expect(page.locator("#queue")).toContainText(record.id);
  expect(await fixture(page)).toMatchObject({ puts: 0, orders: 0, acks: 0 });
});

test("offline logout survives restart and closes the original online context", async ({ page, context }) => {
  await ready(page);
  await page.evaluate(() => navigator.serviceWorker.ready.then(() => true));
  await context.setOffline(true);
  await page.locator("#logout").click();
  await expect(page.getByRole("heading", { name: "Masuk sebagai operator" })).toBeVisible();
  expect((await stored(page, "meta")).some(value => value?.locked)).toBe(true);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Masuk sebagai operator" })).toBeVisible();
  await context.setOffline(false);
  await expect.poll(async () => (await stored(page, "meta")).some(value => value?.locked)).toBe(false);
  await expect(page.getByRole("heading", { name: "Masuk sebagai operator" })).toBeVisible();
  expect(await fixture(page)).toMatchObject({ logouts: 1, puts: 0 });
});

test("quota failure does not acknowledge local evidence or send checkout", async ({ page, context }) => {
  await context.addInitScript(() => {
    const original = IDBObjectStore.prototype.add;
    IDBObjectStore.prototype.add = function (...args) {
      if (this.name === "sales") throw new DOMException("Fixture quota", "QuotaExceededError");
      return original.apply(this, args);
    };
  });
  await ready(page);
  await addCart(page);
  await page.getByRole("button", { name: "Checkout" }).click();
  await expect(page.locator("#notice")).toHaveText("Bukti transaksi gagal disimpan. Checkout tidak dikirim.");
  expect(await fixture(page)).toMatchObject({ puts: 0, orders: 0 });
  expect(await stored(page, "sales")).toHaveLength(0);
  await context.setOffline(true);
  await page.getByRole("button", { name: "Simpan draf tunai" }).click();
  await expect(page.locator("#notice")).toHaveText("Penyimpanan lokal gagal. Draf belum tersimpan.");
});

for (const [mode, reason] of [["shortage", "Stok tidak cukup"], ["stale", "Harga, cakupan"], ["entitlement_expired", "izin tidak lagi berlaku"]]) {
  test(`preserves ${mode} evidence for review`, async ({ page }) => {
    await ready(page);
    await addCart(page);
    await fixture(page, { mode });
    await page.getByRole("button", { name: "Checkout" }).click();
    await expect(page.locator("#queue")).toContainText(reason);
    expect((await stored(page, "sales"))[0].state).toBe("needs_review");
    expect(await fixture(page)).toMatchObject({ puts: 1, orders: 0 });
  });
}

for (const mode of ["revoked", "suspended", "session_expired"]) {
  test(`fails closed for ${mode} after restart and retains the queue`, async ({ page }) => {
    await ready(page);
    await addCart(page);
    await fixture(page, { mode: "lose_response" });
    await page.getByRole("button", { name: "Checkout" }).click();
    await expect(page.locator("#notice")).toContainText("belum pasti");
    await fixture(page, { mode });
    await page.reload();
    await expect(page.getByRole("heading", { name: "Masuk sebagai operator" })).toBeVisible();
    expect((await stored(page, "sales"))[0].state).toBe("pending");
    expect(await fixture(page)).toMatchObject({ puts: 1, acks: 0 });
  });
}

test("detectable eviction locks checkout and pairing", async ({ page }) => {
  await ready(page);
  await page.goto("/__fixture/blank");
  await page.evaluate(() => new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase("hi-ve-app");
    req.onsuccess = resolve;
    req.onerror = reject;
  }));
  await page.goto("/");
  await expect(page.locator("#notice")).toContainText("Penyimpanan perangkat hilang");
  await expect(page.getByRole("button", { name: "Buat kode pairing" })).toBeHidden();
  await expect(page.getByRole("button", { name: "Checkout" })).toBeHidden();
});

test("unknown server outcome stays in review without posting", async ({ page }) => {
  await ready(page);
  await addCart(page);
  await fixture(page, { mode: "lose_before_commit" });
  await page.getByRole("button", { name: "Checkout" }).click();
  await expect(page.locator("#notice")).toContainText("belum pasti");
  await fixture(page, { mode: "" });
  await page.getByRole("button", { name: "Cek bukti server" }).click();
  await expect(page.locator("#queue")).toContainText("Perlu tinjauan");
  expect(await fixture(page)).toMatchObject({ puts: 1, orders: 0, acks: 1 });
});

test("manual payment stays pending until online confirmation", async ({ page }) => {
  await ready(page);
  await addCart(page);
  await page.getByLabel("Metode pembayaran").selectOption("manual_non_cash");
  await page.getByRole("button", { name: "Checkout" }).click();
  await expect(page.locator("#receipt-data")).toContainText("Menunggu konfirmasi manual");
  expect(await stored(page, "sales")).toHaveLength(0);
  await page.getByRole("button", { name: "Muat histori" }).click();
  page.once("dialog", dialog => dialog.accept("REF-ONLINE"));
  await page.getByRole("button", { name: "Catat pembayaran manual" }).click();
  await expect(page.locator("#receipt-data")).toContainText("Dikonfirmasi manual");
});
