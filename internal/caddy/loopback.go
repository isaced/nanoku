package caddy

import (
	"net"
	"strings"
)

// IsLoopbackDomain reports whether the given domain (or IP literal) refers to
// the local machine. Used to opt loopback sites out of ACME — Caddy cannot
// issue a real cert for `localhost` and trying causes a noisy reload failure.
//
// Recognized forms:
//   - "localhost" (any case)
//   - "*.localhost" — RFC 6761 reserves the whole TLD for loopback
//   - 127.0.0.0/8   (IPv4 loopback)
//   - ::1            (IPv6 loopback)
//
// Bare hostnames ending in `.local` (mDNS) are NOT loopback by themselves —
// they resolve via mDNS and behave like normal hostnames, so we don't
// auto-route them. Users with `.local` domains can set NANOKU_CADDY_AUTO_HTTPS=false
// globally instead.
func IsLoopbackDomain(domain string) bool {
	d := strings.TrimSpace(strings.ToLower(domain))
	if d == "" {
		return false
	}
	if d == "localhost" {
		return true
	}
	if strings.HasSuffix(d, ".localhost") {
		return true
	}
	// IP literal? net.ParseIP handles both IPv4 and IPv6.
	if ip := net.ParseIP(d); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
