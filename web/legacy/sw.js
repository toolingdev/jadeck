/* App-shell cache for PWA installability. API/WebSocket/stream traffic is
   never cached. Bump VERSION to invalidate. */
const VERSION = "ptz-shell-v6";
const SHELL = ["/", "/dashboard", "/static/style.css",
               "/static/app.js", "/static/dashboard.js",
               "/static/icon.svg", "/static/manifest.webmanifest"];
self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(VERSION).then((c) => c.addAll(SHELL))
    .then(() => self.skipWaiting()));
});
self.addEventListener("activate", (e) => {
  e.waitUntil(caches.keys().then((keys) => Promise.all(
    keys.filter((k) => k !== VERSION).map((k) => caches.delete(k))))
    .then(() => self.clients.claim()));
});
self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/ws")) return;
  e.respondWith(caches.match(e.request).then((hit) => hit || fetch(e.request)));
});
