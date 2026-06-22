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
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/container"
	"github.com/isaced/nanoku/internal/db/deploy"
	"github.com/isaced/nanoku/internal/db/envvar"
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
	// EnableTrigger generates a per-app bearer token on the server. The
	// token is returned exactly once in the create response. Idempotent
	// on update: an already-set token is left alone.
	EnableTrigger *bool `json:"enableTrigger"`
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
		ID:        a.ID,
		Name:      a.Name,
		Image:     appImage(a),
		Port:      appPort(a),
		CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: a.UpdatedAt.UTC().Format(time.RFC3339),
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
	return out
}

// composeFilePath returns the on-disk path where nanoku stores the
// docker-compose.yml for an app (when deploy_method=compose and
// compose_path is empty).
func (h *Handlers) composeFilePath(appName string) string {
	return filepath.Join(h.ComposeBaseDir, appName, "docker-compose.yml")
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
		writeErr(w, http.StatusInternalServerError, err)
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
	// registry
	if in.RegistryURL != nil && strings.TrimSpace(*in.RegistryURL) != "" {
		create.SetRegistryURL(strings.TrimSpace(*in.RegistryURL))
	}
	if in.RegistryUsername != nil && strings.TrimSpace(*in.RegistryUsername) != "" {
		create.SetRegistryUsername(strings.TrimSpace(*in.RegistryUsername))
	}
	if in.RegistryPassword != nil && *in.RegistryPassword != "" {
		create.SetRegistryPassword(*in.RegistryPassword)
	}

	// HTTP trigger: when enabled on create, generate a fresh bearer token
	// server-side and return it in the create response (single-shot).
	// On update, EnableTrigger is a no-op for already-enabled apps.
	var generatedToken string
	if in.EnableTrigger != nil && *in.EnableTrigger {
		tok, err := randomToken()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Errorf("generate trigger token: %w", err))
			return
		}
		create.SetTriggerToken(tok)
		generatedToken = tok
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
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	dto := h.toAppDTO(r.Context(), a)
	env, err := a.QueryEnvVars().All(r.Context())
	if err == nil {
		dto.EnvVars = make([]EnvVarDTO, 0, len(env))
		for _, e := range env {
			dto.EnvVars = append(dto.EnvVars, EnvVarDTO{Key: e.Key, Value: e.Value})
		}
	}
	writeJSON(w, http.StatusOK, dto)
}

func (h *Handlers) UpdateApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
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
		upd.SetRegistryPassword(*in.RegistryPassword)
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
	writeJSON(w, http.StatusOK, h.toAppDTO(r.Context(), a))
}

func (h *Handlers) DeleteApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
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
				writeErr(w, http.StatusInternalServerError, fmt.Errorf("remove container: %w", err))
				return
			}
		}
		// also remove any non-current containers (historical)
		conts, _ := h.DB.Container.Query().Where(container.HasAppWith(app.IDEQ(a.ID))).All(r.Context())
		for _, c := range conts {
			_ = h.Docker.RemoveContainer(r.Context(), c.Name)
		}
		// Clean up generated compose file on disk (best-effort).
		if a.DeployMethod == "compose" && (a.ComposePath == nil || *a.ComposePath == "") {
			_ = os.Remove(h.composeFilePath(a.Name))
		}
	}
	if err := h.DB.App.DeleteOneID(id).Exec(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) DeployApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if h.Docker == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("docker unavailable"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	// start deploy record
	dep, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus("running").
		SetStartedAt(time.Now().UTC()).
		Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	envVars, err := a.QueryEnvVars().All(r.Context())
	if err != nil {
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	envKVs := make([]string, 0, len(envVars))
	for _, e := range envVars {
		envKVs = append(envKVs, e.Key+"="+e.Value)
	}

	// Wipe the previous primary container row (if any) before redeploying,
	// so port mappings / state stay clean. For compose this is just a
	// pointer — the actual services are managed by `docker compose`.
	if cur, err := a.QueryCurrentContainer().Only(r.Context()); err == nil && cur != nil {
		_ = h.Docker.StopContainer(r.Context(), cur.Name)
		_ = h.Docker.RemoveContainer(r.Context(), cur.Name)
		_ = h.DB.Container.DeleteOneID(cur.ID).Exec(r.Context())
	}
	// For compose mode, also try to take down the prior stack (best-effort).
	if a.DeployMethod == "compose" {
		project, filePath := h.resolveComposeFile(a)
		_ = h.Docker.ComposeDown(r.Context(), project, filePath)
	}

	regURL := ""
	regUser := ""
	regPass := ""
	if a.RegistryURL != nil {
		regURL = *a.RegistryURL
	}
	if a.RegistryUsername != nil {
		regUser = *a.RegistryUsername
	}
	if a.RegistryPassword != nil {
		regPass = *a.RegistryPassword
	}

	runUp := func() error {
		if a.DeployMethod == "compose" {
			project, filePath := h.resolveComposeFile(a)
			// If the user provided inline content, write it to disk now
			// (covers both first deploy and re-deploys after edits).
			if (a.ComposePath == nil || *a.ComposePath == "") && a.ComposeContent != nil && *a.ComposeContent != "" {
				written, werr := h.writeComposeFile(a.Name, *a.ComposeContent)
				if werr != nil {
					return fmt.Errorf("write compose file: %w", werr)
				}
				filePath = written
			}
			return h.Docker.ComposeUp(r.Context(), project, filePath, true)
		}
		// docker mode
		img := appImage(a)
		if img == "" {
			return errors.New("image is required for docker mode")
		}
		if err := h.Docker.PullImage(r.Context(), img); err != nil {
			return fmt.Errorf("pull image: %w", err)
		}
		_, _, err := h.Docker.CreateAppContainer(r.Context(), a.Name, img, appPort(a), envKVs, 0)
		return err
	}

	if err := h.Docker.WithRegistry(r.Context(), regURL, regUser, regPass, runUp); err != nil {
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	// Resolve the primary container we just started and persist it.
	var (
		primaryName string
		primaryImg  string
	)
	if a.DeployMethod == "compose" {
		project, _ := h.resolveComposeFile(a)
		names, _ := h.Docker.ComposePSNames(r.Context(), project, "")
		primaryName = strings.Join(names, ",") // informational only when >1
		if len(names) > 0 {
			primaryName = names[0]
		} else {
			primaryName = composeProjectName(a.Name) + "-1"
		}
		primaryImg = appImage(a)
	} else {
		// docker mode: container name has the standard pattern
		primaryName = fmt.Sprintf("nanoku-%s-", a.Name) // partial; we'll resolve below
		_ = primaryName
		// Re-list to find the actual name (since we don't track the suffix ourselves)
		names := h.findNanokuAppContainers(r.Context(), a.Name)
		if len(names) > 0 {
			primaryName = names[0]
		} else {
			h.markDeployFailed(r.Context(), dep.ID, errors.New("container not found after deploy"))
			writeErr(w, http.StatusInternalServerError, errors.New("container not found after deploy"))
			return
		}
		primaryImg = appImage(a)
	}

	now := time.Now().UTC()
	cont, err := h.DB.Container.Create().
		SetDockerID("").
		SetName(primaryName).
		SetImage(primaryImg).
		SetStatus("running").
		SetStartedAt(now).
		SetAppID(a.ID).
		SetDeployID(dep.ID).
		Save(r.Context())
	if err != nil {
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(cont.ID).Exec(r.Context()); err != nil {
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = h.DB.Deploy.UpdateOneID(dep.ID).SetStatus("success").SetFinishedAt(time.Now().UTC()).Save(r.Context())

	// Container name (or set of names) may have changed — regenerate the
	// Caddyfile so it points at the freshly rotated upstream.
	if err := h.regenerateAndReload(r); err != nil {
		h.markDeployFailed(r.Context(), dep.ID, fmt.Errorf("post-deploy caddy regen: %w", err))
	}

	fresh, _ := h.DB.App.Get(r.Context(), a.ID)
	writeJSON(w, http.StatusCreated, h.toAppDTO(r.Context(), fresh))
}

// findNanokuAppContainers returns the names of running containers whose
// name starts with "nanoku-<appName>-". Used to discover the post-deploy
// container name when we generated a random suffix.
func (h *Handlers) findNanokuAppContainers(ctx context.Context, appName string) []string {
	if h.Docker == nil {
		return nil
	}
	names, _ := h.Docker.ListContainersByNamePrefix(ctx, "nanoku-"+appName+"-")
	return names
}

func (h *Handlers) markDeployFailed(ctx context.Context, depID int, cause error) {
	msg := cause.Error()
	_, _ = h.DB.Deploy.UpdateOneID(depID).
		SetStatus("failed").
		SetError(msg).
		SetFinishedAt(time.Now().UTC()).
		Save(ctx)
}

func (h *Handlers) StartApp(w http.ResponseWriter, r *http.Request) {
	h.containerAction(w, r, "start")
}

func (h *Handlers) StopApp(w http.ResponseWriter, r *http.Request) {
	h.containerAction(w, r, "stop")
}

func (h *Handlers) RestartApp(w http.ResponseWriter, r *http.Request) {
	h.containerAction(w, r, "restart")
}

func (h *Handlers) containerAction(w http.ResponseWriter, r *http.Request, action string) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if h.Docker == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("docker unavailable"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	cur, _ := a.QueryCurrentContainer().Only(r.Context())
	if cur == nil {
		writeErr(w, http.StatusConflict, errors.New("no deployed container for this app"))
		return
	}

	// Compose mode: drive the whole stack via `docker compose ...`.
	if a.DeployMethod == "compose" {
		project, filePath := h.resolveComposeFile(a)
		var actErr error
		switch action {
		case "start":
			actErr = h.Docker.ComposeStart(r.Context(), project, filePath)
		case "stop":
			actErr = h.Docker.ComposeStop(r.Context(), project, filePath)
		case "restart":
			actErr = h.Docker.ComposeRestart(r.Context(), project, filePath)
		}
		if actErr != nil {
			writeErr(w, http.StatusBadGateway, actErr)
			return
		}
		now := time.Now().UTC()
		upd := h.DB.Container.UpdateOneID(cur.ID)
		switch action {
		case "start", "restart":
			upd.SetStatus("running").SetStartedAt(now).ClearStoppedAt()
		case "stop":
			upd.SetStatus("exited").SetStoppedAt(now)
		}
		_, _ = upd.Save(r.Context())
		fresh, _ := h.DB.App.Get(r.Context(), id)
		writeJSON(w, http.StatusOK, h.toAppDTO(r.Context(), fresh))
		return
	}

	// Docker mode: drive the single container.
	var actErr error
	switch action {
	case "start":
		actErr = h.Docker.StartContainer(r.Context(), cur.Name)
	case "stop":
		actErr = h.Docker.StopContainer(r.Context(), cur.Name)
	case "restart":
		actErr = h.Docker.RestartContainer(r.Context(), cur.Name)
	}
	if actErr != nil {
		writeErr(w, http.StatusBadGateway, actErr)
		return
	}
	now := time.Now().UTC()
	upd := h.DB.Container.UpdateOneID(cur.ID)
	switch action {
	case "start":
		upd.SetStatus("running").SetStartedAt(now).ClearStoppedAt()
	case "stop":
		upd.SetStatus("exited").SetStoppedAt(now)
	case "restart":
		upd.SetStatus("running").SetStartedAt(now).ClearStoppedAt()
	}
	_, _ = upd.Save(r.Context())

	fresh, _ := h.DB.App.Get(r.Context(), id)
	writeJSON(w, http.StatusOK, h.toAppDTO(r.Context(), fresh))
}

func (h *Handlers) AppLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if h.Docker == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("docker unavailable"))
		return
	}
	tail := 200
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := strconv.Atoi(t); err == nil {
			tail = n
		}
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	cur, err := a.QueryCurrentContainer().Only(r.Context())
	if err != nil || cur == nil {
		writeErr(w, http.StatusConflict, errors.New("no deployed container"))
		return
	}
	out, err := h.Docker.ContainerLogs(r.Context(), cur.Name, tail)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func (h *Handlers) ListAppEnvVars(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if _, err := h.DB.App.Get(r.Context(), id); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	env, err := h.DB.EnvVar.Query().Where(envvar.HasAppWith(app.IDEQ(id))).All(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]EnvVarDTO, 0, len(env))
	for _, e := range env {
		out = append(out, EnvVarDTO{Key: e.Key, Value: e.Value})
	}
	writeJSON(w, http.StatusOK, out)
}

// ReplaceAppEnvVars replaces the full env var set for an app. Requires re-deploy to take effect.
func (h *Handlers) ReplaceAppEnvVars(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
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
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := tx.EnvVar.Delete().Where(envvar.HasAppWith(app.IDEQ(a.ID))).Exec(r.Context()); err != nil {
		_ = tx.Rollback()
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, kv := range in {
		if _, err := tx.EnvVar.Create().
			SetAppID(a.ID).
			SetKey(kv.Key).
			SetValue(kv.Value).
			Save(r.Context()); err != nil {
			_ = tx.Rollback()
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":  len(in),
		"hint":   "redeploy required for changes to take effect",
		"appId":  a.ID,
	})
}

func (h *Handlers) ListAppDeploys(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
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
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]DeployDTO, 0, len(deps))
	for _, d := range deps {
		out = append(out, toDeployDTO(d))
	}
	writeJSON(w, http.StatusOK, out)
}

// (no helpers below)