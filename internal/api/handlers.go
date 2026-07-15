package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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

	// ShutdownWG is incremented by executeDeploy on entry and decremented
	// on return, so main.go can Wait on it after srv.Shutdown to drain
	// in-flight deploy goroutines spawned by DeployApp / Trigger /
	// RollbackApp. Nil in unit tests that don't exercise shutdown.
	ShutdownWG *sync.WaitGroup

	// inflightMu guards inflight. Populated by executeDeploy so the
	// shutdown path can mark stragglers failed when the WaitGroup times
	// out. Keys are Deploy rows; values are their appID.
	inflightMu sync.Mutex
	inflight   map[int]int
}

type SiteDTO struct {
	ID        int    `json:"id"`
	Domain    string `json:"domain"`
	Upstream  string `json:"upstream"`
	Enabled   bool   `json:"enabled"`
	Scheme    string `json:"scheme"`
	AppID     *int   `json:"appId,omitempty"`
	AppName   string `json:"appName,omitempty"`
	// AppService is the service within a compose-mode app that this site
	// proxies to. Empty for docker-mode apps and for free-upstream sites.
	// When AppID is set and the app is compose, this drives upstream
	// resolution (see resolveSiteUpstreams).
	AppService string `json:"appService,omitempty"`
	// ExposedServices is the resolved list of ExposedPort on the
	// linked app (compose-mode only). Drives the service select in the
	// site editor — when AppService is unset, the UI uses this to decide
	// whether to require a service pick.
	ExposedServices []ExposedPort `json:"exposedServices,omitempty"`
	CreatedAt       string        `json:"createdAt"`
	UpdatedAt       string        `json:"updatedAt"`
}

type SiteInput struct {
	Domain    *string `json:"domain"`
	Upstream  *string `json:"upstream"`
	Enabled   *bool   `json:"enabled"`
	AppID     *int    `json:"appId"`
	AppService *string `json:"appService"`
	Scheme    *string `json:"scheme"`
	// ClearApp detaches the site from any app (and clears appService +
	// upstream). Use this instead of sending AppID=0 so the intent is
	// explicit and the handler doesn't have to guess the empty-FK
	// convention.
	ClearApp bool `json:"clearApp"`
}

// toDTO renders a site for the API. The AppService / ExposedServices fields
// reflect what's stored in the DB; Upstream is the *last-resolved* value
// (i.e. the value the Caddyfile currently uses) and is not recomputed here.
// Callers that want a fresh upstream should use resolveSiteUpstreams.
func toDTO(s *db.Site) SiteDTO {
	out := SiteDTO{
		ID:        s.ID,
		Domain:    s.Domain,
		Upstream:  s.Upstream,
		Enabled:   s.Enabled,
		Scheme:    string(s.Scheme),
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if s.Edges.App != nil {
		id := s.Edges.App.ID
		out.AppID = &id
		out.AppName = s.Edges.App.Name
		if s.AppService != nil {
			out.AppService = *s.AppService
		}
		if ports, err := ParseExposedPorts(s.Edges.App.ExposedPorts); err == nil {
			out.ExposedServices = ports
		}
	}
	return out
}

func (h *Handlers) ListSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.DB.Site.Query().WithApp(func(q *db.AppQuery) { q.WithCurrentContainer() }).Order(sitepkg.ByDomain()).All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]SiteDTO, 0, len(sites))
	for _, s := range sites {
		dto := toDTO(s)
		if s.Edges.App != nil {
			dto.AppName = s.Edges.App.Name
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) UpdateSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}

	var in SiteInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Load the existing site (with the app edge) to validate app/service
	// combinations and to know what the previous app_id was — the
	// upstream rewrite below depends on whether the app linkage actually
	// changed. The app FK lives on the edge, not as a column, so we
	// always need WithApp here.
	existing, err := h.DB.Site.Query().
		Where(sitepkg.IDEQ(id)).
		WithApp().
		Only(r.Context())
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("site not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	var existingAppID int
	if existing.Edges.App != nil {
		existingAppID = existing.Edges.App.ID
	}

	upd := h.DB.Site.UpdateOneID(id)
	if in.Domain != nil {
		if err := validateDomain(*in.Domain); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		upd.SetDomain(strings.TrimSpace(*in.Domain))
	}
	if in.Upstream != nil {
		upd.SetUpstream(strings.TrimSpace(*in.Upstream))
	}
	if in.Enabled != nil {
		upd.SetEnabled(*in.Enabled)
	}
	if in.Scheme != nil {
		switch sitepkg.Scheme(*in.Scheme) {
		case sitepkg.SchemeHTTP, sitepkg.SchemeHTTPS:
			upd.SetScheme(sitepkg.Scheme(*in.Scheme))
		default:
			writeErr(w, http.StatusBadRequest, errors.New("scheme must be http or https"))
			return
		}
	}

	// App linkage changes are split into three explicit branches so the
	// caller's intent is unambiguous: ClearApp > SetAppID(0) > SetAppID(N).
	// ClearApp=true unconditionally detaches the site (and clears
	// appService + upstream). Otherwise: 0 = detach, >0 = attach to that
	// app. The handler never silently treats AppID=0 as a no-op, so a
	// future bug that sends 0 by accident is observable as a 400-ish
	// detach, not a missed update.
	appChanged := false
	switch {
	case in.ClearApp:
		upd.ClearApp()
		upd.ClearAppService()
		upd.SetUpstream("")
		appChanged = true
	case in.AppID != nil:
		if *in.AppID == 0 {
			upd.ClearApp()
			upd.ClearAppService()
			upd.SetUpstream("")
			appChanged = true
		} else {
			upd.SetAppID(*in.AppID)
			appChanged = existingAppID != *in.AppID
		}
	}

	// AppService change handling. The validation below also re-checks
	// the service exists in the target app's exposed_ports (when the app
	// is compose-mode). For docker-mode apps we silently drop the value
	// rather than 400 — docker sites never use app_service and the UI
	// shouldn't have to know.
	if in.AppService != nil {
		targetAppID := in.AppID
		if targetAppID == nil && existingAppID != 0 {
			targetAppID = &existingAppID
		}
		if targetAppID != nil && *targetAppID != 0 {
			app, aerr := h.DB.App.Get(r.Context(), *targetAppID)
			if aerr != nil {
				writeInternalErr(w, aerr)
				return
			}
			if app.DeployMethod == "compose" {
				svc := strings.TrimSpace(*in.AppService)
				if svc == "" {
					upd.ClearAppService()
				} else {
					ports, perr := ParseExposedPorts(app.ExposedPorts)
					if perr != nil {
						writeErr(w, http.StatusBadRequest, perr)
						return
					}
					if findExposedPort(ports, svc) == nil {
						writeErr(w, http.StatusBadRequest, fmt.Errorf("appService %q not in app's exposed_ports", svc))
						return
					}
					upd.SetAppService(svc)
				}
			}
			// docker-mode: ignore the field. We don't ClearAppService
			// either (preserve whatever the caller might want to keep
			// for a future docker→compose switch).
		}
	}

	// Upstream rewrite. Whenever the app linkage OR appService changes,
	// recompute the upstream from the linked app — otherwise the stored
	// upstream string would point at the wrong container (or a deleted
	// service) after the update. The recomputation uses the
	// post-update state (new app, new service).
	if appChanged || in.AppService != nil {
		if err := h.rewriteSiteUpstream(r.Context(), upd, existing, existingAppID, in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}

	site, err := upd.Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if err := h.regenerateAndReload(r); err != nil {
		writeInternalErr(w, err)
		return
	}
	fresh, ferr := h.DB.Site.Query().WithApp(func(q *db.AppQuery) { q.WithCurrentContainer() }).Where(sitepkg.IDEQ(id)).Only(r.Context())
	dto := toDTO(site)
	if ferr == nil {
		dto = toDTO(fresh)
	}
	writeJSON(w, http.StatusOK, dto)
}

func (h *Handlers) CreateSite(w http.ResponseWriter, r *http.Request) {
	var in SiteInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Domain == nil || strings.TrimSpace(*in.Domain) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("domain is required"))
		return
	}
	if err := validateDomain(*in.Domain); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.AppID == nil && (in.Upstream == nil || strings.TrimSpace(*in.Upstream) == "") {
		writeErr(w, http.StatusBadRequest, errors.New("either upstream or appId is required"))
		return
	}

	// When the caller supplies an app, validate the service belongs to
	// that app's exposed_ports (compose-mode only). For docker-mode
	// apps we silently drop app_service — see UpdateSite for the same
	// reasoning. This is the create-side mirror of the update branch.
	var appService string
	if in.AppID != nil && *in.AppID != 0 {
		app, err := h.DB.App.Get(r.Context(), *in.AppID)
		if err != nil {
			if isNotFound(err) {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("app %d not found", *in.AppID))
				return
			}
			writeInternalErr(w, err)
			return
		}
		if app.DeployMethod == "compose" && in.AppService != nil {
			svc := strings.TrimSpace(*in.AppService)
			if svc != "" {
				ports, perr := ParseExposedPorts(app.ExposedPorts)
				if perr != nil {
					writeErr(w, http.StatusBadRequest, perr)
					return
				}
				if findExposedPort(ports, svc) == nil {
					writeErr(w, http.StatusBadRequest, fmt.Errorf("appService %q not in app's exposed_ports", svc))
					return
				}
				appService = svc
			}
		}
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	scheme := sitepkg.SchemeHTTPS
	if in.Scheme != nil {
		switch sitepkg.Scheme(*in.Scheme) {
		case sitepkg.SchemeHTTP, sitepkg.SchemeHTTPS:
			scheme = sitepkg.Scheme(*in.Scheme)
		default:
			writeErr(w, http.StatusBadRequest, errors.New("scheme must be http or https"))
			return
		}
	}

	// Upstream is computed from the app/service if linked; otherwise the
	// caller's free-form value is used. We refuse to create a site that
	// has neither a usable upstream nor a free-form fallback, since a
	// blank Caddyfile entry would either error or silently 502.
	upstream := ""
	if in.AppID != nil && *in.AppID != 0 {
		upstream = h.computeSiteUpstream(r.Context(), *in.AppID, appService)
	} else if in.Upstream != nil {
		upstream = strings.TrimSpace(*in.Upstream)
	}
	if upstream == "" {
		writeErr(w, http.StatusBadRequest, errors.New("could not determine upstream: provide appService (for compose apps), app.port (for docker apps), or a custom upstream"))
		return
	}

	create := h.DB.Site.Create().
		SetDomain(strings.TrimSpace(*in.Domain)).
		SetEnabled(enabled).
		SetScheme(scheme).
		SetUpstream(upstream)
	if in.AppID != nil && *in.AppID != 0 {
		create.SetAppID(*in.AppID)
		if appService != "" {
			create.SetAppService(appService)
		}
	}
	site, err := create.Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if err := h.regenerateAndReload(r); err != nil {
		_ = h.DB.Site.DeleteOneID(site.ID).Exec(r.Context())
		writeInternalErr(w, err)
		return
	}
	fresh, ferr := h.DB.Site.Query().WithApp(func(q *db.AppQuery) { q.WithCurrentContainer() }).Where(sitepkg.IDEQ(site.ID)).Only(r.Context())
	dto := toDTO(site)
	if ferr == nil {
		dto = toDTO(fresh)
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (h *Handlers) DeleteSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if err := h.DB.Site.DeleteOneID(id).Exec(r.Context()); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := h.regenerateAndReload(r); err != nil {
		writeInternalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) ToggleSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	site, err := h.DB.Site.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("site not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	updated, err := h.DB.Site.UpdateOneID(id).SetEnabled(!site.Enabled).Save(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if err := h.regenerateAndReload(r); err != nil {
		writeInternalErr(w, err)
		return
	}
	fresh, ferr := h.DB.Site.Query().WithApp(func(q *db.AppQuery) { q.WithCurrentContainer() }).Where(sitepkg.IDEQ(id)).Only(r.Context())
	dto := toDTO(updated)
	if ferr == nil {
		dto = toDTO(fresh)
	}
	writeJSON(w, http.StatusOK, dto)
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

// resolveSiteUpstreams returns site projections where upstream is resolved:
//   - docker-mode apps: `<current_container.name>:<app.port>`
//   - compose-mode apps with app_service set: `nanoku-<app>-<service>-1:<port>`
//     where port comes from the matching ExposedPort on the app. If the
//     service is not in exposed_ports, falls through to "free upstream"
//     (preserves whatever the user typed) so a stale service name doesn't
//     break the Caddyfile.
//   - compose-mode apps with no app_service and exactly one exposed_port:
//     same as above with the lone service auto-selected. Convenience for
//     single-service stacks.
//   - free upstream: stored value used as-is.
func (h *Handlers) resolveSiteUpstreams(r *http.Request) ([]*db.Site, error) {
	return h.resolveSiteUpstreamsCtx(r.Context())
}

func (h *Handlers) resolveSiteUpstreamsCtx(ctx context.Context) ([]*db.Site, error) {
	sites, err := h.DB.Site.Query().WithApp(func(q *db.AppQuery) { q.WithCurrentContainer() }).All(ctx)
	if err != nil {
		return nil, err
	}
	// Copy each site into a new value so mutating Upstream here doesn't
	// affect the ent-loaded entity in the caller's context. (ent's
	// generated structs are plain in-memory values — no surprise write
	// back to the DB — but mutating shared entities is still a footgun.)
	resolved := make([]*db.Site, 0, len(sites))
	for _, s := range sites {
		cp := *s
		if s.Edges.App != nil {
			cp.Upstream = computeUpstreamForApp(s.Edges.App, s.AppService)
		}
		resolved = append(resolved, &cp)
	}
	return resolved, nil
}

// computeUpstreamForApp returns the upstream string for a site linked to
// the given app, given an optional service name. Returns "" when the
// caller should fall back to whatever is stored in Site.Upstream (e.g. a
// free-upstream site, or a compose site whose service is not in
// exposed_ports).
//
// This is the single source of truth for the upstream string. Both
// resolveSiteUpstreamsCtx and rewriteSiteUpstream / CreateSite / UpdateSite
// use it so the Caddyfile always shows the same string as the API.
func computeUpstreamForApp(a *db.App, appService *string) string {
	if a == nil {
		return ""
	}
	if a.DeployMethod != "compose" {
		// docker-mode: keep the historical <container>:<port> shape.
		// We require both an app_id (callers enforce) and a current
		// container (the deploy path sets it after success). If the
		// current container is missing, fall back to the stored upstream
		// string so the Caddyfile doesn't get a half-baked entry.
		if a.Edges.CurrentContainer == nil {
			return ""
		}
		return a.Edges.CurrentContainer.Name + ":" + strconv.Itoa(a.Port)
	}
	// compose-mode. Resolve the service the caller wants; if not
	// specified and there is exactly one ExposedPort, auto-pick it
	// (single-service stacks are the common case).
	ports, err := ParseExposedPorts(a.ExposedPorts)
	if err != nil || len(ports) == 0 {
		return ""
	}
	var svc string
	if appService != nil {
		svc = strings.TrimSpace(*appService)
	}
	if svc == "" && len(ports) == 1 {
		svc = ports[0].Name
	}
	if svc == "" {
		return ""
	}
	ep := findExposedPort(ports, svc)
	if ep == nil {
		return ""
	}
	return composeServiceContainerName(a.Name, svc) + ":" + strconv.Itoa(ep.Port)
}

// composeServiceContainerName returns the docker-compose container name
// for a service within an app's stack. The -1 suffix is compose's default
// replica index; multi-replica stacks (v2) will need a different pick.
func composeServiceContainerName(appName, service string) string {
	return "nanoku-" + appName + "-" + service + "-1"
}

// computeSiteUpstream is a thin ctx-aware wrapper used by the create /
// update handlers that have a target app_id but no loaded App edge yet.
func (h *Handlers) computeSiteUpstream(ctx context.Context, appID int, appService string) string {
	a, err := h.DB.App.Query().
		Where(app.IDEQ(appID)).
		WithCurrentContainer().
		Only(ctx)
	if err != nil {
		return ""
	}
	var svcPtr *string
	if appService != "" {
		svcPtr = &appService
	}
	return computeUpstreamForApp(a, svcPtr)
}

// rewriteSiteUpstream computes a new upstream for the site being updated
// and queues a SetUpstream on the builder. Used by UpdateSite when the
// app linkage or app_service changes.
//
// The existing site is needed for the "free upstream" fallback: a
// compose-mode site whose new service isn't in exposed_ports should keep
// whatever the user typed rather than go blank. existingAppID is passed
// separately because the edge struct on existing doesn't expose the FK
// id in a queryable form for this code path.
func (h *Handlers) rewriteSiteUpstream(ctx context.Context, upd *db.SiteUpdateOne, existing *db.Site, existingAppID int, in SiteInput) error {
	// Resolve the target app id (after the update is applied, not before).
	var targetAppID int
	switch {
	case in.ClearApp:
		targetAppID = 0
	case in.AppID != nil:
		targetAppID = *in.AppID
	default:
		targetAppID = existingAppID
	}
	if targetAppID == 0 {
		// Detached sites keep whatever upstream the caller set (or
		// blank). The UpdateSite call site has already issued
		// SetUpstream("") for ClearApp / AppID=0.
		return nil
	}

	var svcPtr *string
	if in.AppService != nil {
		s := strings.TrimSpace(*in.AppService)
		if s != "" {
			svcPtr = &s
		}
	}
	if svcPtr == nil && existing.AppService != nil {
		s := strings.TrimSpace(*existing.AppService)
		if s != "" {
			svcPtr = &s
		}
	}

	newUpstream := h.computeSiteUpstream(ctx, targetAppID, svcStringOrEmpty(svcPtr))
	if newUpstream == "" {
		// Couldn't auto-resolve (service not in exposed_ports, or app
		// has no current_container yet). Keep the existing stored
		// value so the Caddyfile doesn't go blank; the next successful
		// deploy will refresh it via the post-deploy reconcile.
		return nil
	}
	upd.SetUpstream(newUpstream)
	return nil
}

func svcStringOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (h *Handlers) regenerateAndReload(r *http.Request) error {
	return h.regenerateAndReloadCtx(r.Context())
}

// regenerateAndReloadCtx is the ctx-only form, safe to call from goroutines
// that don't have a *http.Request (e.g. the trigger deploy worker).
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

// pathID parses the integer `{id}` path parameter from the request. It relies
// on the Go 1.22+ pattern router (the route must declare `{id}`), so it can't
// misparse a non-numeric segment like `/api/apps/abc/env` as id=0 the way the
// old string-prefix parser did.
func pathID(r *http.Request) (int, bool) {
	return pathIntID(r, "id")
}

// pathIntID parses an integer path parameter by name from the request.
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
