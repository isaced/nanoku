package api

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/isaced/nanoku/internal/caddy"
)

// MaxDomainLength matches RFC 1035's 253-octet cap on a full domain name.
const MaxDomainLength = 253

// fqdnPattern matches the same shape as the UI's old pattern: at least one
// dot, the final label is 2+ ASCII letters, the rest is letters/digits/dots/
// hyphens. Loopback addresses (localhost, 127.0.0.1, ::1, *.localhost) are
// handled separately by caddy.IsLoopbackDomain and bypass this regex.
var fqdnPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*\.[a-z]{2,}$`)

// validateDomain is the server-side mirror of the UI's domain form rule.
// It exists so direct API calls (curl, scripted deploys) can't bypass the
// browser check. Loopback addresses are intentionally allowed — they're
// how users point Nanoku at a local dev backend without owning a DNS name.
//
// Returns nil when the value is acceptable.
func validateDomain(raw string) error {
	domain := strings.TrimSpace(raw)
	if domain == "" {
		return errors.New("domain is required")
	}
	if len(domain) > MaxDomainLength {
		return fmt.Errorf("domain must be %d characters or fewer", MaxDomainLength)
	}
	if caddy.IsLoopbackDomain(domain) {
		return nil
	}
	if !fqdnPattern.MatchString(strings.ToLower(domain)) {
		return errors.New("domain must look like a fully-qualified name (e.g. app.example.com) or a loopback address (localhost, 127.0.0.1)")
	}
	return nil
}
