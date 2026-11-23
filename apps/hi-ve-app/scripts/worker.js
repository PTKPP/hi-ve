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
