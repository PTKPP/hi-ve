const prefix = "hi-ve-app-static-";
const cacheName = prefix + "__CACHE_VERSION__";
const assetPaths = new Set(__STATIC_PATHS__);

self.addEventListener("install", event => {
  event.waitUntil((async () => {
    const cache = await caches.open(cacheName);
    await cache.addAll([...assetPaths].map(path => new Request(path, { cache: "reload", redirect: "error" })));
  })());
});

self.addEventListener("activate", event => {
  event.waitUntil((async () => {
    for (const key of await caches.keys()) {
      if (key.startsWith(prefix) && key !== cacheName) await caches.delete(key);
    }
    await self.clients.claim();
  })());
});

self.addEventListener("fetch", event => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== "GET" || url.origin !== self.location.origin || url.search) return;
  const path = url.pathname;
  if (!assetPaths.has(path)) return;
  // Only build artifacts enter Cache Storage. API/session/payment responses go straight to the network.
  event.respondWith((async () => {
    const cache = await caches.open(cacheName);
    return await cache.match(path) ?? fetch(request);
  })());
});

self.addEventListener("push", event => {
  if(!event.data) return;
  let data;
  try { data=event.data.json(); } catch { return; }
  if(data.type!=="sale.received"||typeof data.id!=="string"||!/^[-:0-9a-f]{1,80}$/.test(data.id)) return;
  event.waitUntil((async()=>{
    const tag="hi-ve-sale-"+data.id;
    if((await self.registration.getNotifications({tag})).length) return;
    await self.registration.showNotification("Hi-Ve", {body:"Penjualan baru diterima server.",tag,renotify:false});
  })());
});
self.addEventListener("notificationclick",event=>{
  event.notification.close();
  event.waitUntil(self.clients.openWindow("/"));
});
