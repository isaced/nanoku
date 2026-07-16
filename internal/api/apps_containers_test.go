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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/isaced/nanoku/internal/docker"
)

// fakeContainersDaemon is a fake Docker Engine that also responds to
// /containers/json (ContainerList) with a fixed set of containers,
// in addition to the /logs and /_ping endpoints from fakeLogsDaemon.
// It records the container names requested via /logs so tests can
// verify the ?container= query param was honored.
type fakeContainersDaemon struct {
	*fakeLogsDaemon
	listContainers []container.Summary
	logRequests    atomic.Int32
	lastLogPath    atomic.Value // string
}

func newFakeContainersDaemon(t *testing.T) *fakeContainersDaemon {
	t.Helper()
	fd := newFakeLogsDaemon(t)
	fcd := &fakeContainersDaemon{fakeLogsDaemon: fd}
	// Replace the original handler with one that also handles
	// /containers/json and records the container name from /logs.
	// Crucially, for /logs we only block (hold the connection open)
	// when follow=1 is present -- the non-follow ContainerLogs call
	// expects the response to end after the payload, and the original
	// fakeLogsDaemon always blocks, which would hang AppLogs tests.
	fd.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.40")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(fcd.listContainers)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			fcd.logRequests.Add(1)
			fcd.lastLogPath.Store(r.URL.Path)
			fd.hits.Add(1)
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			var p bytes.Buffer
			stdcopy.NewStdWriter(&p, stdcopy.Stdout).Write([]byte("hello\nworld\n"))
			_, _ = w.Write(p.Bytes())
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Only hold the connection open for follow-streams;
			// non-follow requests (ContainerLogs one-shot) should
			// return immediately after the payload.
			if r.URL.Query().Get("follow") == "1" {
				<-r.Context().Done()
			}
		default:
			http.NotFound(w, r)
		}
	})
	return fcd
}

// TestAppLogs_WithContainerParam_UsesSpecifiedContainer verifies that
// passing ?container=<name> makes the handler fetch logs for that
// container instead of the app's current_container. We verify by
// checking the container name in the Docker API request path.
func TestAppLogs_WithContainerParam_UsesSpecifiedContainer(t *testing.T) {
	h := newLogsHandlers(t)
	fcd := newFakeContainersDaemon(t)
	h.Docker = newFakeDockerManagerWithClient(t, fcd, "nanoku-caddy")
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")

	// Request logs for a specific container that is NOT the app's
	// current_container (which is "nanoku-blog").
	req := httptest.NewRequest(http.MethodGet,
		"/api/apps/"+strconv.Itoa(appID)+"/logs?tail=10&container=nanoku-blog-web-1", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	w := httptest.NewRecorder()
	h.AppLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	// The Docker API path should contain the requested container name.
	got := fcd.lastLogPath.Load().(string)
	if !strings.Contains(got, "nanoku-blog-web-1") {
		t.Errorf("Docker API path = %q, want it to contain the requested container name %q", got, "nanoku-blog-web-1")
	}
}

// TestAppLogs_WithoutContainerParam_FallsBackToCurrentContainer
// verifies the backward-compatible path: no ?container= param means
// the handler uses current_container.
func TestAppLogs_WithoutContainerParam_FallsBackToCurrentContainer(t *testing.T) {
	h := newLogsHandlers(t)
	fcd := newFakeContainersDaemon(t)
	h.Docker = newFakeDockerManagerWithClient(t, fcd, "nanoku-caddy")
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")

	req := httptest.NewRequest(http.MethodGet,
		"/api/apps/"+strconv.Itoa(appID)+"/logs?tail=10", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	w := httptest.NewRecorder()
	h.AppLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	got := fcd.lastLogPath.Load().(string)
	// current_container name is "nanoku-blog" (from seedAppWithContainer).
	if !strings.Contains(got, "nanoku-blog") {
		t.Errorf("Docker API path = %q, want it to contain the current container name %q", got, "nanoku-blog")
	}
	// Make sure we didn't accidentally match a longer name like nanoku-blog-web-1.
	if strings.Contains(got, "nanoku-blog-") {
		t.Errorf("Docker API path = %q, should be the bare current_container name, not a compose service", got)
	}
}

// TestAppContainers_ComposeMode_ReturnsAllContainers verifies the
// compose container listing endpoint returns containers filtered by
// the compose project label.
func TestAppContainers_ComposeMode_ReturnsAllContainers(t *testing.T) {
	h := newLogsHandlers(t)
	fcd := newFakeContainersDaemon(t)
	fcd.listContainers = []container.Summary{
		{Names: []string{"/nanoku-myapp-web-1"}, Image: "nginx:1.27", State: "running"},
		{Names: []string{"/nanoku-myapp-db-1"}, Image: "postgres:16", State: "running"},
		{Names: []string{"/nanoku-myapp-cache-1"}, Image: "redis:7", State: "exited"},
	}
	h.Docker = newFakeDockerManagerWithClient(t, fcd, "nanoku-caddy")

	// Create a compose-mode app (no container row needed - compose
	// containers are listed live from Docker, not the DB).
	ctx := context.Background()
	a, err := h.DB.App.Create().
		SetName("myapp").
		SetImage("nginx:1.27").
		SetPort(80).
		SetDeployMethod("compose").
		Save(ctx)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(a.ID)+"/containers", nil)
	req.SetPathValue("id", strconv.Itoa(a.ID))
	w := httptest.NewRecorder()
	h.AppContainers(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	var got []docker.ContainerInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, w.Body.String())
	}
	if len(got) != 3 {
		t.Fatalf("got %d containers, want 3", len(got))
	}
	// Verify names have the leading "/" stripped.
	wantNames := map[string]bool{
		"nanoku-myapp-web-1":   false,
		"nanoku-myapp-db-1":    false,
		"nanoku-myapp-cache-1": false,
	}
	for _, c := range got {
		if _, ok := wantNames[c.Name]; ok {
			wantNames[c.Name] = true
		} else {
			t.Errorf("unexpected container name %q", c.Name)
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("missing container %q in response", name)
		}
	}
}

// TestAppContainers_DockerMode_ReturnsSingleContainer verifies the
// docker-mode path returns exactly the current_container.
func TestAppContainers_DockerMode_ReturnsSingleContainer(t *testing.T) {
	h := newLogsHandlers(t)
	fcd := newFakeContainersDaemon(t)
	h.Docker = newFakeDockerManagerWithClient(t, fcd, "nanoku-caddy")
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	w := httptest.NewRecorder()
	h.AppContainers(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	var got []docker.ContainerInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, w.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("got %d containers, want 1", len(got))
	}
	if got[0].Name != "nanoku-blog" {
		t.Errorf("container name = %q, want %q", got[0].Name, "nanoku-blog")
	}
}

// TestAppContainers_DockerMode_NoContainer_ReturnsEmptyList verifies
// that a docker-mode app with no current_container returns an empty
// list (not an error), so the frontend can show "no container".
func TestAppContainers_DockerMode_NoContainer_ReturnsEmptyList(t *testing.T) {
	h := newLogsHandlers(t)
	fcd := newFakeContainersDaemon(t)
	h.Docker = newFakeDockerManagerWithClient(t, fcd, "nanoku-caddy")

	ctx := context.Background()
	a, err := h.DB.App.Create().
		SetName("ghost").
		SetImage("nginx:1.27").
		SetPort(80).
		SetDeployMethod("docker").
		Save(ctx)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(a.ID)+"/containers", nil)
	req.SetPathValue("id", strconv.Itoa(a.ID))
	w := httptest.NewRecorder()
	h.AppContainers(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	var got []docker.ContainerInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, w.Body.String())
	}
	if len(got) != 0 {
		t.Fatalf("got %d containers, want 0", len(got))
	}
}

// TestAppContainers_AppNotFound_Returns404 documents the URL contract.
func TestAppContainers_AppNotFound_Returns404(t *testing.T) {
	h := newLogsHandlers(t)
	fcd := newFakeContainersDaemon(t)
	h.Docker = newFakeDockerManagerWithClient(t, fcd, "nanoku-caddy")

	req := httptest.NewRequest(http.MethodGet, "/api/apps/9999/containers", nil)
	req.SetPathValue("id", "9999")
	w := httptest.NewRecorder()
	h.AppContainers(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404; body=%q", w.Code, w.Body.String())
	}
}

// TestAppContainers_DockerUnavailable_Returns503 verifies the guard
// clause fires when Docker is nil.
func TestAppContainers_DockerUnavailable_Returns503(t *testing.T) {
	h := &Handlers{DB: newTestDB(t)}
	appID := seedAppWithContainer(t, h, "blog", "nginx:1.27")

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	w := httptest.NewRecorder()
	h.AppContainers(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Code = %d, want 503; body=%q", w.Code, w.Body.String())
	}
}

// newFakeDockerManagerWithClient is like newFakeDockerManager but accepts
// the fakeContainersDaemon (which wraps fakeLogsDaemon with /containers/json
// support and /logs path recording).
func newFakeDockerManagerWithClient(t *testing.T, fcd *fakeContainersDaemon, caddyName string) *docker.Manager {
	t.Helper()
	cli, err := client.NewClientWithOpts(
		client.WithHost(fcd.URL),
		client.WithHTTPClient(fcd.Client()),
		client.WithVersion("1.40"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return docker.NewManagerWithClient(docker.Config{ContainerName: caddyName}, cli)
}
