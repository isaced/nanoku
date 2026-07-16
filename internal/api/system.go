package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

const (
	systemSourceCaddy  = "caddy"
	systemSourceNanoku = "nanoku"
)

// SystemLogs returns the last `tail` lines of logs for a system container.
// source=caddy  -> the managed Caddy container
// source=nanoku -> nanoku's own container (SelfContainer)
func (h *Handlers) SystemLogs(w http.ResponseWriter, r *http.Request) {
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

	source := r.URL.Query().Get("source")
	var containerName string
	switch source {
	case systemSourceCaddy:
		containerName = h.Docker.CaddyContainerName()
	case systemSourceNanoku:
		containerName = h.SelfContainer
		if containerName == "" {
			writeErr(w, http.StatusServiceUnavailable, errors.New("NANOKU_SELF_CONTAINER not set"))
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, errors.New("source must be 'caddy' or 'nanoku'"))
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

// SystemLogsStream is the SSE counterpart to SystemLogs. Mirrors the
// source=caddy / source=nanoku dispatch; for the "no current container"
// case it returns a non-SSE error so the caller can render a hint
// instead of holding an open connection that never produces events.
func (h *Handlers) SystemLogsStream(w http.ResponseWriter, r *http.Request) {
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
	source := r.URL.Query().Get("source")
	var containerName string
	switch source {
	case systemSourceCaddy:
		containerName = h.Docker.CaddyContainerName()
	case systemSourceNanoku:
		containerName = h.SelfContainer
		if containerName == "" {
			writeErr(w, http.StatusServiceUnavailable, errors.New("NANOKU_SELF_CONTAINER not set"))
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, errors.New("source must be 'caddy' or 'nanoku'"))
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

// SystemStatus reports availability of the two system log sources so the UI
// can show useful hints (e.g. "set NANOKU_SELF_CONTAINER"). Build-time
// version metadata is also exposed for the Footer / update-check UI.
type SystemStatus struct {
	NanokuContainerConfigured bool   `json:"nanokuContainerConfigured"`
	NanokuContainerName       string `json:"nanokuContainerName,omitempty"`
	CaddyContainer            string `json:"caddyContainer"`
	DockerAvailable           bool   `json:"dockerAvailable"`
	Version                   string `json:"version"`
	Commit                    string `json:"commit"`
	Date                      string `json:"date"`
	BuildType                 string `json:"buildType"`
}

func (h *Handlers) SystemStatus(w http.ResponseWriter, r *http.Request) {
	out := SystemStatus{
		NanokuContainerConfigured: h.SelfContainer != "",
		NanokuContainerName:       h.SelfContainer,
	}
	if !h.SkipCaddyReload {
		out.DockerAvailable = h.Docker != nil
		if h.Docker != nil {
			out.CaddyContainer = h.Docker.CaddyContainerName()
		}
	}
	out.Version = h.Version
	out.Commit = h.Commit
	out.Date = h.Date
	out.BuildType = h.BuildType
	writeJSON(w, http.StatusOK, out)
}

// CleanupStatus is the JSON wire shape of GET /api/system/cleanup.
// The Janitor records per-task last-run + counts; this endpoint
// surfaces them so an operator (or the system page) can verify the
// background cleanup is alive and the most recent run did something
// useful.
type CleanupStatus struct {
	// Tasks is keyed by task name. Times are RFC3339; the
	// individual CleanupResult fields let callers tell the
	// difference between "ran but found nothing" and
	// "never ran".
	Tasks map[string]CleanupStatusEntry `json:"tasks"`
}

// CleanupStatusEntry is the per-task shape inside CleanupStatus.
// LastRun and NextRun are RFC3339; a zero LastRun means the task
// has not run yet (and LastErr / LastResult are their zero
// values).
type CleanupStatusEntry struct {
	LastRun    time.Time     `json:"lastRun"`
	NextRun    time.Time     `json:"nextRun"`
	LastResult CleanupResult `json:"lastResult"`
	LastErr    string        `json:"lastErr,omitempty"`
	LastErrAt  time.Time     `json:"lastErrAt,omitempty"`
	RunCount   int           `json:"runCount"`
	ErrCount   int           `json:"errCount"`
}

func (h *Handlers) SystemCleanup(w http.ResponseWriter, r *http.Request) {
	if h.Janitor == nil {
		writeJSON(w, http.StatusOK, CleanupStatus{Tasks: map[string]CleanupStatusEntry{}})
		return
	}
	raw := h.Janitor.Status()
	out := make(map[string]CleanupStatusEntry, len(raw))
	for name, s := range raw {
		var lastRun, nextRun, lastErrAt time.Time
		if !s.LastRun.IsZero() {
			lastRun = s.LastRun.UTC()
		}
		if !s.NextRun.IsZero() {
			nextRun = s.NextRun.UTC()
		}
		if !s.LastErrAt.IsZero() {
			lastErrAt = s.LastErrAt.UTC()
		}
		out[name] = CleanupStatusEntry{
			LastRun:    lastRun,
			NextRun:    nextRun,
			LastResult: s.LastResult,
			LastErr:    s.LastErr,
			LastErrAt:  lastErrAt,
			RunCount:   s.RunCount,
			ErrCount:   s.ErrCount,
		}
	}
	writeJSON(w, http.StatusOK, CleanupStatus{Tasks: out})
}
