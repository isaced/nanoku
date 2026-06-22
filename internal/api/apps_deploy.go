package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *Handlers) DeployApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
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
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}

	// Serialize deploys per app. Manual UI deploys take the same lock as the
	// HTTP trigger path so an external trigger can't race a manual redeploy
	// (or vice versa) on the same container / caddyfile. Different apps still
	// deploy in parallel.
	if h.DeployLock == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("deploy lock not configured"))
		return
	}
	if !h.DeployLock.TryAcquire(a.ID) {
		writeErr(w, http.StatusConflict, errors.New("another deploy is already running for this app"))
		return
	}
	defer h.DeployLock.Release(a.ID)

	// start deploy record
	dep, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus("running").
		SetStartedAt(time.Now().UTC()).
		Save(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}

	envVars, err := a.QueryEnvVars().All(r.Context())
	if err != nil {
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeInternalErr(w, err)
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
		mounts, merr := loadMounts(r.Context(), a)
		if merr != nil {
			return fmt.Errorf("load mounts: %w", merr)
		}
		_, _, err := h.Docker.CreateAppContainer(r.Context(), a.Name, img, appPort(a), envKVs, 0, mounts)
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
		// docker mode: container name is deterministic — just nanoku-<appName>.
		primaryName = "nanoku-" + a.Name
		// Make sure the container actually exists before we record it; a
		// deploy that returned no container (e.g. docker run failed after
		// the call returned) would otherwise leave a dangling Container row.
		names, _ := h.Docker.ListContainersByNamePrefix(r.Context(), primaryName)
		found := false
		for _, n := range names {
			if n == primaryName {
				found = true
				break
			}
		}
		if !found {
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
		writeInternalErr(w, err)
		return
	}
	if err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(cont.ID).Exec(r.Context()); err != nil {
		h.markDeployFailed(r.Context(), dep.ID, err)
		writeInternalErr(w, err)
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
	id, ok := pathID(r)
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
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
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
	id, ok := pathID(r)
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
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
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
