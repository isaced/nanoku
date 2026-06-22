package api

import (
	"errors"
	"net/http"
	"strconv"
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

// SystemStatus reports availability of the two system log sources so the UI
// can show useful hints (e.g. "set NANOKU_SELF_CONTAINER").
type SystemStatus struct {
	NanokuContainerConfigured bool   `json:"nanokuContainerConfigured"`
	NanokuContainerName       string `json:"nanokuContainerName,omitempty"`
	CaddyContainer            string `json:"caddyContainer"`
	DockerAvailable           bool   `json:"dockerAvailable"`
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
	writeJSON(w, http.StatusOK, out)
}
