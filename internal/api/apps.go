package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
)

var appNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type AppDTO struct {
	ID        int           `json:"id"`
	Name      string        `json:"name"`
	Image     string        `json:"image"`
	Port      int           `json:"port"`
	RepoURL   string        `json:"repoUrl,omitempty"`
	Branch    string        `json:"branch"`
	Container *ContainerDTO `json:"container,omitempty"`
	EnvVars   []EnvVarDTO   `json:"envVars,omitempty"`
	CreatedAt string        `json:"createdAt"`
	UpdatedAt string        `json:"updatedAt"`
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
	Name    *string `json:"name"`
	Image   *string `json:"image"`
	Port    *int    `json:"port"`
	RepoURL *string `json:"repoUrl"`
	Branch  *string `json:"branch"`
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
		Image:     a.Image,
		Port:      a.Port,
		Branch:    a.Branch,
		CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: a.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if a.RepoURL != nil {
		out.RepoURL = *a.RepoURL
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
	if in.Image == nil || strings.TrimSpace(*in.Image) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("image is required"))
		return
	}
	if in.Port == nil || *in.Port < 1 || *in.Port > 65535 {
		writeErr(w, http.StatusBadRequest, errors.New("port must be 1..65535"))
		return
	}
	branch := "main"
	if in.Branch != nil && strings.TrimSpace(*in.Branch) != "" {
		branch = strings.TrimSpace(*in.Branch)
	}
	create := h.DB.App.Create().
		SetName(*in.Name).
		SetImage(strings.TrimSpace(*in.Image)).
		SetPort(*in.Port).
		SetBranch(branch)
	if in.RepoURL != nil && strings.TrimSpace(*in.RepoURL) != "" {
		create.SetRepoURL(strings.TrimSpace(*in.RepoURL))
	}
	a, err := create.Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.toAppDTO(r.Context(), a))
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
	if in.Branch != nil {
		upd.SetBranch(strings.TrimSpace(*in.Branch))
	}
	if in.RepoURL != nil {
		repo := strings.TrimSpace(*in.RepoURL)
		if repo == "" {
			upd.ClearRepoURL()
		} else {
			upd.SetRepoURL(repo)
		}
	}
	a, err := upd.Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
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

	if err := h.Docker.PullImage(r.Context(), a.Image); err != nil {
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeErr(w, http.StatusBadGateway, fmt.Errorf("pull image: %w", err))
		return
	}

	// stop & remove old current container (if any) so port mapping / state stays clean
	if cur, err := a.QueryCurrentContainer().Only(r.Context()); err == nil && cur != nil {
		_ = h.Docker.StopContainer(r.Context(), cur.Name)
		_ = h.Docker.RemoveContainer(r.Context(), cur.Name)
		_ = h.DB.Container.DeleteOneID(cur.ID).Exec(r.Context())
	}

	dockerID, containerName, err := h.Docker.CreateAppContainer(r.Context(), a.Name, a.Image, a.Port, envKVs, 0)
	if err != nil {
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	now := time.Now().UTC()
	cont, err := h.DB.Container.Create().
		SetDockerID(dockerID).
		SetName(containerName).
		SetImage(a.Image).
		SetStatus("running").
		SetStartedAt(now).
		SetAppID(a.ID).
		SetDeployID(dep.ID).
		Save(r.Context())
	if err != nil {
		_ = h.Docker.RemoveContainer(r.Context(), containerName)
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(cont.ID).Exec(r.Context()); err != nil {
		_ = h.Docker.RemoveContainer(r.Context(), containerName)
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = h.DB.Deploy.UpdateOneID(dep.ID).SetStatus("success").SetFinishedAt(time.Now().UTC()).Save(r.Context())

	// new container name → any site bound to this app now points to a stale upstream.
	// Regenerate the Caddyfile so it picks up the freshly rotated container name.
	if err := h.regenerateAndReload(r); err != nil {
		// don't fail the deploy — the new container is already running; user can manually re-trigger.
		h.markDeployFailed(r.Context(), dep.ID, fmt.Errorf("post-deploy caddy regen: %w", err))
	}

	fresh, _ := h.DB.App.Get(r.Context(), a.ID)
	writeJSON(w, http.StatusCreated, h.toAppDTO(r.Context(), fresh))
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
	cur, err := a.QueryCurrentContainer().Only(r.Context())
	if err != nil || cur == nil {
		writeErr(w, http.StatusConflict, errors.New("no deployed container for this app"))
		return
	}
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