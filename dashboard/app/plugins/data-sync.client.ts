// What this page changed shows at once, even before the server's notice
// (ADR-072): a successful write refreshes what shows its tables; a tab back
// into view refreshes all (its stream may have slept).
export default defineNuxtPlugin(() => {
  const base = globalThis.$fetch
  globalThis.$fetch = base.create({
    onResponse({ request, options, response }) {
      const method = String(options.method ?? 'GET').toUpperCase()
      const url = typeof request === 'string' ? request : request instanceof Request ? request.url : String(request)
      // a check that writes nothing (validate, test, preview) refreshes nothing
      if (method !== 'GET' && response.ok && url.includes('/api/') && !/\/(validate|test|preview|scan|content)(\?|$)/.test(url)) {
        const path = url.replace(/^https?:\/\/[^/]+/, '')
        const tables = tablesFor(path)
        if (tables.length) liveChanged(tables)
      }
    }
  }) as typeof base
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') liveChanged(['*'])
  })
})
