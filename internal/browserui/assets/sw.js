const CACHE = "ecs-browser-leaf-v2";
const ASSETS = [
  "/browser/",
  "/browser/styles.css",
  "/browser/app.js",
  "/browser/manifest.webmanifest",
  "/browser/favicon.png",
  "/browser/apple-touch-icon.png",
  "/browser/icon-192.png",
  "/browser/icon-512.png",
  "/browser/icon-maskable-512.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(ASSETS)));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(caches.keys().then((keys) =>
    Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key)))));
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  if (event.request.method !== "GET") return;
  event.respondWith(fetch(event.request)
    .then((response) => {
      const copy = response.clone();
      caches.open(CACHE).then((cache) => cache.put(event.request, copy));
      return response;
    })
    .catch(() => caches.match(event.request)));
});
