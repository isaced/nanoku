package api

import (
	"context"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/container"
	"github.com/isaced/nanoku/internal/db/volume"
)

// app_dto.go collects the wire shapes the App / Container / EnvVar
// / Deploy handlers send over the JSON API, plus the converters
// that turn the loaded ent entities into those DTOs.
//
// The converters are pure (no I/O); the toAppDTO method is the
// one exception because it reaches out to the Docker manager to
// refresh a container's status and to the volumes edge for the
// app's volume list. Everything else is a straight copy.

// AppDTO is the wire shape of an app. Compose-mode apps populate
// ComposeContent / ComposePath / ExposedPorts; docker-mode apps
// leave those empty and rely on Image + Port. The wire shape is
// the same for both — the DeployMethod discriminator tells the
// UI which fields to render.
type AppDTO struct {
	ID        int           `json:"id"`
	Name      string        `json:"name"`
	Image     string        `json:"image"`
	Port      int           `json:"port"`
	Container *ContainerDTO `json:"container,omitempty"`
	EnvVars   []EnvVarDTO   `json:"envVars,omitempty"`
	Volumes   []VolumeDTO   `json:"volumes,omitempty"`

	// Deployment method: "docker" (default) or "compose".
	DeployMethod string `json:"deployMethod"`
	// Path to an existing compose file on the host. If set, overrides
	// ComposeContent.
	ComposePath string `json:"composePath,omitempty"`
	// Inline compose YAML (used when DeployMethod="compose" and ComposePath is empty).
	ComposeContent string `json:"composeContent,omitempty"`
	// Resolved path on the host where the compose file actually
	// lives (for UI display + cleanup). Equal to ComposePath if
	// user-provided, otherwise the generated path under
	// ComposeBaseDir.
	ComposeFile string `json:"composeFile,omitempty"`

	// Private registry credentials (password is never returned to the client).
	RegistryConfigured bool   `json:"registryConfigured"`
	RegistryURL        string `json:"registryUrl,omitempty"`
	RegistryUsername   string `json:"registryUsername,omitempty"`

	// HTTP-trigger config. TriggerToken is only populated on the
	// CreateApp / RotateTriggerToken responses, never on List/Get.
	// The token is the bearer credential for POST /api/apps/{name}/trigger;
	// the same value also implies a per-app token is set (used to drive
	// the `triggerConfigured` UI affordance).
	TriggerConfigured bool   `json:"triggerConfigured"`
	TriggerToken      string `json:"triggerToken,omitempty"`

	// If true, deleting this app also removes its auto-named nanoku
	// volumes (nanoku-<app>-vol-*). Bind mounts and user-named volumes
	// are never touched.
	DeleteVolumesOnRemove bool `json:"deleteVolumesOnRemove"`

	// Exposed services the app wants to make reachable via Sites. Only
	// meaningful for compose-mode apps. Each entry contributes one
	// upstream to the rendered Caddyfile. Always empty for docker-mode
	// apps (they expose via App.port).
	ExposedPorts []ExposedPort `json:"exposedPorts,omitempty"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// AppInput is the request body for CreateApp / UpdateApp. Pointer
// fields carry the "leave alone" semantics — only non-nil fields
// are applied. The Clear* and Enable* flags are exceptions
// because they need to express an explicit "false" intent that
// the pointer-of-true shape can't.
type AppInput struct {
	Name             *string `json:"name"`
	Image            *string `json:"image"`
	Port             *int    `json:"port"`
	DeployMethod     *string `json:"deployMethod"`
	ComposePath      *string `json:"composePath"`
	ComposeContent   *string `json:"composeContent"`
	RegistryURL      *string `json:"registryUrl"`
	RegistryUsername *string `json:"registryUsername"`
	RegistryPassword *string `json:"registryPassword"`
	// ClearRegistry wipes stored registry credentials.
	ClearRegistry *bool `json:"clearRegistry"`
	// EnableTrigger controls the HTTP trigger for the app.
	//   Create: only a value of true mints a bearer token (returned once).
	//   Update: nil leaves state alone; true mints a token if none exists;
	//           false clears the token (trigger endpoint returns 404).
	EnableTrigger *bool `json:"enableTrigger"`
	// DeleteVolumesOnRemove controls whether DeleteApp also wipes
	// the app's auto-named nanoku volumes. Bind mounts and
	// user-named volumes are never touched regardless.
	DeleteVolumesOnRemove *bool `json:"deleteVolumesOnRemove"`

	// ExposedPorts is the new value for the app's exposed_ports
	// column. Nil = leave the existing value alone. Empty array (or
	// a JSON `[]`) explicitly clears the field. Compose-mode apps
	// use this to declare which services Caddy should be able to
	// reverse-proxy to.
	ExposedPorts *[]ExposedPort `json:"exposedPorts"`
}

// ContainerDTO is the wire shape of a Container row, embedded
// inside AppDTO and surfaced in its own right by some endpoints.
type ContainerDTO struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt,omitempty"`
	StoppedAt string `json:"stoppedAt,omitempty"`
}

// EnvVarDTO is the wire shape of an EnvVar row. The value is
// decrypted server-side before serialization — the wire always
// carries plaintext for the operator's session. (The at-rest
// encryption protects the DB file, not the network.)
type EnvVarDTO struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// EnvVarInput is the request body for ReplaceAppEnvVars. Same
// shape as EnvVarDTO; the request also requires the caller to
// supply the full desired set (PUT semantics, not PATCH).
type EnvVarInput struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DeployDTO is the wire shape of a Deploy row. The fields are
// intentionally permissive (every timestamp / error is omitempty)
// because a half-finished deploy is a normal state — the API has
// to render it without faking values.
type DeployDTO struct {
	ID            int    `json:"id"`
	Trigger       string `json:"trigger"`
	Status        string `json:"status"`
	CommitSHA     string `json:"commitSha,omitempty"`
	CommitMessage string `json:"commitMessage,omitempty"`
	Error         string `json:"error,omitempty"`
	StartedAt     string `json:"startedAt,omitempty"`
	FinishedAt    string `json:"finishedAt,omitempty"`
	CreatedAt     string `json:"createdAt"`
	Container     string `json:"containerName,omitempty"`
}

// toContainerDTO renders a Container row. Pure — no I/O. The
// status is the value ent loaded; callers that need a live
// status from Docker should call ContainerStatus first and
// mutate the field on the entity before calling this.
func toContainerDTO(c *db.Container) ContainerDTO {
	out := ContainerDTO{
		ID:     c.ID,
		Name:   c.Name,
		Image:  c.Image,
		Status: string(c.Status),
	}
	if c.StartedAt != nil {
		out.StartedAt = c.StartedAt.UTC().Format(time.RFC3339)
	}
	if c.StoppedAt != nil {
		out.StoppedAt = c.StoppedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// toDeployDTO renders a Deploy row. Pure.
func toDeployDTO(d *db.Deploy) DeployDTO {
	out := DeployDTO{
		ID:        d.ID,
		Trigger:   string(d.Trigger),
		Status:    string(d.Status),
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
	}
	if d.CommitSha != nil {
		out.CommitSHA = *d.CommitSha
	}
	if d.CommitMessage != nil {
		out.CommitMessage = *d.CommitMessage
	}
	if d.Error != nil {
		out.Error = *d.Error
	}
	if d.StartedAt != nil {
		out.StartedAt = d.StartedAt.UTC().Format(time.RFC3339)
	}
	if d.FinishedAt != nil {
		out.FinishedAt = d.FinishedAt.UTC().Format(time.RFC3339)
	}
	if d.Edges.Container != nil {
		out.Container = d.Edges.Container.Name
	}
	return out
}

// toAppDTO loads the current container for the app (1 query),
// its volumes edge (1 query), and assembles the DTO. The
// docker-status refresh is best-effort and silent on error —
// the rest of the DTO still serializes with the ent-cached
// status, which is good enough for the list view.
func (h *Handlers) toAppDTO(ctx context.Context, a *db.App) AppDTO {
	out := AppDTO{
		ID:                    a.ID,
		Name:                  a.Name,
		Image:                 appImage(a),
		Port:                  appPort(a),
		DeleteVolumesOnRemove: a.DeleteVolumesOnRemove,
		CreatedAt:             a.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:             a.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if ports, err := ParseExposedPorts(a.ExposedPorts); err == nil && len(ports) > 0 {
		out.ExposedPorts = ports
	}
	out.DeployMethod = a.DeployMethod
	if a.DeployMethod == "compose" {
		if a.ComposePath != nil && *a.ComposePath != "" {
			out.ComposePath = *a.ComposePath
			out.ComposeFile = *a.ComposePath
		} else if a.ComposeContent != nil && *a.ComposeContent != "" {
			out.ComposeFile = h.composeFilePath(a.Name)
		}
		if a.ComposeContent != nil {
			out.ComposeContent = *a.ComposeContent
		}
	}
	if a.RegistryUsername != nil && *a.RegistryUsername != "" {
		out.RegistryConfigured = true
		out.RegistryUsername = *a.RegistryUsername
		if a.RegistryURL != nil {
			out.RegistryURL = *a.RegistryURL
		}
	}
	if appHasToken(a) {
		out.TriggerConfigured = true
	}
	if h.Docker != nil {
		cur, err := a.QueryCurrentContainer().Only(ctx)
		if err == nil && cur != nil {
			if status, err := h.Docker.ContainerStatus(ctx, cur.Name); err == nil && status != "not_found" {
				cur.Status = container.Status(status)
			}
			dto := toContainerDTO(cur)
			out.Container = &dto
		}
	}
	vols, verr := a.QueryVolumes().Order(volume.ByID()).All(ctx)
	if verr == nil {
		out.Volumes = make([]VolumeDTO, 0, len(vols))
		for _, v := range vols {
			out.Volumes = append(out.Volumes, toVolumeDTO(v))
		}
	}
	return out
}
