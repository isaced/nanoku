package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/isaced/nanoku/internal/docker"
)

// fakeDockerDeployDaemon is a minimal HTTP fake of the Docker Engine API
// that simulates a successful "create one container" deploy. It returns a
// deterministic ID from POST /containers/create so tests can verify the
// ID the engine gave us is the one we recorded — catching the bug where
// the executor discards the returned ID and stores an empty string.
//
// The fake intentionally supports the smallest set of endpoints the docker
// branch of executeDeploy exercises: ping, network list/create, container
// inspect (returns 404 so removeContainerIfExists is a no-op), image pull,
// container create, container start, and container list.
type fakeDockerDeployDaemon struct {
	*httptest.Server
	createdID     string
	containerName string
}

func newFakeDockerDeployDaemon(t *testing.T, createdID, containerName string) *fakeDockerDeployDaemon {
	t.Helper()
	fd := &fakeDockerDeployDaemon{createdID: createdID, containerName: containerName}
	fd.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The docker client negotiates an API version and prefixes
		// every path with it (e.g. /v1.40/images/create). Strip that
		// off so the dispatch table can match on the canonical path.
		path := stripAPIVersionPrefix(r.URL.Path)
		switch {
		case strings.HasSuffix(path, "/_ping"):
			w.Header().Set("API-Version", "1.40")
			w.WriteHeader(http.StatusOK)
		case path == "/networks" && r.Method == http.MethodGet:
			// EnsureNetwork: return empty so it falls through to create.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]struct{}{})
		case path == "/networks/create" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "fake-net-id"})
		case path == "/containers/create" && r.Method == http.MethodPost:
			// CreateAppContainer: this is the ID the engine "gave us".
			// The bug is that the executor discards it.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: fd.createdID})
		case strings.HasSuffix(path, "/start") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		case path == "/containers/json" && r.Method == http.MethodGet:
			// ListContainersByNamePrefix post-create: report the
			// freshly-created container so the existence check passes.
			// MUST come before the "/containers/.../json" inspect
			// case below — that one would otherwise match
			// "/containers/json" (the list endpoint) and 404 it.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: fd.createdID, Names: []string{"/" + fd.containerName}},
			})
		case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json") && r.Method == http.MethodGet:
			// removeContainerIfExists: no prior container with the
			// desired name → 404 → errdefs.IsNotFound → no-op.
			http.NotFound(w, r)
		case strings.HasPrefix(path, "/images/create") && r.Method == http.MethodPost:
			// PullImage: emit a single progress line so the JSON
			// decoder is satisfied.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"Pulling fs layer"}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fd.Close)
	return fd
}

// stripAPIVersionPrefix removes a leading "/v<digit>..." segment from
// the URL path so the dispatch table can match on the canonical route
// regardless of the negotiated engine version.
func stripAPIVersionPrefix(p string) string {
	if !strings.HasPrefix(p, "/") {
		return p
	}
	rest := p[1:]
	if !strings.HasPrefix(rest, "v") || len(rest) < 2 || rest[1] < '0' || rest[1] > '9' {
		return p
	}
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		return rest[slash:]
	}
	return p
}

func newFakeDockerDeployManager(t *testing.T, fd *fakeDockerDeployDaemon, caddyName string) *docker.Manager {
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

// TestExecuteDeploy_DockerPath_StoresRealDockerID reproduces the bug
// where the docker branch of executeDeploy discards the ID returned by
// CreateAppContainer and stores "" in containers.docker_id.
//
// The fake daemon returns a deterministic ID from POST /containers/create
// so the test can compare what the engine gave us against what landed in
// the DB. With the current code, the assertion fails because the
// executor writes SetDockerID("") regardless of what the engine said.
// Once the executor is fixed to thread the returned ID through to
// SetDockerID, the assertion holds.
//
// The test drives the full happy path: pull → create → start →
// list → record → regenerate, with no registry creds (so WithRegistry
// is a no-op) and SkipCaddyReload (so we don't need a real caddy
// container). Old-container retire is skipped naturally because this
// is a first deploy (oldContainerID = 0).
func TestExecuteDeploy_DockerPath_StoresRealDockerID(t *testing.T) {
	const expectedID = "deadbeef-cafe-f00d-0000-000000000001"
	const appName = "docker-id-app"
	const expectedContainerName = "nanoku-" + appName

	fd := newFakeDockerDeployDaemon(t, expectedID, expectedContainerName)

	d := newTestDB(t)
	a, err := d.App.Create().
		SetName(appName).
		SetImage("nginx:1.27").
		SetPort(80).
		SetDeployMethod("docker").
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}

	h := &Handlers{
		DB:              d,
		DeployLock:      NewDeployLock(),
		Docker:          newFakeDockerDeployManager(t, fd, "nanoku-caddy"),
		CaddyfilePath:   t.TempDir() + "/Caddyfile",
		SkipCaddyReload: true,
		Secret:          newTestSealer(t),
		DeployLogs:      mustStore(t),
	}
	depID := seedRunningDeploy(t, h, a.ID)

	done := make(chan struct{})
	go func() {
		h.executeDeploy(context.Background(), a.ID, depID, "")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("executeDeploy did not return within 3s")
	}

	// The deploy itself must have completed successfully. If we failed
	// here, the assertion below would be testing a different bug (e.g.
	// a missing fake endpoint), not the docker_id one we care about.
	status, msg := readDeployError(t, h, depID)
	if status != "success" {
		t.Fatalf("deploy status = %s, want success (msg=%q). The fake daemon is probably missing a route the executor exercises.", status, msg)
	}

	// The container row must carry the ID the engine returned from
	// /containers/create. The pre-fix executor wrote nil-or-empty
	// here regardless of what the engine said; the fix threads
	// CreateAppContainer's return value through to SetNillableDockerID.
	cont, err := a.QueryCurrentContainer().Only(context.Background())
	if err != nil {
		t.Fatalf("read app's current container: %v", err)
	}
	if cont.DockerID == nil || *cont.DockerID == "" {
		t.Fatalf("Container.DockerID = %v, want %q (the ID returned by POST /containers/create was discarded)", cont.DockerID, expectedID)
	}
	if *cont.DockerID != expectedID {
		t.Errorf("Container.DockerID = %q, want %q", *cont.DockerID, expectedID)
	}
}
