import type { H3Event } from 'h3'

// The client's IP for the Go server's audit log and login limits. A browser
// can send any X-Forwarded-For, so it is only believed when the peer is on
// this machine (a tunnel such as cloudflared), and then only its last entry,
// the one that local proxy added. Anyone else is who the socket says.
export function clientIP(event: H3Event): string {
  const peer = getRequestIP(event) ?? ''
  if (!isLoopback(peer)) return peer
  const xff = getRequestHeader(event, 'x-forwarded-for')
  const last = xff?.split(',').map(s => s.trim()).filter(Boolean).pop()
  return last || peer
}

// The scheme, believed from X-Forwarded-Proto only behind a local proxy.
export function clientProto(event: H3Event): string {
  return isLoopback(getRequestIP(event) ?? '') ? getRequestProtocol(event) : getRequestProtocol(event, { xForwardedProto: false })
}

function isLoopback(ip: string): boolean {
  return ip === '::1' || ip.startsWith('127.') || ip.startsWith('::ffff:127.')
}
