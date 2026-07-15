// Mirror of internal/caddy/IsLoopbackDomain — used by the site editor's
// domain field to allow local-dev addresses that don't fit the FQDN shape
// (no dot, no real TLD). Kept in sync manually; both sides have tests.
const FQDN_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*\.[a-z]{2,}$/i
const IPV4_OCTET = '(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)'
const IPV4_LOOPBACK_RE = new RegExp(`^127(?:\\.${IPV4_OCTET}){3}$`)
const IPV6_LOOPBACK = '::1'

export const MAX_DOMAIN_LENGTH = 253

export function isLoopbackDomain(raw: string): boolean {
  const d = raw.trim().toLowerCase()
  if (d === 'localhost') return true
  if (d.endsWith('.localhost')) return true
  if (IPV4_LOOPBACK_RE.test(d)) return true
  if (d === IPV6_LOOPBACK) return true
  return false
}

export function isValidDomain(raw: string): boolean {
  const d = raw.trim()
  if (d === '') return false
  if (d.length > MAX_DOMAIN_LENGTH) return false
  if (isLoopbackDomain(d)) return true
  return FQDN_RE.test(d.toLowerCase())
}
