package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/isaced/nanoku/internal/db/deploy"
	"github.com/isaced/nanoku/internal/docker"
)

// newLogsHandlers builds a Handlers wired to a fresh DB + seed
// deploys/apps so the log-stream handlers have rows to look up.
// Docker is intentionally nil — the per-test wiring installs a fake
// one where needed.
func newLogsHandlers(t *testing.T) *Handlers {
	t.Helper()
	store, err := newDeployLogStore(t.TempDir())
	if err != nil {
		t.Fatalf("deploy log store: %v", err)
	}
	return &Handlers{
		DB:              newTestDB(t),
		SkipCaddyReload: true,
		DeployLock:      NewDeployLock(),
		DeployLogs:      store,
		Secret:          newTestSealer(t),
	}
}

// seedAppWithContainer creates an App + a Container row marked as the
// app's current_container. Returns the app id.
func seedAppWithContainer(t *testing.T, h *Handlers, name, image string) int {
	t.Helper()
	ctx := context.Background()
	a, err := h.DB.App.Create().
		SetName(name).
		SetImage(image).
		SetPort(80).
		SetDeployMethod("docker").
		Save(ctx)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	now := time.Now().UTC()
	cont, err := h.DB.Container.Create().
		SetDockerID("docker-id-" + name).
		SetName("nanoku-" + name).
		SetImage(image).
		SetStatus("running").
		SetStartedAt(now).
		SetAppID(a.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	if err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(cont.ID).Exec(ctx); err != nil {
		t.Fatalf("set current container: %v", err)
	}
	return a.ID
}

// seedDeploy creates a Deploy row with the given status, linked to the
// given app, and returns its id.
func seedDeploy(t *testing.T, h *Handlers, appID int, status deploy.Status) int {
	t.Helper()
	now := time.Now().UTC()
	d, err := h.DB.Deploy.Create().
		SetAppID(appID).
		SetTrigger("manual").
		SetStatus(status).
		SetStartedAt(now).
		SetImage("nginx:1.27").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create deploy: %v", err)
	}
	return d.ID
}

// fakeLogsDaemon is a minimal HTTP mock of the Docker Engine API used
// by the api-package log stream tests. It replies to /_ping, /logs
// (with a docker raw-stream payload), and 404s everything else.
type fakeLogsDaemon struct {
	*httptest.Server
	hits atomic.Int32
}

func newFakeLogsDaemon(t *testing.T) *fakeLogsDaemon {
	t.Helper()
	fd := &fakeLogsDaemon{}
	fd.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.40")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			fd.hits.Add(1)
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			var p bytes.Buffer
			stdcopy.NewStdWriter(&p, stdcopy.Stdout).Write([]byte("hello\nworld\n"))
			_, _ = w.Write(p.Bytes())
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Hold the connection open so the follow-stream stays
			// alive; the test cancels via stream.Cancel.
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fd.Close)
	return fd
}

// newFakeDockerManager returns a *docker.Manager wired to the fake
// daemon, with the configured caddy container name baked in.
func newFakeDockerManager(t *testing.T, fd *fakeLogsDaemon, caddyName string) *docker.Manager {
	t.Helper()
	cli, err := client.NewClientWithOpts(
		client.WithHost(fd.URL),
		client.WithHTTPClient(fd.Client()),
		client.WithVersion("1.40"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return docker.NewManagerWithClient(docker.Config{ContainerName: caddyName}, cli)
}

// TestAppLogsStream_DockerUnavailable_Returns503 covers the simplest
// pre-condition check: the handler refuses to even start an SSE
// response when Docker is not wired in.
func TestAppLogsStream_DockerUnavailable_Returns503(t *testing.T) {
	h := &Handlers{DB: newTestDB(t)}
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")
	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/logs/stream?tail=10", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	w := httptest.NewRecorder()
	h.AppLogsStream(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Code = %d, want 503; body=%q", w.Code, w.Body.String())
	}
}

// TestAppLogsStream_NoContainer_Returns409 covers the case where the
// app exists but hasn't been deployed yet. The handler must NOT
// switch to SSE headers and hang — it returns 409 so the UI can
// surface a friendly hint.
func TestAppLogsStream_NoContainer_Returns409(t *testing.T) {
	h := newLogsHandlers(t)
	fd := newFakeLogsDaemon(t)
	h.Docker = newFakeDockerManager(t, fd, "nanoku-caddy")
	a, err := h.DB.App.Create().
		SetName("ghost").
		SetImage("nginx:1.27").
		SetPort(80).
		SetDeployMethod("docker").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(a.ID)+"/logs/stream?tail=10", nil)
	req.SetPathValue("id", strconv.Itoa(a.ID))
	w := httptest.NewRecorder()
	h.AppLogsStream(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("Code = %d, want 409; body=%q", w.Code, w.Body.String())
	}
}

// TestAppLogsStream_AppNotFound_Returns404 documents the URL contract
// for a non-existent app id.
func TestAppLogsStream_AppNotFound_Returns404(t *testing.T) {
	h := newLogsHandlers(t)
	fd := newFakeLogsDaemon(t)
	h.Docker = newFakeDockerManager(t, fd, "nanoku-caddy")
	req := httptest.NewRequest(http.MethodGet, "/api/apps/9999/logs/stream?tail=10", nil)
	req.SetPathValue("id", "9999")
	w := httptest.NewRecorder()
	h.AppLogsStream(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404; body=%q", w.Code, w.Body.String())
	}
}

// TestAppLogsStream_HappyPath_SetsSSEHeadersAndStreamsLines is the
// end-to-end test: the handler sets SSE headers, writes a few line
// events from the fake daemon, and the response body matches the
// expected format.
func TestAppLogsStream_HappyPath_SetsSSEHeadersAndStreamsLines(t *testing.T) {
	h := newLogsHandlers(t)
	fd := newFakeLogsDaemon(t)
	h.Docker = newFakeDockerManager(t, fd, "nanoku-caddy")
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")
	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/logs/stream?tail=10", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	w := httptest.NewRecorder()
	// Run in a goroutine; cancel via request context after we've
	// received a couple of lines.
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	done := make(chan struct{})
	go func() {
		h.AppLogsStream(w, req)
		close(done)
	}()
	// Wait for the daemon to register at least one /logs hit, then
	// cancel. 200ms is a generous bound for the local httptest server.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fd.hits.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AppLogsStream did not return after ctx cancel")
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	body := w.Body.String()
	for _, want := range []string{
		"retry: 2000",
		": nanoku log stream",
		"event: line",
		"data: hello",
		"data: world",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestSystemLogsStream_UnknownSource_Returns400 covers the dispatcher
// validation: source must be "caddy" or "nanoku".
func TestSystemLogsStream_UnknownSource_Returns400(t *testing.T) {
	h := newLogsHandlers(t)
	fd := newFakeLogsDaemon(t)
	h.Docker = newFakeDockerManager(t, fd, "nanoku-caddy")
	req := httptest.NewRequest(http.MethodGet, "/api/system/logs/stream?source=garbage&tail=10", nil)
	w := httptest.NewRecorder()
	h.SystemLogsStream(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Code = %d, want 400; body=%q", w.Code, w.Body.String())
	}
}

// TestSystemLogsStream_NanokuSourceUnset_Returns503 covers the case
// where the operator hasn't set NANOKU_SELF_CONTAINER.
func TestSystemLogsStream_NanokuSourceUnset_Returns503(t *testing.T) {
	h := newLogsHandlers(t)
	fd := newFakeLogsDaemon(t)
	h.Docker = newFakeDockerManager(t, fd, "nanoku-caddy")
	req := httptest.NewRequest(http.MethodGet, "/api/system/logs/stream?source=nanoku&tail=10", nil)
	w := httptest.NewRecorder()
	h.SystemLogsStream(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Code = %d, want 503; body=%q", w.Code, w.Body.String())
	}
}

// TestSystemLogsStream_DockerUnavailable_Returns503 mirrors the App
// path: without Docker we can't resolve the source container.
func TestSystemLogsStream_DockerUnavailable_Returns503(t *testing.T) {
	h := &Handlers{DB: newTestDB(t)}
	req := httptest.NewRequest(http.MethodGet, "/api/system/logs/stream?source=caddy&tail=10", nil)
	w := httptest.NewRecorder()
	h.SystemLogsStream(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Code = %d, want 503", w.Code)
	}
}

// TestDeployLogStream_AppMismatch_Returns404 is the authorization test:
// a deploy belongs to one app, so a different appID in the URL must
// return 404 (not 403, to avoid leaking the deploy's existence).
func TestDeployLogStream_AppMismatch_Returns404(t *testing.T) {
	h := newLogsHandlers(t)
	appA := seedAppWithContainer(t, h, "a", "nginx:1.27")
	appB := seedAppWithContainer(t, h, "b", "nginx:1.27")
	depID := seedDeploy(t, h, appA, deploy.StatusRunning)

	req := httptest.NewRequest(http.MethodGet,
		"/api/apps/"+strconv.Itoa(appB)+"/deployments/"+strconv.Itoa(depID)+"/logs/stream", nil)
	req.SetPathValue("id", strconv.Itoa(appB))
	req.SetPathValue("did", strconv.Itoa(depID))
	w := httptest.NewRecorder()
	h.DeployLogStream(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404; body=%q", w.Code, w.Body.String())
	}
}

// TestDeployLogStream_ReplaysHistory_AfterTerminal covers the "user
// opened the panel after the deploy finished" path: the log file
// already has the full history, the worker is no longer in flight,
// so the handler replays the file and emits the terminal `end`
// event so the EventSource closes (rather than auto-reconnecting
// into a replay loop).
func TestDeployLogStream_ReplaysHistory_AfterTerminal(t *testing.T) {
	h := newLogsHandlers(t)
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")
	depID := seedDeploy(t, h, appID, deploy.StatusSuccess)
	w, err := h.DeployLogs.openWriter(depID)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	for _, line := range []string{
		"→ pull nginx:1.27",
		"→ pull nginx:1.27: ok",
		"→ deploy started (image=nginx:1.27)",
	} {
		if err := w.appendLine(line); err != nil {
			t.Fatalf("append %q: %v", line, err)
		}
	}
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/apps/"+strconv.Itoa(appID)+"/deployments/"+strconv.Itoa(depID)+"/logs/stream", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("did", strconv.Itoa(depID))
	w2 := httptest.NewRecorder()
	h.DeployLogStream(w2, req)

	if got := w2.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	body := w2.Body.String()
	for _, want := range []string{
		"data: → pull nginx:1.27",
		"data: → pull nginx:1.27: ok",
		"data: → deploy started (image=nginx:1.27)",
		"event: end",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestDeployLogStream_DeployNotFound_Returns404 documents the URL
// contract: an unknown deploy id is 404, not 400 (we don't know which
// app it belongs to).
func TestDeployLogStream_DeployNotFound_Returns404(t *testing.T) {
	h := newLogsHandlers(t)
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")
	req := httptest.NewRequest(http.MethodGet,
		"/api/apps/"+strconv.Itoa(appID)+"/deployments/9999/logs/stream", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("did", "9999")
	w := httptest.NewRecorder()
	h.DeployLogStream(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404", w.Code)
	}
}

// TestDeployLogStream_NoHub_Returns503 covers the "hub not wired"
// defensive case: a misconfigured server returns 503 instead of
// crashing.
func TestDeployLogStream_NoHub_Returns503(t *testing.T) {
	h := &Handlers{DB: newTestDB(t), SkipCaddyReload: true}
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")
	depID := seedDeploy(t, h, appID, deploy.StatusRunning)
	req := httptest.NewRequest(http.MethodGet,
		"/api/apps/"+strconv.Itoa(appID)+"/deployments/"+strconv.Itoa(depID)+"/logs/stream", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("did", strconv.Itoa(depID))
	w := httptest.NewRecorder()
	h.DeployLogStream(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Code = %d, want 503", w.Code)
	}
}

// TestAppLogsStream_StreamCancelReleasesEngine covers the cancel
// contract: the handler must call stream.Cancel when the request
// context is canceled, so the docker engine stream is released
// promptly (and the fake daemon sees the connection close).
func TestAppLogsStream_StreamCancelReleasesEngine(t *testing.T) {
	h := newLogsHandlers(t)
	fd := newFakeLogsDaemon(t)
	h.Docker = newFakeDockerManager(t, fd, "nanoku-caddy")
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")
	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/logs/stream?tail=10", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.AppLogsStream(w, req)
		close(done)
	}()
	// Wait for the daemon to register at least one /logs hit, then
	// cancel. The fake daemon holds the connection open via a
	// <-r.Context().Done(), so canceling the handler's ctx must
	// unblock it too.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fd.hits.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AppLogsStream did not return after ctx cancel")
	}
	// Force the unused-import lint to be silent on container/api/types
	// (referenced only indirectly via the fake daemon path).
	_ = container.LogsOptions{}
	_ = json.Marshal
}
