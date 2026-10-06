// Forward /api/** to the Go office server. Cookies and Set-Cookie pass through;
// forwarded headers let the Go side check Origin and log the real client IP.
export default defineEventHandler((event) => {
  const { officeApiBase } = useRuntimeConfig(event)
  return proxyRequest(event, officeApiBase.replace(/\/+$/, '') + event.path, {
    headers: {
      'x-forwarded-host': getRequestHost(event),
      'x-forwarded-proto': clientProto(event),
      'x-forwarded-for': clientIP(event)
    }
  })
})
