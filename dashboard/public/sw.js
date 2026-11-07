// Browser notifications of what needs a person (plugins/notify.client.ts
// shows them): their buttons act right from the notification (Duyệt, Bỏ qua,
// Đã xem…, as composables/useApprovals.ts), with the dashboard's own session
// cookie; a click elsewhere opens where it is.
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', e => e.waitUntil(self.clients.claim()))

function request(act, x) {
  switch (act) {
    case 'approve':
    case 'reject':
      return [(x.kind === 'patch' ? '/api/patches/' : '/api/actions/') + x.id + '/' + act, 'POST', act === 'reject' ? { skip: true } : {}]
    case 'seen':
      return ['/api/conversations/' + x.id + '/seen', 'POST', { seen: true }]
    case 'continue':
      return ['/api/conversations/' + x.id + '/messages', 'POST', { text: x.prompt }]
    case 'dismiss':
      return ['/api/incidents/dismiss', 'POST', { key: x.key }]
    case 'retryNow':
      return ['/api/conversations/' + x.id + '/retry-now', 'POST', {}]
    case 'cancelRetry':
      return ['/api/conversations/' + x.id + '/retry', 'DELETE', null]
  }
  return null
}

self.addEventListener('notificationclick', (event) => {
  const n = event.notification
  const d = n.data || {}
  const x = d.incident || {}
  n.close()
  event.waitUntil((async () => {
    const req = request(event.action, x)
    if (req) {
      try {
        const [url, method, body] = req
        const res = await fetch(url, {
          method, credentials: 'same-origin',
          headers: body === null ? undefined : { 'Content-Type': 'application/json' },
          body: body === null ? undefined : JSON.stringify(body)
        })
        if (res.ok) return // done: the notification is gone, nothing more to show
        if (res.status === 409) { // decided first elsewhere (or by office on its own): said so, nothing to open
          const d = await res.json().catch(() => ({}))
          const card = d.action || d.patch || {}
          const by = card.deciding || card.decided_by || ''
          await self.registration.showNotification(n.title, { body: (card.deciding ? '⏳ ' : '✓ ') + (by.startsWith('auto:') ? 'auto' : by), tag: x.key + ':decided' })
          return
        }
      } catch { /* offline: open it instead */ }
    }
    const link = x.link || '/'
    const tabs = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    const tab = tabs.find(c => c.url.startsWith(self.location.origin))
    if (tab) {
      await tab.focus()
      tab.postMessage({ type: 'open', link })
    } else {
      await self.clients.openWindow(new URL(link, self.location.origin).href)
    }
  })())
})
