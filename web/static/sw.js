/* Service worker of Bytewise Panel: shows push messages and keeps the
 * number on the app icon (servers with a problem). No caching: the panel
 * always needs the live server anyway. */
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', e => e.waitUntil(self.clients.claim()));

self.addEventListener('push', e => {
  let d = {};
  try { d = e.data ? e.data.json() : {}; } catch (err) { d = { title: 'Bytewise Panel', body: e.data ? e.data.text() : '' }; }
  const jobs = [self.registration.showNotification(d.title || 'Bytewise Panel', {
    body: d.body || '',
    tag: d.tag || undefined,
    icon: 'img/icon-192.png',
    badge: 'img/icon-192.png',
    data: { url: d.url || '/' }
  })];
  if (typeof d.badge === 'number' && self.navigator.setAppBadge) {
    jobs.push(d.badge > 0 ? self.navigator.setAppBadge(d.badge) : self.navigator.clearAppBadge());
  }
  e.waitUntil(Promise.all(jobs).catch(() => {}));
});

self.addEventListener('notificationclick', e => {
  e.notification.close();
  const url = new URL((e.notification.data && e.notification.data.url) || '/', self.location.origin).href;
  e.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(list => {
    for (const c of list) {
      if (new URL(c.url).origin === self.location.origin) { c.navigate(url).catch(() => {}); return c.focus(); }
    }
    return self.clients.openWindow(url);
  }));
});
