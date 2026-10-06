// Forward /hooks/** (automation webhooks, ADR-040) to the Go office server.
export default defineEventHandler((event) => {
  const { officeApiBase } = useRuntimeConfig(event)
  return proxyRequest(event, officeApiBase.replace(/\/+$/, '') + event.path, {
    headers: { 'x-forwarded-for': clientIP(event) }
  })
})
