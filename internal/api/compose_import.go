package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeService is the subset of the docker-compose schema we care
// about for the "Import from compose" button. We only need to find
// (service name, port) pairs; everything else in the file is opaque.
//
// `ports` is intentionally typed as `yaml.Node` rather than a
// concrete slice because the field accepts at least three shapes in
// docker-compose v2/v3:
//
//	ports:
//	  - "8080:80"   # host:container
//	  - "80"        # container only
//	  - 80          # YAML integer
//	  - ["80", "443"]
//
// and we want a single decoder that doesn't error on the integer
// form. A typed []string would reject the int form.
type composeService struct {
	Ports         yaml.Node `yaml:"ports"`
	Expose        yaml.Node `yaml:"expose"`
	ContainerName string    `yaml:"container_name"`
}

// composeFile is the top-level shape we decode into. Anything not
// listed (volumes, networks, configs, version, …) is ignored.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

// ImportExposedPortsFromCompose parses a docker-compose YAML string
// and returns a list of ExposedPort scaffolding the operator can
// review / edit before saving. Service names come from the `services:`
// map keys. Port picking is heuristic — see pickServicePort for the
// priority — so the result is best-effort, not authoritative. The
// caller is expected to let the user edit the result before the
// value lands in App.exposed_ports.
//
// Returns an error only when the YAML itself is unparseable; a
// compose file with no services or no ports is a valid (but empty)
// result.
func ImportExposedPortsFromCompose(content string) ([]ExposedPort, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}
	var cf composeFile
	if err := yaml.Unmarshal([]byte(content), &cf); err != nil {
		return nil, fmt.Errorf("parse compose YAML: %w", err)
	}
	if len(cf.Services) == 0 {
		return nil, nil
	}

	out := make([]ExposedPort, 0, len(cf.Services))
	for name, svc := range cf.Services {
		// Skip nanoku-internal service names that should never
		// appear as user-facing exposed services. Right now this
		// is just a defensive guard — the operator writes the
		// compose file, so any service in `services:` is theirs —
		// but it costs nothing and keeps the result predictable
		// for the common "caddy" / "nanoku" service names that
		// appear when a user pastes a sidecar pattern.
		if name == "" {
			continue
		}
		out = append(out, ExposedPort{
			Name:          name,
			Port:          pickServicePort(svc),
			ContainerName: importableContainerName(svc.ContainerName),
		})
	}

	// Stable order so the result is reproducible — the editor
	// renders the list in the same order on every import.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// pickServicePort returns the most likely "container-side" port for
// a service. The priority is:
//
//  1. expose[0] — the operator wrote a hint saying "this is the
//     service-facing port". Caddy should use this.
//  2. ports[0] with the container-side half parsed out — for
//     "8080:80" the answer is 80 (NOT 8080, which is the host port
//     and irrelevant on the nanoku internal network).
//  3. 0 — caller leaves the port field empty / 0 in the UI for the
//     user to fill in.
//
// Returning 0 (rather than skipping the service entirely) is
// deliberate: the operator probably wants this service in the
// exposed_ports list and just forgot to declare a port. The UI
// surfaces the row with port=0 so it's visible, not silently
// dropped. The ExposedPortsSection editor renders the input as
// empty when port=0; the server's MarshalExposedPorts validation
// will reject a port<=0 on save, so an operator who leaves the row
// untouched still gets a clear 400 with the field name.
func pickServicePort(svc composeService) int {
	if p, ok := firstScalarInt(svc.Expose); ok {
		return p
	}
	if p, ok := firstPortFromPorts(svc.Ports); ok {
		return p
	}
	return 0
}

// importableContainerName returns the trimmed container_name:
// field if it is a literal value the operator can rely on. Compose
// itself supports env-var interpolation in container_name:
// (e.g. `container_name: ${NAME}`), but the result is only known
// after `docker compose up` substitutes the variable, which is
// *after* this import runs. Storing a `${...}` literal would force
// the Caddy upstream to carry a never-resolving host, so we
// treat any `$` placeholder as "absent" and let the deploy-time
// compose-default name take over. Operators can still hand-edit
// the resolved value in the editor once the env is known.
func importableContainerName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "$") {
		return ""
	}
	return raw
}

// firstScalarInt reads the first scalar integer from a yaml.Node
// that may be a sequence, a single scalar, or absent. Returns
// (0, false) when the node is nil / empty / not an int.
func firstScalarInt(n yaml.Node) (int, bool) {
	if isEmptyNode(n) {
		return 0, false
	}
	// Sequence: take the first entry that's a scalar int.
	if n.Kind == yaml.SequenceNode {
		for _, item := range n.Content {
			if item.Kind == yaml.ScalarNode && item.Tag == "!!int" {
				if v, err := strconv.Atoi(item.Value); err == nil {
					return v, true
				}
			}
		}
		// First entry was a string like "80" — try that.
		for _, item := range n.Content {
			if item.Kind == yaml.ScalarNode {
				if v, err := strconv.Atoi(item.Value); err == nil {
					return v, true
				}
				return 0, false
			}
		}
		return 0, false
	}
	// Single scalar.
	if n.Kind == yaml.ScalarNode {
		if v, err := strconv.Atoi(n.Value); err == nil {
			return v, true
		}
	}
	return 0, false
}

// firstPortFromPorts takes the first entry of a `ports:` sequence
// and, if it's a "host:container" string, returns the container
// half. For a bare "80" or 80 it returns 80 directly. The host
// half is intentionally discarded — it's only meaningful for
// host-side binding and Caddy is going to talk to the container
// over the nanoku network, not to the host.
//
//   "8080:80"  → 80
//   "80"       → 80
//   80         → 80
//   "127.0.0.1:8080:80" → 80 (long form with bind IP, container
//   half is still last)
func firstPortFromPorts(n yaml.Node) (int, bool) {
	if isEmptyNode(n) {
		return 0, false
	}
	if n.Kind != yaml.SequenceNode || len(n.Content) == 0 {
		return 0, false
	}
	item := n.Content[0]
	if item.Kind != yaml.ScalarNode {
		return 0, false
	}
	raw := item.Value
	// Strip surrounding quotes (single or double) so
	// `ports: ["80"]` and `ports: [80]` both parse.
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return 0, false
	}
	// Split on ":" and take the last segment as the container
	// port. "80" → ["80"] → 80. "8080:80" → ["8080", "80"] → 80.
	parts := strings.Split(raw, ":")
	last := parts[len(parts)-1]
	port, err := strconv.Atoi(last)
	if err != nil {
		return 0, false
	}
	if port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

// isEmptyNode returns true for a yaml.Node that is the zero value
// or whose decoded content is empty. A node that was never
// assigned is a valid signal that the field was absent in the YAML
// (e.g. no `expose:` key), so we treat that as "no value here".
func isEmptyNode(n yaml.Node) bool {
	if n.IsZero() {
		return true
	}
	return n.Kind == 0
}
