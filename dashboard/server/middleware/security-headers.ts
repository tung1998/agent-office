// The dashboard's own pages may not be framed by another site (clickjacking)
// and are not type-sniffed. /api and /hooks are left to the Go server, which
// sets its own (attachments carry their sandboxed CSP).
export default defineEventHandler((event) => {
  const path = event.path
  if (path.startsWith('/api/') || path.startsWith('/hooks/')) return
  setResponseHeaders(event, {
    'X-Frame-Options': 'DENY',
    'Content-Security-Policy': 'frame-ancestors \'none\'',
    'X-Content-Type-Options': 'nosniff',
    'Referrer-Policy': 'same-origin'
  })
})
