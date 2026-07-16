package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/docker"
)

// DeployApp is the manual UI deploy entry point. Like the HTTP trigger path,
// it now records the deploy as running and dispatches the actual work to a
// goroutine so a slow image pull doesn't hold the HTTP request open (or
// inherit the request's context, which would kill the pull on client
// disconnect). The response is 202 with the new deployId; the UI polls
// GET /api/apps/{id}/deployments for status.
//
// The handler does NOT pre-check h.Docker — the trigger path doesn't either,
// and short-circuiting on a missing docker manager would leave the manual
// path returning 503 instead of an async-failed Deploy row that the UI
// can poll. The executor reports docker-unavailable as a Deploy row status
// transition, the same shape CI users already see on trigger.
func (h *Handlers) DeployApp(w http.ResponseWriter, r *http.Request) {
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

	// Serialize deploys per app so an external trigger can't race a manual
	// redeploy (or vice versa) on the same container / caddyfile. Lock
	// release happens in the executor goroutine, not here — a busy lock
	// returns 202 accepted:false so the UI knows a deploy is in flight.
	if h.DeployLock == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("deploy lock not configured"))
		return
	}
	if !h.DeployLock.TryAcquire(a.ID) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": false,
			"reason":   "another deploy is already running for this app",
			"appId":    a.ID,
		})
		return
	}

	dep, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus("running").
		SetStartedAt(time.Now().UTC()).
		SetImage(appImage(a)).
		Save(r.Context())
	if err != nil {
		h.DeployLock.Release(a.ID)
		writeInternalErr(w, err)
		return
	}

	// Detached context: the goroutine must outlive the HTTP request,
	// otherwise a client disconnect would kill an in-flight pull.
	go h.executeDeploy(context.Background(), a.ID, dep.ID, "")

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"deployId": dep.ID,
		"appId":    a.ID,
	})
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
	containerName, status, msg := h.resolveLogContainer(r, a)
	if msg != "" {
		writeErr(w, status, errors.New(msg))
		return
	}
	out, err := h.Docker.ContainerLogs(r.Context(), containerName, tail)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// AppLogsStream is the SSE counterpart to AppLogs. The frontend uses
// GET /api/apps/{id}/logs for the initial history dump, then opens this
// stream to subscribe to new lines. The stream is open-ended (Follow:
// true on the engine) — it stays connected until the client disconnects
// or the container exits.
//
// We resolve the same {app → current container} lookup as AppLogs and
// bail with a synthetic SSE error event when there's no container
// (e.g. the app hasn't been deployed yet). The browser's EventSource
// will retry on the retry: 2000 directive we set, so once the app is
// deployed the same connection can be reopened.
func (h *Handlers) AppLogsStream(w http.ResponseWriter, r *http.Request) {
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
	containerName, status, msg := h.resolveLogContainer(r, a)
	if msg != "" {
		writeErr(w, status, errors.New(msg))
		return
	}
	stream, err := h.Docker.ContainerLogsStream(r.Context(), containerName, tail, true)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	sseHeaders(w)
	w.WriteHeader(http.StatusOK)
	streamToSSE(r.Context(), w, stream.Lines, stream.Err, stream.Cancel)
}

// resolveLogContainer determines which container's logs to stream. The
// ?container=<name> query param (used by the compose container-selector
// UI) takes precedence; when absent we fall back to the app's
// current_container so docker-mode apps and older callers keep working.
//
// Returns (name, 0, "") on success. On failure returns ("", status, msg)
// where status/msg are suitable for writeErr.
func (h *Handlers) resolveLogContainer(r *http.Request, a *db.App) (string, int, string) {
	if c := r.URL.Query().Get("container"); c != "" {
		return c, 0, ""
	}
	cur, err := a.QueryCurrentContainer().Only(r.Context())
	if err != nil || cur == nil {
		return "", http.StatusConflict, "no deployed container"
	}
	return cur.Name, 0, ""
}

// AppContainers lists the containers belonging to an app. For compose
// apps this enumerates every service in the stack (via the
// com.docker.compose.project label) so the Logs tab can offer a
// container selector. For docker-mode apps it returns the single
// current_container. The list reflects live Docker state, not the DB,
// so containers added/removed by `docker compose up/down` between
// deploys are accurately represented.
func (h *Handlers) AppContainers(w http.ResponseWriter, r *http.Request) {
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

	if a.DeployMethod == "compose" {
		project := composeProjectName(a.Name)
		containers, err := h.Docker.ListAppContainers(r.Context(), project)
		if err != nil {
			writeInternalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, containers)
		return
	}

	// docker-mode: return the single current container (or empty list
	// if not yet deployed). The frontend hides the selector when only
	// one container exists, so this just confirms deploy status.
	cur, err := a.QueryCurrentContainer().Only(r.Context())
	if err != nil || cur == nil {
		writeJSON(w, http.StatusOK, []docker.ContainerInfo{})
		return
	}
	status := string(cur.Status)
	if live, err := h.Docker.ContainerStatus(r.Context(), cur.Name); err == nil && live != "not_found" {
		status = live
	}
	writeJSON(w, http.StatusOK, []docker.ContainerInfo{{
		Name:   cur.Name,
		Image:  cur.Image,
		Status: status,
	}})
}