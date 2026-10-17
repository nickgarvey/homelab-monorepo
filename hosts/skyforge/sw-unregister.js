// Served at /sw.js in place of Mainsail's service worker (web-gate.nix).
// Mainsail registers /sw.js on every page load; this worker deletes the
// caches, unregisters itself, and reloads the open tabs only if there were
// caches, so the second load (nothing cached) does not reload again.
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys.map((k) => caches.delete(k)));
    await self.registration.unregister();
    if (keys.length > 0) {
      const tabs = await self.clients.matchAll({ type: "window" });
      tabs.forEach((tab) => tab.navigate(tab.url));
    }
  })());
});
