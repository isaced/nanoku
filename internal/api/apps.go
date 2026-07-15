package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/container"
	"github.com/isaced/nanoku/internal/db/deploy"
	"github.com/isaced/nanoku/internal/db/envvar"
	"github.com/isaced/nanoku/internal/db/volume"
	"github.com/isaced/nanoku/internal/docker"
)

var appNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

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
	// Resolved path on the host where the compose file actually lives
	// (for UI display + cleanup). Equal to ComposePath if user-provided,
	// otherwise the generated path under ComposeBaseDir.
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

type ContainerDTO struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt,omitempty"`
	StoppedAt string `json:"stoppedAt,omitempty"`
}

type EnvVarDTO struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type EnvVarInput struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

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
	// DeleteVolumesOnRemove controls whether DeleteApp also wipes the
	// app's auto-named nanoku volumes. Bind mounts and user-named
	// volumes are never touched regardless.
	DeleteVolumesOnRemove *bool `json:"deleteVolumesOnRemove"`

	// ExposedPorts is the new value for the app's exposed_ports column.
	// Nil = leave the existing value alone. Empty array (or a JSON `[]`)
	// explicitly clears the field. Compose-mode apps use this to declare
	// which services Caddy should be able to reverse-proxy to.
	ExposedPorts *[]ExposedPort `json:"exposedPorts"`
}

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

// toAppDTO loads the current container for the app (1 query) and assembles DTO.
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

// composeFilePath returns the on-disk path where nanoku stores the
// docker-compose.yml for an app (when deploy_method=compose and
// compose_path is empty).
func (h *Handlers) composeFilePath(appName string) string {
	return filepath.Join(h.ComposeBaseDir, appName, "docker-compose.yml")
}

// loadMounts returns the app's volume rows as docker.VolumeMounts in
// stored order. Used by DeployApp / trigger_deploy.
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

// writeComposeFile atomically writes the compose content to disk and
// returns the path used. Creates parent dirs as needed.
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

// composeProjectName returns the compose project name nanoku uses for an app.
// Format: "nanoku-<app-name>". This controls network / container prefix.
func composeProjectName(appName string) string {
	return "nanoku-" + appName
}

// resolveComposeFile returns the compose file path actually used at deploy time:
// user-provided compose_path wins; otherwise the generated path.
func (h *Handlers) resolveComposeFile(a *db.App) (project, filePath string) {
	project = composeProjectName(a.Name)
	if a.ComposePath != nil && *a.ComposePath != "" {
		return project, *a.ComposePath
	}
	return project, h.composeFilePath(a.Name)
}

// appImage safely dereferences a's image pointer (nil → "").
func appImage(a *db.App) string {
	if a.Image == nil {
		return ""
	}
	return *a.Image
}

// appPort returns the app's port (0 for compose-mode apps without one).
func appPort(a *db.App) int {
	return a.Port
}

func (h *Handlers) ListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := h.DB.App.Query().Order(app.ByName()).All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]AppDTO, 0, len(apps))
	for _, a := range apps {
		out = append(out, h.toAppDTO(r.Context(), a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) CreateApp(w http.ResponseWriter, r *http.Request) {
	var in AppInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Name == nil || !appNameRe.MatchString(*in.Name) {
		writeErr(w, http.StatusBadRequest, errors.New("name must match ^[a-z][a-z0-9-]{0,62}$"))
		return
	}
	deployMethod := "docker"
	if in.DeployMethod != nil {
		switch *in.DeployMethod {
		case "docker", "compose":
			deployMethod = *in.DeployMethod
		default:
			writeErr(w, http.StatusBadRequest, errors.New("deployMethod must be 'docker' or 'compose'"))
			return
		}
	}
	if deployMethod == "docker" {
		if in.Image == nil || strings.TrimSpace(*in.Image) == "" {
			writeErr(w, http.StatusBadRequest, errors.New("image is required for docker mode"))
			return
		}
		if in.Port == nil || *in.Port < 1 || *in.Port > 65535 {
			writeErr(w, http.StatusBadRequest, errors.New("port must be 1..65535"))
			return
		}
	} else {
		hasPath := in.ComposePath != nil && strings.TrimSpace(*in.ComposePath) != ""
		hasContent := in.ComposeContent != nil && strings.TrimSpace(*in.ComposeContent) != ""
		if !hasPath && !hasContent {
			writeErr(w, http.StatusBadRequest, errors.New("compose mode requires composePath or composeContent"))
			return
		}
	}
	create := h.DB.App.Create().
		SetName(*in.Name).
		SetDeployMethod(deployMethod)
	if deployMethod == "docker" {
		create.SetImage(strings.TrimSpace(*in.Image))
		if in.Port != nil {
			create.SetPort(*in.Port)
		}
	}
	// compose fields
	if in.ComposePath != nil && strings.TrimSpace(*in.ComposePath) != "" {
		create.SetComposePath(strings.TrimSpace(*in.ComposePath))
	}
	if in.ComposeContent != nil && strings.TrimSpace(*in.ComposeContent) != "" {
		// written to disk after Save so we have an ID
		create.SetComposeContent(strings.TrimSpace(*in.ComposeContent))
	}
	// ExposedPorts is only meaningful for compose-mode apps; docker-mode
	// apps always proxy through App.port. Validate + marshal now so a bad
	// payload returns 400 before we touch the DB. We accept the field on
	// docker-mode requests too (rather than silently drop) so a caller
	// editing an app's deploy method in the same PATCH doesn't have to
	// remember to clear the field — we ignore the value when mode=docker.
	if in.ExposedPorts != nil {
		ep, err := MarshalExposedPorts(*in.ExposedPorts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if ep != nil {
			create.SetExposedPorts(*ep)
		}
	}
	// registry
	if in.RegistryURL != nil && strings.TrimSpace(*in.RegistryURL) != "" {
		create.SetRegistryURL(strings.TrimSpace(*in.RegistryURL))
	}
	if in.RegistryUsername != nil && strings.TrimSpace(*in.RegistryUsername) != "" {
		create.SetRegistryUsername(strings.TrimSpace(*in.RegistryUsername))
	}
	if in.RegistryPassword != nil && *in.RegistryPassword != "" {
		enc, _, err := h.Secret.EncryptOptional(*in.RegistryPassword)
		if err != nil {
			writeInternalErr(w, fmt.Errorf("encrypt registry password: %w", err))
			return
		}
		if enc != nil {
			create.SetRegistryPassword(*enc)
		}
	}

	// HTTP trigger: when enabled on create, generate a fresh bearer token
	// server-side and return it in the create response (single-shot).
	// On update, EnableTrigger is a no-op for already-enabled apps.
	var generatedToken string
	if in.EnableTrigger != nil && *in.EnableTrigger {
		tok, err := randomToken()
		if err != nil {
			writeInternalErr(w, fmt.Errorf("generate trigger token: %w", err))
			return
		}
		encTok, err := h.Secret.EncryptString(tok)
		if err != nil {
			writeInternalErr(w, fmt.Errorf("encrypt trigger token: %w", err))
			return
		}
		create.SetTriggerToken(encTok)
		generatedToken = tok
	}
	if in.DeleteVolumesOnRemove != nil {
		create.SetDeleteVolumesOnRemove(*in.DeleteVolumesOnRemove)
	}

	a, err := create.Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Persist the compose file to disk right away so the file path returned
	// in the DTO is always valid (not just after a deploy).
	if deployMethod == "compose" && (in.ComposePath == nil || strings.TrimSpace(*in.ComposePath) == "") && in.ComposeContent != nil && strings.TrimSpace(*in.ComposeContent) != "" {
		if _, werr := h.writeComposeFile(a.Name, strings.TrimSpace(*in.ComposeContent)); werr != nil {
			// non-fatal: deploy will retry
			_ = werr
		}
	}
	dto := h.toAppDTO(r.Context(), a)
	dto.TriggerToken = generatedToken // single-shot: only on this create response
	writeJSON(w, http.StatusCreated, dto)
}

func (h *Handlers) GetApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	dto := h.toAppDTO(r.Context(), a)
	env, err := a.QueryEnvVars().All(r.Context())
	if err == nil {
		dto.EnvVars = make([]EnvVarDTO, 0, len(env))
		for _, e := range env {
			val, derr := h.Secret.DecryptString(e.Value)
			if derr != nil {
				writeInternalErr(w, fmt.Errorf("decrypt env var %s: %w", e.Key, derr))
				return
			}
			dto.EnvVars = append(dto.EnvVars, EnvVarDTO{Key: e.Key, Value: val})
		}
	}
	writeJSON(w, http.StatusOK, dto)
}

func (h *Handlers) UpdateApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	var in AppInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Name != nil && !appNameRe.MatchString(*in.Name) {
		writeErr(w, http.StatusBadRequest, errors.New("name must match ^[a-z][a-z0-9-]{0,62}$"))
		return
	}
	if in.Port != nil && (*in.Port < 1 || *in.Port > 65535) {
		writeErr(w, http.StatusBadRequest, errors.New("port must be 1..65535"))
		return
	}
	upd := h.DB.App.UpdateOneID(id)
	if in.Name != nil {
		upd.SetName(*in.Name)
	}
	if in.Image != nil {
		upd.SetImage(strings.TrimSpace(*in.Image))
	}
	if in.Port != nil {
		upd.SetPort(*in.Port)
	}
	if in.DeployMethod != nil {
		switch *in.DeployMethod {
		case "docker", "compose":
			upd.SetDeployMethod(*in.DeployMethod)
		default:
			writeErr(w, http.StatusBadRequest, errors.New("deployMethod must be 'docker' or 'compose'"))
			return
		}
	}
	if in.ComposePath != nil {
		p := strings.TrimSpace(*in.ComposePath)
		if p == "" {
			upd.ClearComposePath()
		} else {
			upd.SetComposePath(p)
		}
	}
	if in.ComposeContent != nil {
		c := strings.TrimSpace(*in.ComposeContent)
		if c == "" {
			upd.ClearComposeContent()
		} else {
			upd.SetComposeContent(c)
		}
	}
	// ExposedPorts update: nil = leave alone (the only nil case),
	// empty array = clear, non-empty = replace. Only meaningful for
	// compose-mode apps; we apply it on update regardless so users can
	// switch an app from docker→compose with a single PATCH.
	if in.ExposedPorts != nil {
		ep, err := MarshalExposedPorts(*in.ExposedPorts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if ep == nil {
			upd.ClearExposedPorts()
		} else {
			upd.SetExposedPorts(*ep)
		}
	}
	if in.RegistryURL != nil {
		u := strings.TrimSpace(*in.RegistryURL)
		if u == "" {
			upd.ClearRegistryURL()
		} else {
			upd.SetRegistryURL(u)
		}
	}
	if in.RegistryUsername != nil {
		u := strings.TrimSpace(*in.RegistryUsername)
		if u == "" {
			upd.ClearRegistryUsername()
		} else {
			upd.SetRegistryUsername(u)
		}
	}
	if in.ClearRegistry != nil && *in.ClearRegistry {
		upd.ClearRegistryPassword()
		upd.ClearRegistryUsername()
		upd.ClearRegistryURL()
	} else if in.RegistryPassword != nil && *in.RegistryPassword != "" {
		enc, _, err := h.Secret.EncryptOptional(*in.RegistryPassword)
		if err != nil {
			writeInternalErr(w, fmt.Errorf("encrypt registry password: %w", err))
			return
		}
		if enc != nil {
			upd.SetRegistryPassword(*enc)
		}
	}
	if in.DeleteVolumesOnRemove != nil {
		upd.SetDeleteVolumesOnRemove(*in.DeleteVolumesOnRemove)
	}
	// HTTP trigger:
	//   EnableTrigger=nil  → leave current state alone
	//   EnableTrigger=true  → if not yet enabled, mint a new bearer token
	//   EnableTrigger=false → clear the token; trigger endpoint returns 404
	if in.EnableTrigger != nil {
		if *in.EnableTrigger {
			if a.TriggerToken == nil || *a.TriggerToken == "" {
				tok, err := randomToken()
				if err != nil {
					writeInternalErr(w, fmt.Errorf("generate trigger token: %w", err))
					return
				}
				encTok, err := h.Secret.EncryptString(tok)
				if err != nil {
					writeInternalErr(w, fmt.Errorf("encrypt trigger token: %w", err))
					return
				}
				upd.SetTriggerToken(encTok)
			}
		} else {
			upd.ClearTriggerToken()
		}
	}
	a, err = upd.Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Sync the on-disk compose file with the latest content.
	if a.DeployMethod == "compose" && (a.ComposePath == nil || *a.ComposePath == "") && a.ComposeContent != nil && *a.ComposeContent != "" {
		if _, werr := h.writeComposeFile(a.Name, *a.ComposeContent); werr != nil {
			_ = werr
		}
	}
	// If exposed_ports or the docker→compose mode changed, the upstream
	// formula for every site linked to this app may have shifted. Refresh
	// them so the API list view reflects what the Caddyfile actually
	// serves. Best-effort: a refresh failure is not fatal here, since
	// the next deploy will reconcile anyway.
	if in.ExposedPorts != nil || in.DeployMethod != nil {
		if rerr := h.RefreshSitesForApp(r.Context(), a.ID); rerr != nil {
			// Don't fail the PATCH on a refresh error — the app row
			// itself is already saved. The next site update / deploy
			// will pick up the slack.
			_ = rerr
		}
	}
	writeJSON(w, http.StatusOK, h.toAppDTO(r.Context(), a))
}

func (h *Handlers) DeleteApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	if h.Docker != nil {
		if a.DeployMethod == "compose" {
			// Bring the whole stack down (compose down removes containers,
			// default network, and the nanoku- prefix).
			project, filePath := h.resolveComposeFile(a)
			_ = h.Docker.ComposeDown(r.Context(), project, filePath)
		}
		if cur, err := a.QueryCurrentContainer().Only(r.Context()); err == nil && cur != nil {
			if err := h.Docker.RemoveContainer(r.Context(), cur.Name); err != nil {
				writeInternalErr(w, fmt.Errorf("remove container: %w", err))
				return
			}
		}
		// also remove any non-current containers (historical)
		conts, _ := h.DB.Container.Query().Where(container.HasAppWith(app.IDEQ(a.ID))).All(r.Context())
		for _, c := range conts {
			_ = h.Docker.RemoveContainer(r.Context(), c.Name)
		}
		// If the user opted in, also wipe auto-named nanoku volumes for
		// this app. Bind mounts and user-named volumes are not touched.
		if a.DeleteVolumesOnRemove {
			_ = h.Docker.RemoveAppVolumes(r.Context(), a.Name)
		}
		// Clean up generated compose file on disk (best-effort).
		if a.DeployMethod == "compose" && (a.ComposePath == nil || *a.ComposePath == "") {
			_ = os.Remove(h.composeFilePath(a.Name))
		}
	}
	if err := h.DB.App.DeleteOneID(id).Exec(r.Context()); err != nil {
		writeInternalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ImportExposedPorts pre-fills the exposed_ports list for a
// compose-mode app by parsing the app's compose YAML. The handler
// returns a list of {name, port} scaffolding the operator can
// review / edit in the UI before the value lands in the database —
// we never write to App.exposed_ports here. The port for each
// service is best-effort (expose[0] > ports[0] container-half >
// 0); a service with no detectable port comes back with port=0
// and the UI renders an empty input so the operator can fill it
// in.
//
// This is compose-only: docker-mode apps have no services to
// import, and the caller would have nothing to put in exposed_ports
// anyway (docker apps use App.port). Refuse the request up front
// rather than returning an empty list, so the UI can show a clear
// error instead of silently populating zero rows.
func (h *Handlers) ImportExposedPorts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	if a.DeployMethod != "compose" {
		writeErr(w, http.StatusBadRequest, errors.New("import is only available for compose-mode apps"))
		return
	}

	// Pick the compose source the same way deploy does:
	// user-provided path wins, otherwise the inline content.
	// Empty content (path set but unreadable, or neither set) is
	// a 400 with an actionable hint — the operator should either
	// add compose_content or check compose_path.
	var content string
	switch {
	case a.ComposePath != nil && *a.ComposePath != "":
		writeErr(w, http.StatusBadRequest, fmt.Errorf("compose_path is set (%s); import only reads inline compose content — paste the YAML into compose_content or open the file manually", *a.ComposePath))
		return
	case a.ComposeContent != nil:
		content = *a.ComposeContent
	}
	if strings.TrimSpace(content) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("app has no compose content to import from"))
		return
	}

	ports, err := ImportExposedPortsFromCompose(content)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	hint := "review and edit before saving"
	if len(ports) == 0 {
		hint = "no services found in compose content"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"appId":        a.ID,
		"exposedPorts": ports,
		"hint":         hint,
	})
}

func (h *Handlers) ListAppEnvVars(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if _, err := h.DB.App.Get(r.Context(), id); err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	env, err := h.DB.EnvVar.Query().Where(envvar.HasAppWith(app.IDEQ(id))).All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]EnvVarDTO, 0, len(env))
	for _, e := range env {
		val, derr := h.Secret.DecryptString(e.Value)
		if derr != nil {
			writeInternalErr(w, fmt.Errorf("decrypt env var %s: %w", e.Key, derr))
			return
		}
		out = append(out, EnvVarDTO{Key: e.Key, Value: val})
	}
	writeJSON(w, http.StatusOK, out)
}

// ReplaceAppEnvVars replaces the full env var set for an app. Requires re-deploy to take effect.
func (h *Handlers) ReplaceAppEnvVars(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	var in []EnvVarInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	seen := map[string]struct{}{}
	for i, kv := range in {
		k := strings.TrimSpace(kv.Key)
		if k == "" {
			writeErr(w, http.StatusBadRequest, errors.New("env var key cannot be empty"))
			return
		}
		if err := docker.ValidateEnvVar(k, kv.Value); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if _, dup := seen[k]; dup {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("duplicate key %q", k))
			return
		}
		seen[k] = struct{}{}
		in[i].Key = k
	}
	// wipe + recreate in a tx so partial failures don't leave junk
	tx, err := h.DB.Tx(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if _, err := tx.EnvVar.Delete().Where(envvar.HasAppWith(app.IDEQ(a.ID))).Exec(r.Context()); err != nil {
		_ = tx.Rollback()
		writeInternalErr(w, err)
		return
	}
	for _, kv := range in {
		encVal, err := h.Secret.EncryptString(kv.Value)
		if err != nil {
			_ = tx.Rollback()
			writeInternalErr(w, fmt.Errorf("encrypt env var %s: %w", kv.Key, err))
			return
		}
		if _, err := tx.EnvVar.Create().
			SetAppID(a.ID).
			SetKey(kv.Key).
			SetValue(encVal).
			Save(r.Context()); err != nil {
			_ = tx.Rollback()
			writeInternalErr(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(in),
		"hint":  "redeploy required for changes to take effect",
		"appId": a.ID,
	})
}

func (h *Handlers) ListAppDeploys(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	deps, err := h.DB.Deploy.Query().
		Where(deploy.HasAppWith(app.IDEQ(id))).
		WithContainer().
		Order(deploy.ByCreatedAt(sql.OrderDesc())).
		Limit(50).
		All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]DeployDTO, 0, len(deps))
	for _, d := range deps {
		out = append(out, toDeployDTO(d))
	}
	writeJSON(w, http.StatusOK, out)
}
