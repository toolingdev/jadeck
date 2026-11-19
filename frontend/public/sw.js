/* App-shell cache so the installed app opens instantly. API, WebSocket,
   MCP and stream traffic is never cached. The cache is network-first, so updates show at once. */
const VERSION = "ptz-shell-v1";
self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(VERSION).then((c) => c.addAll(["/", "/manifest.webmanifest", "/icon.svg"])).then(() => self.skipWaiting()));
});
self.addEventListener("activate", (e) => {
  e.waitUntil(caches.keys().then((ks) => Promise.all(ks.filter((k) => k !== VERSION).map((k) => caches.delete(k)))).then(() => self.clients.claim()));
});
self.addEventListener("fetch", (e) => {
  const u = new URL(e.request.url);
  if (e.request.method !== "GET" || u.origin !== location.origin) return;
  if (u.pathname.startsWith("/api/") || u.pathname.startsWith("/ws") || u.pathname.startsWith("/mcp")) return;
  // Network first (fresh after an update), cache as fallback.
  e.respondWith(
    fetch(e.request).then((r) => {
      if (r.ok) {
        const copy = r.clone();
        caches.open(VERSION).then((c) => c.put(e.request, copy));
      }
      return r;
    }).catch(() => caches.match(e.request).then((h) => h || caches.match("/")))
  );
});
