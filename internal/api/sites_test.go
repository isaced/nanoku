package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/site"
)

// newSiteTestHandlers builds a Handlers with a fresh DB and the
// minimal wire-up sites tests need: a Sealer (sites don't touch
// encryption but the field is non-nil) and a caddyfile path the
// regenerate call can write to without touching the real one.
//
// Sites tests don't exercise Caddy reload (SkipCaddyReload=true), so
// the absence of a Docker manager is fine — the only piece of code
// that requires a real Docker is the per-deploy reconcile hook, which
// is tested separately.
func newSiteTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	return &Handlers{
		DB:              newTestDB(t),
		Secret:          newTestSealer(t),
		CaddyfilePath:   t.TempDir() + "/Caddyfile",
		SkipCaddyReload: true,
	}
}

// seedAppDocker creates a docker-mode app with port 80 and a synthetic
// "running" current_container. Tests that need an actual linked
// site use this.
func seedAppDocker(t *testing.T, h *Handlers, name string) *db.App {
	t.Helper()
	a, err := h.DB.App.Create().
		SetName(name).
		SetDeployMethod("docker").
		SetImage("nginx:1.27").
		SetPort(80).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed docker app: %v", err)
	}
	cont, err := h.DB.Container.Create().
		SetDockerID("").
		SetName("nanoku-" + name).
		SetImage("nginx:1.27").
		SetStatus("running").
		SetAppID(a.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed container: %v", err)
	}
	if _, err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(cont.ID).Save(context.Background()); err != nil {
		t.Fatalf("set current_container: %v", err)
	}
	return a
}

// seedAppCompose creates a compose-mode app with a given exposed_ports
// list. No container rows are created — compose sites don't depend on
// the legacy current_container pointer.
func seedAppCompose(t *testing.T, h *Handlers, name string, ports []ExposedPort) *db.App {
	t.Helper()
	epJSON, err := MarshalExposedPorts(ports)
	if err != nil {
		t.Fatalf("marshal exposed_ports: %v", err)
	}
	create := h.DB.App.Create().
		SetName(name).
		SetDeployMethod("compose").
		SetComposeContent("services:\n  web:\n    image: nginx:1.27\n")
	if epJSON != nil {
		create.SetExposedPorts(*epJSON)
	}
	a, err := create.Save(context.Background())
	if err != nil {
		t.Fatalf("seed compose app: %v", err)
	}
	return a
}

// postSite drives the CreateSite handler end-to-end and returns the
// decoded SiteDTO (or fails the test on a non-2xx).
func postSite(t *testing.T, h *Handlers, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sites", &buf)
	w := httptest.NewRecorder()
	h.CreateSite(w, req)
	return w
}

// patchSite drives UpdateSite end-to-end.
func patchSite(t *testing.T, h *Handlers, id int, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/sites/"+strconv.Itoa(id), &buf)
	req.SetPathValue("id", strconv.Itoa(id))
	w := httptest.NewRecorder()
	h.UpdateSite(w, req)
	return w
}

// importExposedPorts drives the ImportExposedPorts handler. The
// path declares {id} so the handler reads it via pathID, which
// uses r.PathValue — set the value here so the handler sees a
// real integer.
func importExposedPorts(t *testing.T, h *Handlers, id int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/apps/"+strconv.Itoa(id)+"/exposed-ports/import", nil)
	req.SetPathValue("id", strconv.Itoa(id))
	w := httptest.NewRecorder()
	h.ImportExposedPorts(w, req)
	return w
}

func decodeSite(t *testing.T, w *httptest.ResponseRecorder) SiteDTO {
	t.Helper()
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 2xx; body = %s", w.Code, w.Body.String())
	}
	var dto SiteDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return dto
}

// --- docker mode (legacy behavior) --------------------------------------

// TestCreateSite_DockerApp_ResolvesUpstreamFromContainer is the
// regression guard for the docker path: a site linked to a docker-mode
// app must have its upstream derived from the current_container name
// and the app's port, not from any exposed_ports (which is empty for
// docker apps by design).
func TestCreateSite_DockerApp_ResolvesUpstreamFromContainer(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppDocker(t, h, "blog")

	w := postSite(t, h, map[string]any{"domain": "blog.example.com", "appId": a.ID})
	dto := decodeSite(t, w)

	if dto.AppID == nil || *dto.AppID != a.ID {
		t.Fatalf("appId = %v, want %d", dto.AppID, a.ID)
	}
	if dto.AppService != "" {
		t.Errorf("appService = %q, want empty (docker mode)", dto.AppService)
	}
	want := "nanoku-blog:80"
	if dto.Upstream != want {
		t.Errorf("upstream = %q, want %q", dto.Upstream, want)
	}
}

// TestUpdateSite_DockerApp_NoUpstreamRewriteOnUnrelatedPatch is the
// contract guard: a PATCH that doesn't touch the app linkage or
// appService must NOT silently rewrite the stored upstream. Upstream
// rewrites are gated on the app edge / service actually changing
// (or RefreshSitesForApp being called explicitly). Otherwise a no-op
// PATCH like {enabled: true} would lose a previous deploy-driven
// upstream refresh, which would be very surprising.
func TestUpdateSite_DockerApp_NoUpstreamRewriteOnUnrelatedPatch(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppDocker(t, h, "blog")

	w := postSite(t, h, map[string]any{"domain": "blog.example.com", "appId": a.ID})
	dto := decodeSite(t, w)
	if dto.Upstream != "nanoku-blog:80" {
		t.Fatalf("initial upstream = %q, want nanoku-blog:80", dto.Upstream)
	}

	// Roll the container. The site's stored upstream is now stale
	// (still points at nanoku-blog, not the new container name).
	if _, err := h.DB.App.UpdateOneID(a.ID).ClearCurrentContainer().Save(context.Background()); err != nil {
		t.Fatalf("clear current_container: %v", err)
	}
	newCont, err := h.DB.Container.Create().
		SetDockerID("").
		SetName("nanoku-blog-v2").
		SetImage("nginx:1.27").
		SetStatus("running").
		SetAppID(a.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed new container: %v", err)
	}
	if _, err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(newCont.ID).Save(context.Background()); err != nil {
		t.Fatalf("swap current_container: %v", err)
	}

	// PATCH that touches nothing related to the app edge.
	w2 := patchSite(t, h, dto.ID, map[string]any{"enabled": true})
	dto2 := decodeSite(t, w2)
	if dto2.Upstream != "nanoku-blog:80" {
		t.Errorf("upstream after no-op PATCH = %q, want unchanged (nanoku-blog:80)", dto2.Upstream)
	}
}

// --- compose mode -------------------------------------------------------

// TestCreateSite_ComposeApp_ResolvesFromExposedPort is the headline
// test for the feature: a compose app with a single exposed_port
// (and no app_service set) must auto-pick that service.
func TestCreateSite_ComposeApp_ResolvesFromExposedPort(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppCompose(t, h, "kuma", []ExposedPort{{Name: "uptime-kuma", Port: 3001}})

	w := postSite(t, h, map[string]any{"domain": "kuma.example.com", "appId": a.ID})
	dto := decodeSite(t, w)

	if dto.AppID == nil || *dto.AppID != a.ID {
		t.Fatalf("appId = %v, want %d", dto.AppID, a.ID)
	}
	want := "nanoku-kuma-uptime-kuma-1:3001"
	if dto.Upstream != want {
		t.Errorf("upstream = %q, want %q", dto.Upstream, want)
	}
	if len(dto.ExposedServices) != 1 || dto.ExposedServices[0].Name != "uptime-kuma" {
		t.Errorf("exposedServices = %+v, want the single kuma entry", dto.ExposedServices)
	}
}

// TestCreateSite_ComposeApp_AppServiceOverridesExposedPort covers the
// "user picked a specific service" path. We supply appService
// explicitly even though there's only one option, to confirm the
// field flows through and is stored.
func TestCreateSite_ComposeApp_AppServiceOverridesExposedPort(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppCompose(t, h, "stack", []ExposedPort{
		{Name: "web", Port: 80},
		{Name: "api", Port: 8080},
	})

	w := postSite(t, h, map[string]any{
		"domain":     "api.example.com",
		"appId":      a.ID,
		"appService": "api",
	})
	dto := decodeSite(t, w)
	if dto.Upstream != "nanoku-stack-api-1:8080" {
		t.Errorf("upstream = %q, want nanoku-stack-api-1:8080", dto.Upstream)
	}
	if dto.AppService != "api" {
		t.Errorf("appService = %q, want api", dto.AppService)
	}
}

// TestCreateSite_ComposeApp_AppServiceNotInExposedPort_400 covers the
// input validator: a service name that isn't in the app's
// exposed_ports is a 400, not a silent fallback to a broken upstream.
func TestCreateSite_ComposeApp_AppServiceNotInExposedPort_400(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppCompose(t, h, "stack", []ExposedPort{{Name: "web", Port: 80}})

	w := postSite(t, h, map[string]any{
		"domain":     "x.example.com",
		"appId":      a.ID,
		"appService": "missing",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing") {
		t.Errorf("body should mention the bad service name: %s", w.Body.String())
	}
}

// TestCreateSite_ComposeApp_AppServiceIgnoredForDockerApp covers the
// docker-mode site: app_service in the input is silently dropped, not
// an error. A docker site uses App.port, not a service.
func TestCreateSite_ComposeApp_AppServiceIgnoredForDockerApp(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppDocker(t, h, "blog")

	w := postSite(t, h, map[string]any{
		"domain":     "blog.example.com",
		"appId":      a.ID,
		"appService": "ignored",
	})
	dto := decodeSite(t, w)
	if dto.Upstream != "nanoku-blog:80" {
		t.Errorf("upstream = %q, want nanoku-blog:80", dto.Upstream)
	}
	if dto.AppService != "" {
		t.Errorf("appService = %q, want empty for docker", dto.AppService)
	}
}

// TestUpdateSite_ComposeApp_ChangeServiceRewritesUpstream covers the
// "swap which service a site proxies to" path. The first site is
// created against `web`; the PATCH flips it to `api` and the stored
// upstream is rewritten in lockstep.
func TestUpdateSite_ComposeApp_ChangeServiceRewritesUpstream(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppCompose(t, h, "stack", []ExposedPort{
		{Name: "web", Port: 80},
		{Name: "api", Port: 8080},
	})

	w := postSite(t, h, map[string]any{
		"domain":     "x.example.com",
		"appId":      a.ID,
		"appService": "web",
	})
	dto := decodeSite(t, w)
	if dto.Upstream != "nanoku-stack-web-1:80" {
		t.Fatalf("initial upstream = %q", dto.Upstream)
	}

	w2 := patchSite(t, h, dto.ID, map[string]any{"appService": "api"})
	dto2 := decodeSite(t, w2)
	if dto2.Upstream != "nanoku-stack-api-1:8080" {
		t.Errorf("after swap upstream = %q, want nanoku-stack-api-1:8080", dto2.Upstream)
	}
	if dto2.AppService != "api" {
		t.Errorf("appService = %q, want api", dto2.AppService)
	}
}

// TestUpdateSite_ClearApp_DetachesAndClearsUpstream exercises the
// explicit clearApp flag. Sending ClearApp=true must drop the app
// edge, the appService, and the upstream in one shot.
func TestUpdateSite_ClearApp_DetachesAndClearsUpstream(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppCompose(t, h, "stack", []ExposedPort{{Name: "web", Port: 80}})

	w := postSite(t, h, map[string]any{
		"domain":     "x.example.com",
		"appId":      a.ID,
		"appService": "web",
	})
	dto := decodeSite(t, w)

	w2 := patchSite(t, h, dto.ID, map[string]any{"clearApp": true})
	dto2 := decodeSite(t, w2)
	if dto2.AppID != nil {
		t.Errorf("appId = %v, want nil after clear", dto2.AppID)
	}
	if dto2.AppService != "" {
		t.Errorf("appService = %q, want empty", dto2.AppService)
	}
	if dto2.Upstream != "" {
		t.Errorf("upstream = %q, want empty", dto2.Upstream)
	}

	// The DB row should be entirely detached now.
	row, err := h.DB.Site.Query().Where(site.IDEQ(dto.ID)).WithApp().Only(context.Background())
	if err != nil {
		t.Fatalf("get site: %v", err)
	}
	if row.Edges.App != nil {
		t.Errorf("db row app edge = %+v, want nil", row.Edges.App)
	}
}

// --- upstream resolution helpers ---------------------------------------

// TestComputeUpstreamForApp_Docker exercises the upstream formula
// directly so we don't have to spin up a Handlers/Docker stack just
// for the calculation.
func TestComputeUpstreamForApp_Docker(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppDocker(t, h, "blog")
	a2, err := h.DB.App.Query().Where(app.IDEQ(a.ID)).WithCurrentContainer().Only(context.Background())
	if err != nil {
		t.Fatalf("reload app with current_container: %v", err)
	}
	got := computeUpstreamForApp(a2, nil)
	if got != "nanoku-blog:80" {
		t.Errorf("got %q, want nanoku-blog:80", got)
	}
}

func TestComputeUpstreamForApp_Compose_AutoSingle(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppCompose(t, h, "kuma", []ExposedPort{{Name: "uptime-kuma", Port: 3001}})
	a2, err := h.DB.App.Query().Where(app.IDEQ(a.ID)).WithCurrentContainer().Only(context.Background())
	if err != nil {
		t.Fatalf("reload app: %v", err)
	}
	got := computeUpstreamForApp(a2, nil)
	if got != "nanoku-kuma-uptime-kuma-1:3001" {
		t.Errorf("got %q, want nanoku-kuma-uptime-kuma-1:3001", got)
	}
}

func TestComputeUpstreamForApp_Compose_MultiNeedsService(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppCompose(t, h, "stack", []ExposedPort{
		{Name: "web", Port: 80},
		{Name: "api", Port: 8080},
	})
	a2, err := h.DB.App.Query().Where(app.IDEQ(a.ID)).WithCurrentContainer().Only(context.Background())
	if err != nil {
		t.Fatalf("reload app: %v", err)
	}
	// No service set + multiple exposed ports → cannot auto-resolve.
	if got := computeUpstreamForApp(a2, nil); got != "" {
		t.Errorf("with no service got %q, want empty", got)
	}
	// With service set → resolves.
	api := "api"
	if got := computeUpstreamForApp(a2, &api); got != "nanoku-stack-api-1:8080" {
		t.Errorf("with service=api got %q, want nanoku-stack-api-1:8080", got)
	}
}

func TestComputeUpstreamForApp_Compose_StaleServiceFallsThrough(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppCompose(t, h, "stack", []ExposedPort{{Name: "web", Port: 80}})
	a2, err := h.DB.App.Query().Where(app.IDEQ(a.ID)).WithCurrentContainer().Only(context.Background())
	if err != nil {
		t.Fatalf("reload app: %v", err)
	}
	// Stale service name (deleted from exposed_ports) → empty so the
	// stored upstream is preserved. The site is not auto-rewritten to
	// blank.
	missing := "missing"
	if got := computeUpstreamForApp(a2, &missing); got != "" {
		t.Errorf("with stale service got %q, want empty (fallthrough)", got)
	}
}

// TestRefreshSitesForApp_RewritesUpstreamAfterContainerRoll covers the
// post-deploy reconcile hook. After a docker roll, the linked site's
// stored upstream is stale; the helper must update it.
func TestRefreshSitesForApp_RewritesUpstreamAfterContainerRoll(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppDocker(t, h, "blog")

	// Create a site linked to the app.
	w := postSite(t, h, map[string]any{"domain": "blog.example.com", "appId": a.ID})
	dto := decodeSite(t, w)
	if dto.Upstream != "nanoku-blog:80" {
		t.Fatalf("initial upstream = %q", dto.Upstream)
	}

	// Roll the container: replace current_container with a new name.
	// Clear first — the FK is UNIQUE per app, so the second Set would
	// collide with the first container row that still has it.
	if _, err := h.DB.App.UpdateOneID(a.ID).ClearCurrentContainer().Save(context.Background()); err != nil {
		t.Fatalf("clear current_container: %v", err)
	}
	newCont, err := h.DB.Container.Create().
		SetDockerID("").
		SetName("nanoku-blog-v2").
		SetImage("nginx:1.27").
		SetStatus("running").
		SetAppID(a.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed new container: %v", err)
	}
	if _, err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(newCont.ID).Save(context.Background()); err != nil {
		t.Fatalf("swap current_container: %v", err)
	}

	if err := h.RefreshSitesForApp(context.Background(), a.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	row, err := h.DB.Site.Get(context.Background(), dto.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Upstream != "nanoku-blog-v2:80" {
		t.Errorf("upstream after refresh = %q, want nanoku-blog-v2:80", row.Upstream)
	}
}

// TestRefreshSitesForApp_NoChangeNoOp is the no-op guard: if the
// recomputed upstream equals what's stored, no DB write is performed.
// We don't assert that directly (ent's UpdateOneID is a no-op anyway
// for unchanged values), but the test guards against a regression
// where Refresh does an unconditional write.
func TestRefreshSitesForApp_NoChangeNoOp(t *testing.T) {
	h := newSiteTestHandlers(t)
	a := seedAppDocker(t, h, "blog")

	w := postSite(t, h, map[string]any{"domain": "blog.example.com", "appId": a.ID})
	dto := decodeSite(t, w)

	// No container roll. Refresh should be a no-op and not error.
	if err := h.RefreshSitesForApp(context.Background(), a.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// Pull a fresh read to confirm nothing moved.
	row, err := h.DB.Site.Query().Where(site.IDEQ(dto.ID)).Only(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Upstream != "nanoku-blog:80" {
		t.Errorf("upstream = %q, want unchanged", row.Upstream)
	}
}
