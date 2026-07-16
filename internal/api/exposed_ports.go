package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ExposedPort is a single (service name, container-side port) pair declared by
// a compose-mode app. Caddy uses the port as the upstream target; the name
// matches a service in the app's compose file and a container name of the
// shape `nanoku-<app>-<name>-1` once the stack is up.
//
// The struct is also serialized to JSON in the App.exposed_ports DB column,
// so changing field tags / names is a wire-format break.
type ExposedPort struct {
	// Service name as it appears in the compose file (e.g. "web", "api").
	// Must be DNS-1123 label-friendly: lowercase, alnum + dash, ≤ 63 chars.
	// Matches the form compose uses to suffix container names so that
	// `nanoku-<app>-<name>-1` is the actual running container we proxy to.
	Name string `json:"name"`
	// Container-side port the service listens on (1..65535). Caddy
	// reverse-proxies to `<container-name>:<port>` over the nanoku network.
	Port int `json:"port"`
	// ContainerName is the actual docker container name for this service
	// when it differs from the compose-default `nanoku-<app>-<name>-1`.
	// Set from the compose file's `container_name:` field at import time
	// (or by hand in the editor); empty means "use the default". Caddy
	// upstream and the deploy-time missing-check both read through
	// resolveServiceContainerName so the two stay in lockstep.
	ContainerName string `json:"containerName"`
}

// serviceNameRe mirrors compose's constraint on service names: a service name
// must start with a letter or digit and may contain letters, digits,
// underscores, and hyphens. We tighten underscores away so the resulting
// container suffix stays DNS-1123 label compatible (docker compose allows
// underscores, but other downstream consumers — and our own Caddyfile
// rendering — prefer dashes). Keeping it strict here also rules out
// injection attempts where a name like "../etc" would otherwise slip
// through a less strict validator.
var serviceNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// containerNameRe is the validation pattern for ExposedPort.ContainerName
// — the user-supplied actual docker container name for the service. It is
// intentionally broader than serviceNameRe (docker itself accepts
// `[a-zA-Z0-9_.-]`, ≤ 64 chars); the user's compose `container_name:`
// may contain underscores or upper-case that compose defaults never
// produce, and we want to round-trip whatever the user wrote. We still
// reject spaces, slashes, and other shell-special chars that would
// break the Caddyfile or the deploy-time `docker compose ps` lookup.
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

// ParseExposedPorts decodes the App.exposed_ports JSON column. The column is
// nullable; a nil/empty raw value is a valid "no exposed_ports" state and
// returns (nil, nil) — callers should not treat that as an error.
//
// Invalid JSON, duplicate service names, missing names, or out-of-range
// ports all return a wrapped error so the create/update handler can surface
// the field name in the API response.
func ParseExposedPorts(raw *string) ([]ExposedPort, error) {
	if raw == nil {
		return nil, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return nil, nil
	}
	var out []ExposedPort
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("exposed_ports: invalid JSON: %w", err)
	}
	if len(out) == 0 {
		// Treat "[]" the same as nil/empty raw so the DB column is
		// always NULLABLE for the "no exposed_ports" case. Without
		// this normalization, an empty-array input produces a
		// non-nil slice that round-trips as "[]" instead of
		// collapsing to NULL on the next write.
		return nil, nil
	}
	seen := make(map[string]struct{}, len(out))
	for i, ep := range out {
		name := strings.TrimSpace(ep.Name)
		if name == "" {
			return nil, fmt.Errorf("exposed_ports[%d]: name is required", i)
		}
		if !serviceNameRe.MatchString(name) {
			return nil, fmt.Errorf("exposed_ports[%d]: name %q must match %s", i, name, serviceNameRe.String())
		}
		if ep.Port < 1 || ep.Port > 65535 {
			return nil, fmt.Errorf("exposed_ports[%d]: port must be 1..65535", i)
		}
		if cn := strings.TrimSpace(ep.ContainerName); cn != "" {
			if !containerNameRe.MatchString(cn) {
				return nil, fmt.Errorf("exposed_ports[%d]: containerName %q must match %s", i, cn, containerNameRe.String())
			}
			ep.ContainerName = cn
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("exposed_ports[%d]: duplicate name %q", i, name)
		}
		seen[name] = struct{}{}
		ep.Name = name
		out[i] = ep
	}
	// Sort by name for stable diffs / output ordering. The UI is free to
	// re-order for display; the DB column has a canonical order so two
	// semantically-equal lists always produce the same string.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// MarshalExposedPorts encodes a list to the App.exposed_ports column format.
// Returns ("", nil) for an empty/nil list so the column stays NULLABLE in
// the DB rather than carrying an empty JSON array.
func MarshalExposedPorts(ports []ExposedPort) (*string, error) {
	if len(ports) == 0 {
		return nil, nil
	}
	// Validate before encoding so a 400 from the input layer never produces
	// a partially-valid column write.
	seen := make(map[string]struct{}, len(ports))
	for i, ep := range ports {
		name := strings.TrimSpace(ep.Name)
		if name == "" {
			return nil, fmt.Errorf("exposed_ports[%d]: name is required", i)
		}
		if !serviceNameRe.MatchString(name) {
			return nil, fmt.Errorf("exposed_ports[%d]: name %q must match %s", i, name, serviceNameRe.String())
		}
		if ep.Port < 1 || ep.Port > 65535 {
			return nil, fmt.Errorf("exposed_ports[%d]: port must be 1..65535", i)
		}
		if cn := strings.TrimSpace(ep.ContainerName); cn != "" {
			if !containerNameRe.MatchString(cn) {
				return nil, fmt.Errorf("exposed_ports[%d]: containerName %q must match %s", i, cn, containerNameRe.String())
			}
			ports[i].ContainerName = cn
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("exposed_ports[%d]: duplicate name %q", i, name)
		}
		seen[name] = struct{}{}
		ports[i].Name = name
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Name < ports[j].Name })
	b, err := json.Marshal(ports)
	if err != nil {
		return nil, fmt.Errorf("marshal exposed_ports: %w", err)
	}
	s := string(b)
	return &s, nil
}

// findExposedPort returns the ExposedPort matching name, or nil if absent.
// Callers should check for nil before dereferencing.
func findExposedPort(ports []ExposedPort, name string) *ExposedPort {
	for i := range ports {
		if ports[i].Name == name {
			return &ports[i]
		}
	}
	return nil
}

// errExposedPortsInvalid is a sentinel callers can use to distinguish
// input validation failures from unexpected JSON errors. Currently unused
// but kept here so handlers don't reach for errors.Is with a string
// comparison in the future.
var errExposedPortsInvalid = errors.New("exposed_ports invalid")
