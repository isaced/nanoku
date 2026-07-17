package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/isaced/nanoku/internal/caddy"
	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	sitepkg "github.com/isaced/nanoku/internal/db/site"
)

// sites.go owns everything to do with the Site table: DTOs, HTTP
// handlers, and the pure upstream-resolution function.
//
// The upstream string for an app-linked site is **derived** from
// (app, app_service) at render time — never stored on the row. The
// network alias (e.g. `nanoku-<app>-<service>`) is the stable
// routing identity, so the Caddyfile never needs to change when a
// container rotates, scales, or is recreated. For free-upstream
// sites (no app linked), the operator's literal string is the
// source of truth and IS stored on the row.
//
// Splitting the two cases here means there is no "reconciler"
// keeping a stored value in lockstep with derived state — the
// derived case literally has nothing to reconcile, because the
// derived value is never written back.

// --- DTOs ---------------------------------------------------------------

// SiteDTO is the wire shape of a site. Upstream is always populated
// (derived for app-linked sites, the stored value for free-upstream
// sites) so the UI and the Caddyfile see the same string.
type SiteDTO struct {
	ID       int    `json:"id"`
	Domain   string `json:"domain"`
	Upstream string `json:"upstream"`
	Enabled  bool   `json:"enabled"`
	Scheme   string `json:"scheme"`
	AppID    *int   `json:"appId,omitempty"`
	AppName  string `json:"appName,omitempty"`
	// AppService is the service within a compose-mode app that this
	// site proxies to. Empty for docker-mode apps and for free-upstream
	// sites. Drives upstream resolution together with the linked app.
	AppService string `json:"appService,omitempty"`
	// ExposedServices is the resolved list of ExposedPort on the
	// linked app (compose-mode only). Drives the service select in
	// the site editor — when AppService is unset, the UI uses this
	// to decide whether to require a service pick.
	ExposedServices []ExposedPort `json:"exposedServices,omitempty"`
	CreatedAt       string        `json:"createdAt"`
	UpdatedAt       string        `json:"updatedAt"`
}

// SiteInput is the request body for CreateSite / UpdateSite. The
// ClearApp flag is the explicit "detach this site" path; sending
// AppID=0 has the same effect but the explicit flag is preferred
// when the caller wants to make the intent obvious in a PATCH.
type SiteInput struct {
	Domain     *string `json:"domain"`
	Upstream   *string `json:"upstream"`
	Enabled    *bool   `json:"enabled"`
	AppID      *int    `json:"appId"`
	AppService *string `json:"appService"`
	Scheme     *string `json:"scheme"`
	// ClearApp detaches the site from any app (and clears
	// appService). Use this instead of sending AppID=0 so the
	// intent is explicit and the handler doesn't have to guess
	// the empty-FK convention.
	ClearApp bool `json:"clearApp"`
}

// toDTO renders a site for the API. Upstream is always populated:
// for app-linked sites it's the result of upstreamFor() applied to
// the (app, app_service) pair; for free-upstream sites it's the
// stored column value.
func toDTO(s *db.Site) SiteDTO {
	out := SiteDTO{
		ID:        s.ID,
		Domain:    s.Domain,
		Enabled:   s.Enabled,
		Scheme:    string(s.Scheme),
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if s.Upstream != nil {
		out.Upstream = *s.Upstream
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
		// For app-linked sites the stored column is always nil
		// (we never write it), so out.Upstream is still the
		// zero value here — overwrite with the derived string.
		if derived := upstreamFor(s.Edges.App, s.AppService); derived != "" {
			out.Upstream = derived
		}
	}
	return out
}

// --- HTTP handlers ------------------------------------------------------

func (h *Handlers) ListSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.DB.Site.Query().WithApp().Order(sitepkg.ByDomain()).All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]SiteDTO, 0, len(sites))
	for _, s := range sites {
		out = append(out, toDTO(s))
	}
	writeJSON(w, http.StatusOK, out)
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
	// apps we silently drop app_service — docker sites never use it.
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

	// Upstream is only stored for free-upstream sites. For app-linked
	// sites it's a pure function of (app, app_service); the value is
	// computed in toDTO. Validating here that we can compute it (i.e.
	// the service resolves to a port) gives the user a clear 400
	// rather than a silently-broken Caddyfile entry.
	if in.AppID != nil && *in.AppID != 0 {
		upstream := h.computeSiteUpstream(r.Context(), *in.AppID, appService)
		if upstream == "" {
			if appService == "" {
				writeErr(w, http.StatusBadRequest, errors.New("appService is required for compose apps"))
			} else {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("could not resolve upstream for app %d service %q: service not in exposed_ports", *in.AppID, appService))
			}
			return
		}
	} else {
		if strings.TrimSpace(*in.Upstream) == "" {
			writeErr(w, http.StatusBadRequest, errors.New("upstream is required when no app is linked"))
			return
		}
	}

	create := h.DB.Site.Create().
		SetDomain(strings.TrimSpace(*in.Domain)).
		SetEnabled(enabled).
		SetScheme(scheme)
	if in.AppID != nil && *in.AppID != 0 {
		create.SetAppID(*in.AppID)
		if appService != "" {
			create.SetAppService(appService)
		}
		// App-linked: do NOT write Upstream. It's derived.
	} else {
		upstream := strings.TrimSpace(*in.Upstream)
		create.SetUpstream(upstream)
	}
	site, err := create.Save(r.Context())
	if err != nil {
		writeDBErr(w, err)
		return
	}

	if err := h.regenerateAndReload(r); err != nil {
		_ = h.DB.Site.DeleteOneID(site.ID).Exec(r.Context())
		writeInternalErr(w, err)
		return
	}
	fresh, ferr := h.DB.Site.Query().WithApp().Where(sitepkg.IDEQ(site.ID)).Only(r.Context())
	dto := toDTO(site)
	if ferr == nil {
		dto = toDTO(fresh)
	}
	writeJSON(w, http.StatusCreated, dto)
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

	// Load the existing site (with the app edge) to validate
	// app/service combinations and to know what the previous app_id
	// was. The app FK lives on the edge, not as a column, so we
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

	// Free-upstream upstream column is only writable when the site
	// has no app linked. If the caller sends upstream together with
	// appId / clearApp, refuse — the field would be ignored on
	// read anyway (toDTO prefers the derived value), and silently
	// accepting it would be a footgun.
	willHaveApp := !in.ClearApp && (in.AppID == nil || *in.AppID != 0 || existingAppID != 0)
	if in.Upstream != nil {
		if willHaveApp {
			writeErr(w, http.StatusBadRequest, errors.New("upstream is derived for app-linked sites and cannot be set directly; clearApp first to switch to free-upstream mode"))
			return
		}
		upd.SetUpstream(strings.TrimSpace(*in.Upstream))
	}

	// App linkage is split into three explicit branches so the
	// caller's intent is unambiguous: ClearApp > AppID=0 >
	// SetAppID(N). The handler never silently treats AppID=0 as a
	// no-op, so a future bug that sends 0 by accident is
	// observable as a 400-ish detach, not a missed update.
	//
	// For app-linked sites the upstream column is intentionally
	// never written: the value is derived at render time.
	applySiteAppLinkage(upd, in, existingAppID)
	// AppService is validated against the target app's
	// exposed_ports; for docker apps we silently drop the value
	// rather than 400 (docker sites never use app_service).
	if in.AppService != nil {
		targetAppID := in.AppID
		if targetAppID == nil && existingAppID != 0 {
			targetAppID = &existingAppID
		}
		if err := h.applySiteAppService(upd, in.AppService, targetAppID); err != nil {
			if errors.Is(err, errAppServiceDB) {
				writeInternalErr(w, err)
				return
			}
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}

	// If the patch is creating an app-linked state, make sure the
	// (app, service) pair actually resolves to a port. We check
	// this against the post-update state — the same logic the
	// Caddyfile will see — so a future bug that lets an
	// unresolvable pair through can't render a 502.
	if willHaveApp {
		var targetAppID int
		if in.ClearApp {
			targetAppID = 0
		} else if in.AppID != nil {
			targetAppID = *in.AppID
		} else {
			targetAppID = existingAppID
		}
		var svc string
		if in.AppService != nil {
			svc = strings.TrimSpace(*in.AppService)
		} else if existing.AppService != nil {
			svc = strings.TrimSpace(*existing.AppService)
		}
		if targetAppID != 0 {
			if upstream := h.computeSiteUpstream(r.Context(), targetAppID, svc); upstream == "" {
				if svc == "" {
					writeErr(w, http.StatusBadRequest, errors.New("appService is required for compose apps"))
				} else {
					writeErr(w, http.StatusBadRequest, fmt.Errorf("could not resolve upstream for app %d service %q: service not in exposed_ports", targetAppID, svc))
				}
				return
			}
		}
	}

	site, err := upd.Save(r.Context())
	if err != nil {
		writeDBErr(w, err)
		return
	}

	if err := h.regenerateAndReload(r); err != nil {
		writeInternalErr(w, err)
		return
	}
	fresh, ferr := h.DB.Site.Query().WithApp().Where(sitepkg.IDEQ(id)).Only(r.Context())
	dto := toDTO(site)
	if ferr == nil {
		dto = toDTO(fresh)
	}
	writeJSON(w, http.StatusOK, dto)
}

func (h *Handlers) DeleteSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if err := h.DB.Site.DeleteOneID(id).Exec(r.Context()); err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("site not found"))
			return
		}
		writeDBErr(w, err)
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
	fresh, ferr := h.DB.Site.Query().WithApp().Where(sitepkg.IDEQ(id)).Only(r.Context())
	dto := toDTO(updated)
	if ferr == nil {
		dto = toDTO(fresh)
	}
	writeJSON(w, http.StatusOK, dto)
}

// --- UpdateSite helpers -------------------------------------------------

// applySiteAppLinkage handles the three explicit "change the app
// edge" branches (ClearApp / AppID=0 / AppID=N). The upstream
// column is no longer touched here — app-linked sites compute
// upstream at render time, and ClearApp / AppID=0 transitions
// leave the stored upstream alone (the editor and free-upstream
// code paths set it explicitly when appropriate).
func applySiteAppLinkage(upd *db.SiteUpdateOne, in SiteInput, existingAppID int) {
	switch {
	case in.ClearApp:
		upd.ClearApp()
		upd.ClearAppService()
	case in.AppID != nil:
		if *in.AppID == 0 {
			upd.ClearApp()
			upd.ClearAppService()
		} else {
			upd.SetAppID(*in.AppID)
		}
		_ = existingAppID // kept for signature symmetry; the upstream no longer depends on it
	}
}

// applySiteAppService validates the requested service against the
// target app's exposed_ports and queues SetAppService /
// ClearAppService on the builder. For docker-mode apps the value is
// silently dropped (the caller's intent is preserved by the field
// type, not by us) so a future docker→compose switch on the same
// site doesn't have to remember to clear it.
//
// targetAppID is the post-update app FK; pass nil for sites with
// no app (the field is then a no-op).
//
// Errors fall in two categories that callers must distinguish:
//   - A wrapped *ent* / DB error from the load below — surfaced via
//     errAppServiceDB so handlers can return 500 (writeInternalErr)
//     instead of echoing the internal ent message at 400.
//   - Input-validation errors (ParseExposedPorts, "not in
//     exposed_ports") — safe to surface at 400.
func (h *Handlers) applySiteAppService(upd *db.SiteUpdateOne, inAppService *string, targetAppID *int) error {
	if targetAppID == nil || *targetAppID == 0 {
		return nil
	}
	app, err := h.DB.App.Get(context.Background(), *targetAppID)
	if err != nil {
		return fmt.Errorf("%w: load app for service check: %v", errAppServiceDB, err)
	}
	if app.DeployMethod != "compose" {
		// docker-mode: ignore the value. We don't ClearAppService
		// either — preserve whatever the caller might want to
		// keep for a future docker→compose switch.
		return nil
	}
	svc := strings.TrimSpace(*inAppService)
	if svc == "" {
		upd.ClearAppService()
		return nil
	}
	ports, perr := ParseExposedPorts(app.ExposedPorts)
	if perr != nil {
		return perr
	}
	if findExposedPort(ports, svc) == nil {
		return fmt.Errorf("appService %q not in app's exposed_ports", svc)
	}
	upd.SetAppService(svc)
	return nil
}

// errAppServiceDB is the sentinel that wrap-loaded DB errors in
// applySiteAppService. The handler uses errors.Is to decide between
// 500 (writeInternalErr) and 400 (writeErr with the safe message).
var errAppServiceDB = errors.New("applySiteAppService: db")

// --- Upstream resolution ------------------------------------------------

// upstreamFor is the single source of truth for app-linked
// upstreams. The returned string is what Caddy reverse-proxies to;
// it depends only on the network alias (a function of app+service)
// and the exposed port, never on the actual container name.
//
// Returns "" when the inputs are insufficient to compute a value
// (e.g. app has no current_container in docker mode, or the
// service is not in exposed_ports in compose mode). Callers should
// treat that as "site is in an inconsistent state" and surface a
// 400 / 502 rather than guessing.
//
// docker mode:  nanoku-<app>:<port>
// compose mode: nanoku-<app>-<service>:<port>
func upstreamFor(a *db.App, appService *string) string {
	if a == nil {
		return ""
	}
	if a.DeployMethod != "compose" {
		// Docker-mode app: a single container with a stable
		// alias `nanoku-<app>`. Caddy talks to the alias, not
		// the container name, so the Caddyfile never changes
		// across redeploys.
		return networkAliasForApp(a.Name) + ":" + strconv.Itoa(a.Port)
	}
	// Compose mode. The site must select a service, or
	// auto-pick the lone service when the stack has
	// exactly one exposed port (single-service stacks are
	// the common case and don't deserve a UI prompt).
	var svc string
	if appService != nil {
		svc = strings.TrimSpace(*appService)
	}
	ports, err := ParseExposedPorts(a.ExposedPorts)
	if err != nil {
		return ""
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
	return networkAliasForService(a.Name, svc) + ":" + strconv.Itoa(ep.Port)
}

// computeSiteUpstream is a thin ctx-aware wrapper used by the
// create / update handlers that have a target app_id but no loaded
// App edge yet.
func (h *Handlers) computeSiteUpstream(ctx context.Context, appID int, appService string) string {
	a, err := h.DB.App.Query().
		Where(app.IDEQ(appID)).
		Only(ctx)
	if err != nil {
		return ""
	}
	var svcPtr *string
	if appService != "" {
		svcPtr = &appService
	}
	return upstreamFor(a, svcPtr)
}

// resolveCaddySites projects every site to the caddy.Site shape
// the renderer wants: domain, scheme, fully resolved upstream,
// enabled. App-linked sites are resolved through upstreamFor;
// free-upstream sites read from the stored column. The result is
// the single input to caddy.Render — both CaddyfilePreview and
// regenerateAndReloadCtx go through here so the live Caddyfile
// and the preview pane are always in lockstep.
func (h *Handlers) resolveCaddySites(ctx context.Context) ([]caddy.Site, error) {
	sites, err := h.DB.Site.Query().WithApp().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]caddy.Site, 0, len(sites))
	for _, s := range sites {
		cs := caddy.Site{
			Domain:  s.Domain,
			Scheme:  string(s.Scheme),
			Enabled: s.Enabled,
		}
		switch {
		case s.Edges.App != nil:
			cs.Upstream = upstreamFor(s.Edges.App, s.AppService)
		case s.Upstream != nil:
			cs.Upstream = *s.Upstream
		}
		// Empty Upstream at this point would mean a stale app
		// row (e.g. service removed from exposed_ports) or a
		// docker app with no current_container before the first
		// deploy. Skip the row entirely — the site can still be
		// listed in the UI, it just won't be in the Caddyfile.
		// A 502 from a stale row is strictly worse than a
		// missing reverse_proxy entry that the operator can
		// notice.
		if cs.Upstream == "" {
			continue
		}
		out = append(out, cs)
	}
	return out, nil
}
