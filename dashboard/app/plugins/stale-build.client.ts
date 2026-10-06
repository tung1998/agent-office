// A tab opened before "Cập nhật office" asks for bundles of the old build,
// gone since the swap: load the page again (the new build) instead of a 500.
// reloadNuxtApp's ttl keeps it from looping if the bundle is really missing.
const stale = /dynamically imported module|Importing a module script failed|error loading dynamically imported module|Unable to preload CSS/i

export default defineNuxtPlugin((nuxtApp) => {
  const reload = () => reloadNuxtApp({ persistState: false, ttl: 10_000 })
  nuxtApp.hook('app:chunkError', reload)
  nuxtApp.hook('app:error', (err) => {
    if (stale.test(String((err as Error)?.message ?? err))) reload()
  })
  window.addEventListener('vite:preloadError', (e) => {
    e.preventDefault()
    reload()
  })
  window.addEventListener('unhandledrejection', (e) => {
    if (stale.test(String(e.reason?.message ?? e.reason))) reload()
  })
})
