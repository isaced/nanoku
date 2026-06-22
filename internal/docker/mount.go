package docker

import (
	"fmt"
	"strings"
)

// VolumeMount describes a single mount entry for an app container.
// Source is the volume name (Type=volume) or host path (Type=bind). When
// Type=volume and Source is empty, docker.Manager auto-names it as
// `nanoku-<appName>-vol-<idx>` (see AutoAppVolumeName).
type VolumeMount struct {
	Type     string
	Source   string
	Target   string
	ReadOnly bool
}

// AutoAppVolumeName returns the canonical name nanoku assigns to an unnamed
// app volume at the given index.
func AutoAppVolumeName(appName string, idx int) string {
	return fmt.Sprintf("nanoku-%s-vol-%d", appName, idx)
}

// IsAutoAppVolumeName reports whether name follows the auto-naming
// convention for a given app.
func IsAutoAppVolumeName(appName, name string) bool {
	prefix := fmt.Sprintf("nanoku-%s-vol-", appName)
	return strings.HasPrefix(name, prefix) && len(name) > len(prefix)
}

// formatMount renders a single --mount value for the docker CLI. Kept here
// for parity with historical behavior; the SDK path uses container.Mount
// directly. Output is safe to pass via a single argv slot.
func formatMount(typ, source, target string, readOnly bool) string {
	parts := []string{
		"type=" + typ,
		"source=" + source,
		"target=" + target,
	}
	if readOnly {
		parts = append(parts, "readonly")
	}
	return strings.Join(parts, ",")
}