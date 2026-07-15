package api

import (
	"strings"
	"testing"
)

func TestValidateDomain(t *testing.T) {
	cases := []struct {
		name   string
		domain string
		wantOK bool
	}{
		// accepted
		{"plain fqdn", "app.example.com", true},
		{"uppercase fqdn", "APP.Example.com", true},
		{"hyphen in middle", "my-app.example.com", true},
		{"multi-level", "a.b.c.example.co", true},
		{"two-letter tld", "foo.xx", true},
		{"localhost bare", "localhost", true},
		{"localhost mixed case", "LocalHost", true},
		{"localhost subdomain", "app.localhost", true},
		{"localhost with whitespace", "  localhost  ", true},
		{"ipv4 loopback", "127.0.0.1", true},
		{"ipv4 127/8", "127.5.6.7", true},
		{"ipv6 loopback", "::1", true},

		// rejected
		{"empty", "", false},
		{"whitespace only", "   ", false},
		{"no dot not loopback", "intranet", false},
		{"single-letter tld", "foo.x", false},
		{"trailing dot", "app.example.com.", false},
		{"leading hyphen label", "-bad.example.com", false},
		{"trailing hyphen label", "bad-.example.com", false},
		{"contains space", "app example.com", false},
		{"contains slash", "app/example.com", false},
		{"contains scheme", "http://app.example.com", false},
		{"uppercase tld is fine, but junk label not", "app.eXAMPLE.123", false},
		{"too long", "a" + strings.Repeat("b", 252) + ".com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDomain(tc.domain)
			if tc.wantOK && err != nil {
				t.Errorf("validateDomain(%q) = %v, want nil", tc.domain, err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("validateDomain(%q) = nil, want error", tc.domain)
			}
		})
	}
}
