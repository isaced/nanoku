package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/isaced/nanoku/internal/caddy"
	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	sitepkg "github.com/isaced/nanoku/internal/db/site"
	"github.com/isaced/nanoku/internal/docker"
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
	Sessions        *SessionStore
	Version         string
	Commit          string
	Date            string
	BuildType       string
	TrustProxy      bool
}

type SiteDTO struct {
	ID        int    `json:"id"`
	Domain    string `json:"domain"`
	Upstream  string `json:"upstream"`
	Enabled   bool   `json:"enabled"`
	Scheme    string `json:"scheme"`
	AppID     *int   `json:"appId,omitempty"`
	AppName   string `json:"appName,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type SiteInput struct {
	Domain   *string `json:"domain"`
	Upstream *string `json:"upstream"`
	Enabled  *bool   `json:"enabled"`
	AppID    *int    `json:"appId"`
	Scheme   *string `json:"scheme"`
}

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
		if s.Edges.App.Edges.CurrentContainer != nil {
			out.Upstream = s.Edges.App.Edges.CurrentContainer.Name + ":" + strconv.Itoa(s.Edges.App.Port)
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

	upd := h.DB.Site.UpdateOneID(id)
	if in.Domain != nil {
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
	if in.AppID != nil {
		if *in.AppID == 0 {
			upd.ClearApp()
			upd.SetUpstream("")
		} else {
			upd.SetAppID(*in.AppID)
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
	if in.AppID == nil && (in.Upstream == nil || strings.TrimSpace(*in.Upstream) == "") {
		writeErr(w, http.StatusBadRequest, errors.New("either upstream or appId is required"))
		return
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

	create := h.DB.Site.Create().
		SetDomain(strings.TrimSpace(*in.Domain)).
		SetEnabled(enabled).
		SetScheme(scheme)
	if in.Upstream != nil && strings.TrimSpace(*in.Upstream) != "" {
		create.SetUpstream(strings.TrimSpace(*in.Upstream))
	} else {
		// placeholder; will be replaced once the app has a running container
		create.SetUpstream("placeholder:0")
	}
	if in.AppID != nil {
		create.SetAppID(*in.AppID)
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
// if a site has an app linked with a current container, upstream becomes
// "container-name:port". Otherwise the stored upstream is used.
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
		if s.Edges.App != nil && s.Edges.App.Edges.CurrentContainer != nil {
			cp.Upstream = s.Edges.App.Edges.CurrentContainer.Name + ":" + strconv.Itoa(s.Edges.App.Port)
		}
		resolved = append(resolved, &cp)
	}
	return resolved, nil
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
