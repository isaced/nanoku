// Package api hosts the HTTP layer. handlers.go is the home for
// the Handlers struct definition, the request/response utilities
// (pathID, writeJSON, writeErr), and the small handful of
// endpoints that don't fit into a domain-specific file (Status,
// Caddyfile preview). The Caddyfile regen-and-reload flow lives
// here too because it touches both the Caddy config and the
// live docker process — it's plumbing, not site/app logic.
//
// Domain-specific endpoints live in their own files: sites.go
// owns the Site table end to end (DTOs, handlers, upstream
// resolution), apps.go owns the App table (DTOs, CRUD, env
// vars, import helper), and so on.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/isaced/nanoku/internal/caddy"
	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	sitepkg "github.com/isaced/nanoku/internal/db/site"
	"github.com/isaced/nanoku/internal/docker"
	"github.com/isaced/nanoku/internal/secret"
)

type Handlers struct {
	DB              *db.DB
	Docker          *docker.Manager
	CaddyfilePath   string
	ACMEEmail       string
	SkipCaddyReload bool
	SelfContainer   string
	ComposeBaseDir  string
	DeployLock      *DeployLock
	DeployLogs      *deployLogHub
	Sessions        *SessionStore
	Secret          *secret.Sealer
	Version         string
	Commit          string
	Date            string
	BuildType       string
	TrustProxy      bool

	// ShutdownWG is incremented by executeDeploy on entry and
	// decremented on return, so main.go can Wait on it after
	// srv.Shutdown to drain in-flight deploy goroutines spawned
	// by DeployApp / Trigger / RollbackApp. Nil in unit tests
	// that don't exercise shutdown.
	ShutdownWG *sync.WaitGroup

	// inflightMu guards inflight. Populated by executeDeploy so
	// the shutdown path can mark stragglers failed when the
	// WaitGroup times out. Keys are Deploy rows; values are
	// their appID.
	inflightMu sync.Mutex
	inflight   map[int]int
}

type StatusResponse struct {
	DockerConnected  bool   `json:"dockerConnected"`
	CaddyStatus      string `json:"caddyStatus"`
	SiteCount        int    `json:"siteCount"`
	EnabledSiteCount int    `json:"enabledSiteCount"`
	AppCount         int    `json:"appCount"`
	RunningAppCount  int    `json:"runningAppCount"`
	ACMEEmail        string `json:"acmeEmail"`
}

func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	caddyStatus := "skipped"
	dockerConnected := false
	if !h.SkipCaddyReload && h.Docker != nil {
		if err := h.Docker.Ping(ctx); err == nil {
			dockerConnected = true
			if s, err := h.Docker.CaddyContainerStatus(ctx); err == nil {
				caddyStatus = s
			}
		} else {
			caddyStatus = "docker_unreachable"
		}
	}

	allSites, _ := h.DB.Site.Query().Count(ctx)
	enabled, _ := h.DB.Site.Query().Where(sitepkg.Enabled(true)).Count(ctx)
	appCount, _ := h.DB.App.Query().Count(ctx)
	runningAppCount, _ := h.DB.App.Query().Where(app.HasCurrentContainer()).Count(ctx)

	writeJSON(w, http.StatusOK, StatusResponse{
		DockerConnected:  dockerConnected,
		CaddyStatus:      caddyStatus,
		SiteCount:        allSites,
		EnabledSiteCount: enabled,
		AppCount:         appCount,
		RunningAppCount:  runningAppCount,
		ACMEEmail:        h.ACMEEmail,
	})
}

func (h *Handlers) CaddyfilePreview(w http.ResponseWriter, r *http.Request) {
	resolved, err := h.resolveSiteUpstreams(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(caddy.Render(resolved, h.ACMEEmail)))
}

// regenerateAndReload regenerates the Caddyfile from the current
// site state and asks the running Caddy container to pick it up.
// It's the single hook every site mutation goes through, so the
// generated Caddyfile is always coherent with the DB.
func (h *Handlers) regenerateAndReload(r *http.Request) error {
	return h.regenerateAndReloadCtx(r.Context())
}

// regenerateAndReloadCtx is the ctx-only form, safe to call from
// goroutines that don't have a *http.Request (e.g. the trigger
// deploy worker).
func (h *Handlers) regenerateAndReloadCtx(ctx context.Context) error {
	resolved, err := h.resolveSiteUpstreamsCtx(ctx)
	if err != nil {
		return err
	}
	content := caddy.Render(resolved, h.ACMEEmail)
	if err := caddy.WriteAtomic(h.CaddyfilePath, content); err != nil {
		return err
	}
	if h.SkipCaddyReload || h.Docker == nil {
		return nil
	}
	return h.Docker.ReloadCaddy(ctx)
}

// pathID parses the integer `{id}` path parameter from the
// request. It relies on the Go 1.22+ pattern router (the route
// must declare `{id}`), so it can't misparse a non-numeric
// segment like `/api/apps/abc/env` as id=0 the way the old
// string-prefix parser did.
func pathID(r *http.Request) (int, bool) {
	return pathIntID(r, "id")
}

// pathIntID parses an integer path parameter by name from the
// request.
func pathIntID(r *http.Request, name string) (int, bool) {
	v := r.PathValue(name)
	if v == "" {
		return 0, false
	}
	id, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
