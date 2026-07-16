package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/volume"
	"github.com/isaced/nanoku/internal/docker"
)

// compose_helpers.go collects the small, pure helpers we use to
// manage docker-compose stacks from nanoku: the on-disk path
// conventions, the compose project name (which doubles as the
// container prefix), and the file-read / atomic-write plumbing
// for the inline compose YAML we persist on the App row.
//
// Everything in this file is composable — none of it touches
// docker or the DB directly, so it's safe to use from both the
// HTTP handler path and the deploy worker.

// composeBaseDir returns the on-disk directory where nanoku
// stores per-app docker-compose.yml files when deploy_method=compose
// and compose_path is empty. The Handlers struct carries the
// base path as a field so tests can point it at a t.TempDir().
//
// Default layout: $ComposeBaseDir/$appName/docker-compose.yml.
func (h *Handlers) composeFilePath(appName string) string {
	return filepath.Join(h.ComposeBaseDir, appName, "docker-compose.yml")
}

// composeProjectName returns the compose project name nanoku
// uses for an app. Format: "nanoku-<app-name>". This controls the
// network / container prefix compose assigns to every service
// in the stack, so we derive the upstream target (e.g.
// nanoku-<app>-<service>-1) from this same prefix.
func composeProjectName(appName string) string {
	return "nanoku-" + appName
}

// composeServiceContainerName returns the docker-compose
// container name for a service within an app's stack. The -1
// suffix is compose's default replica index; multi-replica
// stacks (v2) will need a different pick.
//
// This is the upstream target Caddy reverse-proxies to: the
// compose network routes <container-name>:<port> to the right
// service without us having to think about host ports.
func composeServiceContainerName(appName, service string) string {
	return "nanoku-" + appName + "-" + service + "-1"
}

// resolveServiceContainerName returns the host:port component
// for a site targeting a compose service. Order of resolution:
//
//  1. override — set by the operator or imported from the
//     compose file's `container_name:` field. Required when the
//     user opts out of the compose-default name (otherwise the
//     Caddyfile upstream and the deploy-time missing-check both
//     miss the actual running container and the route 502s).
//  2. compose default — `nanoku-<app>-<service>-1`.
//
// We trust the override if it matches the docker container-name
// pattern (see containerNameRe). Anything with shell-special
// characters, spaces, or path separators falls through to the
// default rather than reaching the Caddyfile / docker cli — both
// of which would be unsafe to inject unchecked.
func resolveServiceContainerName(appName, service, override string) string {
	override = strings.TrimSpace(override)
	if override != "" && containerNameRe.MatchString(override) {
		return override
	}
	return composeServiceContainerName(appName, service)
}

// resolveComposeFile returns the compose file path actually used
// at deploy time: user-provided compose_path wins; otherwise the
// generated path. The project name is the standard
// "nanoku-<app>" prefix.
func (h *Handlers) resolveComposeFile(a *db.App) (project, filePath string) {
	project = composeProjectName(a.Name)
	if a.ComposePath != nil && *a.ComposePath != "" {
		return project, *a.ComposePath
	}
	return project, h.composeFilePath(a.Name)
}

// writeComposeFile atomically writes the compose content to
// disk and returns the path used. Creates parent dirs as
// needed. The atomic-rename is important: a deploy worker
// reading the file in the middle of a write would otherwise
// see a half-written YAML and fail to parse.
//
// Caller is responsible for the file's lifetime (deploys write
// it; app delete removes it). The function does NOT remove the
// file on error — the caller decides whether to keep the
// partial state for debugging.
func (h *Handlers) writeComposeFile(appName, content string) (string, error) {
	path := h.composeFilePath(appName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".docker-compose-*.yml.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("rename: %w", err)
	}
	return path, nil
}

// appImage safely dereferences a's image pointer (nil → "").
// Used by deploy_executor and the DTO converter. Kept here
// alongside the other compose helpers because it has the same
// shape (a tiny nil-safe accessor on the App struct).
func appImage(a *db.App) string {
	if a.Image == nil {
		return ""
	}
	return *a.Image
}

// appPort returns the app's port (0 for compose-mode apps
// without one). Same nil-safe pattern as appImage.
func appPort(a *db.App) int {
	return a.Port
}

// loadMounts returns the app's volume rows as docker.VolumeMounts
// in stored order. Used by DeployApp / trigger_deploy. The
// indirection through this function (rather than a direct
// inline slice-build) makes it easy to unit-test the mount
// shape without standing up a Handlers.
func loadMounts(ctx context.Context, a *db.App) ([]docker.VolumeMount, error) {
	vols, err := a.QueryVolumes().Order(volume.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]docker.VolumeMount, 0, len(vols))
	for _, v := range vols {
		src := ""
		if v.Source != nil {
			src = *v.Source
		}
		out = append(out, docker.VolumeMount{
			Type:     string(v.Type),
			Source:   src,
			Target:   v.Target,
			ReadOnly: v.ReadOnly,
		})
	}
	return out, nil
}
