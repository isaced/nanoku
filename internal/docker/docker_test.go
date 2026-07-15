package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// fakeDaemon is a minimal HTTP mock of the Docker Engine API used by the
// docker package tests. Each test wires the routes it needs; recorded calls
// let tests assert the Manager made the expected requests.
type fakeDaemon struct {
	*httptest.Server

	mu       sync.Mutex
	calls    []fakeCall
	hosts    map[string]bool // container names currently known
	dispatch func(w http.ResponseWriter, r *http.Request, body string)
}

type fakeCall struct {
	method string
	path   string
	body   string
}

func newFakeDaemon() *fakeDaemon {
	fd := &fakeDaemon{hosts: map[string]bool{}}
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
			w.Header().Set("API-Version", "1.40")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	fd.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bytes.Buffer
		_, _ = body.ReadFrom(r.Body)
		bodyStr := body.String()
		// Rewind so the dispatch handler can re-read r.Body.
		r.Body = io.NopCloser(strings.NewReader(bodyStr))
		r.URL.Path = stripVersionPrefix(r.URL.Path)
		fd.mu.Lock()
		fd.calls = append(fd.calls, fakeCall{method: r.Method, path: r.URL.Path, body: bodyStr})
		handler := fd.dispatch
		fd.mu.Unlock()
		if handler != nil {
			handler(w, r, bodyStr)
		}
	}))
	return fd
}

func stripVersionPrefix(p string) string {
	if !strings.HasPrefix(p, "/") {
		return p
	}
	rest := p[1:]
	if !strings.HasPrefix(rest, "v") {
		return p
	}
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return p
	}
	seg := rest[:slash]
	if !(strings.HasPrefix(seg, "v1.") || strings.HasPrefix(seg, "v")) {
		return p
	}
	if len(seg) < 2 || seg[1] < '0' || seg[1] > '9' {
		return p
	}
	return rest[slash:]
}

func (fd *fakeDaemon) callsOf(path string) []fakeCall {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	var out []fakeCall
	for _, c := range fd.calls {
		if c.path == path {
			out = append(out, c)
		}
	}
	return out
}

func newTestManager(t *testing.T, fd *fakeDaemon, cfg Config) *Manager {
	t.Helper()
	cli, err := client.NewClientWithOpts(
		client.WithHost(fd.URL),
		client.WithHTTPClient(fd.Client()),
		client.WithVersion("1.40"),
	)
	if err != nil {
		t.Fatalf("NewClientWithOpts: %v", err)
	}
	return newManagerWithClient(cfg, cli)
}

func TestPing(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	m := newTestManager(t, fd, Config{})
	if err := m.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestEnsureNetwork_CreatesIfMissing(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		t.Logf("hit: %s %s", r.Method, r.URL.Path)
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{})
		case r.URL.Path == "/networks/create" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(network.CreateResponse{ID: "abc123"})
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if err := m.EnsureNetwork(context.Background()); err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	if got := fd.callsOf("/networks/create"); len(got) != 1 {
		t.Errorf("expected 1 network create, got %d", len(got))
	}
}

func TestEnsureNetwork_NoopIfExists(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		default:
			http.Error(w, "should not be called", http.StatusInternalServerError)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if err := m.EnsureNetwork(context.Background()); err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	if got := fd.callsOf("/networks/create"); len(got) != 0 {
		t.Errorf("expected no network create, got %d", len(got))
	}
}

func TestEnsureVolume_CreatesIfMissing(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{})
		case r.URL.Path == "/volumes/create" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(volume.Volume{Name: "nanoku-data"})
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{VolumeName: "nanoku-data"})
	if err := m.EnsureVolume(context.Background()); err != nil {
		t.Fatalf("EnsureVolume: %v", err)
	}
	if got := fd.callsOf("/volumes/create"); len(got) != 1 {
		t.Errorf("expected 1 volume create, got %d", len(got))
	}
}

func TestContainerStatus(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == "/containers/json" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-app"}, State: "running"},
			})
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	got, err := m.ContainerStatus(context.Background(), "nanoku-app")
	if err != nil {
		t.Fatalf("ContainerStatus: %v", err)
	}
	if got != "running" {
		t.Errorf("status = %q, want running", got)
	}
}

func TestContainerStatus_NotFound(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == "/containers/json" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]container.Summary{})
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	got, err := m.ContainerStatus(context.Background(), "missing")
	if err != nil {
		t.Fatalf("ContainerStatus: %v", err)
	}
	if got != "not_found" {
		t.Errorf("status = %q, want not_found", got)
	}
}

func TestStartStopRestartRemove(t *testing.T) {
	cases := []struct {
		name   string
		call   func(*Manager, context.Context, string) error
		method string
		path   string
	}{
		{"start", func(m *Manager, ctx context.Context, n string) error { return m.StartContainer(ctx, n) }, "POST", "/containers/nanoku-app/start"},
		{"stop", func(m *Manager, ctx context.Context, n string) error { return m.StopContainer(ctx, n) }, "POST", "/containers/nanoku-app/stop"},
		{"restart", func(m *Manager, ctx context.Context, n string) error { return m.RestartContainer(ctx, n) }, "POST", "/containers/nanoku-app/restart"},
		{"remove", func(m *Manager, ctx context.Context, n string) error { return m.RemoveContainer(ctx, n) }, "DELETE", "/containers/nanoku-app"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := newFakeDaemon()
			defer fd.Close()
			fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
				if r.URL.Path == tc.path && r.Method == tc.method {
					if tc.method == "DELETE" {
						w.WriteHeader(http.StatusNoContent)
					} else {
						w.WriteHeader(http.StatusOK)
					}
					return
				}
				http.NotFound(w, r)
			}
			m := newTestManager(t, fd, Config{})
			if err := tc.call(m, context.Background(), "nanoku-app"); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if len(fd.callsOf(tc.path)) != 1 {
				t.Errorf("expected 1 call to %s, got %d", tc.path, len(fd.callsOf(tc.path)))
			}
		})
	}
}

func TestContainerLogs_StdCopyDemux(t *testing.T) {
	var payload bytes.Buffer
	stdcopy.NewStdWriter(&payload, stdcopy.Stdout).Write([]byte("OUT\n"))
	stdcopy.NewStdWriter(&payload, stdcopy.Stderr).Write([]byte("ERR\n"))

	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/logs") {
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			_, _ = w.Write(payload.Bytes())
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	out, err := m.ContainerLogs(context.Background(), "nanoku-app", 100)
	if err != nil {
		t.Fatalf("ContainerLogs: %v", err)
	}
	if !strings.Contains(out, "OUT") {
		t.Errorf("missing stdout in: %q", out)
	}
	if !strings.Contains(out, "ERR") {
		t.Errorf("missing stderr in: %q", out)
	}
}

func TestPullImage_ReadsStream(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	pulls := 0
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/images/create") {
			pulls++
			// Simulate the pull progress stream.
			_, _ = io.WriteString(w, `{"status":"Pulling"}`+"\n")
			_, _ = io.WriteString(w, `{"status":"Downloaded"}`+"\n")
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	if err := m.PullImage(context.Background(), "nginx:latest"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	if pulls != 1 {
		t.Errorf("expected 1 image pull, got %d", pulls)
	}
}

func TestCreateAppContainer(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "container-xyz"})
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/containers/container-xyz/start" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	id, name, err := m.CreateAppContainer(context.Background(), "blog", "nginx:latest", 8080, nil, 0, nil)
	if err != nil {
		t.Fatalf("CreateAppContainer: %v", err)
	}
	if id != "container-xyz" {
		t.Errorf("id = %q, want container-xyz", id)
	}
	if name != "nanoku-blog" {
		t.Errorf("name = %q, want nanoku-blog", name)
	}
	if len(fd.callsOf("/containers/create")) != 1 {
		t.Errorf("expected 1 container create")
	}
	if len(fd.callsOf("/containers/container-xyz/start")) != 1 {
		t.Errorf("expected 1 container start")
	}
}

func TestListContainersByNamePrefix(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == "/containers/json" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "1", Names: []string{"/nanoku-app-1"}},
				{ID: "2", Names: []string{"/nanoku-app-2"}},
				{ID: "3", Names: []string{"/other-thing"}},
			})
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	got, err := m.ListContainersByNamePrefix(context.Background(), "nanoku-app")
	if err != nil {
		t.Fatalf("ListContainersByNamePrefix: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d names, want 2: %v", len(got), got)
	}
}

func TestRemoveAppVolumes(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	deleted := []string{}
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{
				Volumes: []*volume.Volume{
					{Name: "nanoku-blog-vol-0"},
					{Name: "nanoku-blog-vol-1"},
					{Name: "other-vol"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/volumes/") && r.Method == http.MethodDelete:
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/volumes/"))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{})
	if err := m.RemoveAppVolumes(context.Background(), "blog"); err != nil {
		t.Fatalf("RemoveAppVolumes: %v", err)
	}
	if len(deleted) != 2 {
		t.Errorf("deleted = %v, want 2 entries", deleted)
	}
	for _, d := range deleted {
		if d == "other-vol" {
			t.Errorf("wrong volume removed: %v", deleted)
		}
	}
}

func TestAllStats_SkipsEmpty(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == "/containers/json" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]container.Summary{})
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	got, err := m.AllStats(context.Background())
	if err != nil {
		t.Fatalf("AllStats: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d stats, want 0", len(got))
	}
}

func TestAllStats_FetchesPerContainer(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	statCalls := map[string]int{}
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-a"}},
				{ID: "id2", Names: []string{"/nanoku-b"}},
			})
		case strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/stats"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/stats")
			statCalls[id]++
			w.Header().Set("Content-Type", "application/json")
			stats := map[string]any{
				"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": uint64(1000)}, "system_cpu_usage": uint64(2000)},
				"precpu_stats": map[string]any{"cpu_usage": map[string]any{"total_usage": uint64(500)}, "system_cpu_usage": uint64(1000)},
				"memory_stats": map[string]any{"usage": uint64(1024), "limit": uint64(2048), "stats": map[string]uint64{"cache": 0}},
				"networks":     map[string]any{"eth0": map[string]any{"rx_bytes": 10, "tx_bytes": 20}},
				"blkio_stats":  map[string]any{"io_service_bytes_recursive": []map[string]any{{"op": "Read", "value": 5}, {"op": "Write", "value": 7}}},
				"pids_stats":   map[string]any{"current": 3},
			}
			_ = json.NewEncoder(w).Encode(stats)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{})
	got, err := m.AllStats(context.Background())
	if err != nil {
		t.Fatalf("AllStats: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d stats, want 2", len(got))
	}
	if statCalls["id1"] != 1 || statCalls["id2"] != 1 {
		t.Errorf("expected one stats call per container, got %v", statCalls)
	}
	for _, s := range got {
		if s.CPUPerc <= 0 {
			t.Errorf("cpu perc should be > 0, got %f", s.CPUPerc)
		}
		if s.MemUsed != 1024 || s.MemLimit != 2048 {
			t.Errorf("mem = used=%d limit=%d, want 1024/2048", s.MemUsed, s.MemLimit)
		}
		if s.MemPerc != 50.0 {
			t.Errorf("mem perc = %f, want 50.0", s.MemPerc)
		}
		if s.NetRxBytes != 10 || s.NetTxBytes != 20 {
			t.Errorf("net = rx=%d tx=%d, want 10/20", s.NetRxBytes, s.NetTxBytes)
		}
		if s.BlockRead != 5 || s.BlockWrite != 7 {
			t.Errorf("blkio = r=%d w=%d, want 5/7", s.BlockRead, s.BlockWrite)
		}
		if s.PIDs != 3 {
			t.Errorf("pids = %d, want 3", s.PIDs)
		}
	}
}

func TestStatsResponseToStats(t *testing.T) {
	v := container.StatsResponse{
		CPUStats: container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: 1000},
			SystemUsage: 2000,
		},
		PreCPUStats: container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: 500},
			SystemUsage: 1000,
		},
		MemoryStats: container.MemoryStats{
			Usage: 1024,
			Limit: 2048,
			Stats: map[string]uint64{"cache": 0},
		},
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: 10, TxBytes: 20},
		},
		BlkioStats: container.BlkioStats{
			IoServiceBytesRecursive: []container.BlkioStatEntry{
				{Op: "Read", Value: 5},
				{Op: "Write", Value: 7},
			},
		},
		PidsStats: container.PidsStats{Current: 3},
	}
	s := statsResponseToStats(v)
	if s.CPUPerc != 50.0 {
		t.Errorf("cpu perc = %f, want 50.0", s.CPUPerc)
	}
	if s.MemUsed != 1024 || s.MemLimit != 2048 || s.MemPerc != 50.0 {
		t.Errorf("mem = %+v", s)
	}
	if s.NetRxBytes != 10 || s.NetTxBytes != 20 {
		t.Errorf("net = %+v", s)
	}
	if s.BlockRead != 5 || s.BlockWrite != 7 {
		t.Errorf("blkio = %+v", s)
	}
	if s.PIDs != 3 {
		t.Errorf("pids = %d", s.PIDs)
	}
}

func TestEnsureCaddyContainer_StartsExisting(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{Volumes: []*volume.Volume{{Name: "nanoku-data"}}})
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-caddy"}, State: "exited"},
			})
		case r.URL.Path == "/containers/nanoku-caddy/start" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{
		ContainerName: "nanoku-caddy",
		NetworkName:   "nanoku-net",
		VolumeName:    "nanoku-data",
	})
	if err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile"); err != nil {
		t.Fatalf("EnsureCaddyContainer: %v", err)
	}
	if len(fd.callsOf("/containers/nanoku-caddy/start")) != 1 {
		t.Errorf("expected caddy container start")
	}
}

func TestEnsureCaddyContainer_SkipsStartWhenRunning(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{Volumes: []*volume.Volume{{Name: "nanoku-data"}}})
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-caddy"}, State: "running"},
			})
		case strings.HasPrefix(r.URL.Path, "/containers/nanoku-caddy/start"):
			t.Errorf("should not start a running container")
			http.Error(w, "no", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{
		ContainerName: "nanoku-caddy",
		NetworkName:   "nanoku-net",
		VolumeName:    "nanoku-data",
	})
	if err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile"); err != nil {
		t.Fatalf("EnsureCaddyContainer: %v", err)
	}
}

// -------- error paths --------

func TestEnsureNetwork_ListFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	err := m.EnsureNetwork(context.Background())
	if err == nil || !strings.Contains(err.Error(), "network list") {
		t.Errorf("err = %v, want wrapped 'network list'", err)
	}
}

func TestEnsureNetwork_CreateFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{})
		case r.URL.Path == "/networks/create" && r.Method == http.MethodPost:
			http.Error(w, "permission denied", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	err := m.EnsureNetwork(context.Background())
	if err == nil || !strings.Contains(err.Error(), "network create") {
		t.Errorf("err = %v, want wrapped 'network create'", err)
	}
}

func TestEnsureVolume_ListFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{VolumeName: "nanoku-data"})
	err := m.EnsureVolume(context.Background())
	if err == nil || !strings.Contains(err.Error(), "volume list") {
		t.Errorf("err = %v, want wrapped 'volume list'", err)
	}
}

func TestEnsureVolume_CreateFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{})
		case r.URL.Path == "/volumes/create" && r.Method == http.MethodPost:
			http.Error(w, "driver fail", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{VolumeName: "nanoku-data"})
	err := m.EnsureVolume(context.Background())
	if err == nil || !strings.Contains(err.Error(), "volume create") {
		t.Errorf("err = %v, want wrapped 'volume create'", err)
	}
}

func TestPullImage_APIFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "pull denied", http.StatusForbidden)
	}
	m := newTestManager(t, fd, Config{})
	err := m.PullImage(context.Background(), "ghcr.io/foo/bar")
	if err == nil || !strings.Contains(err.Error(), "pull") {
		t.Errorf("err = %v, want wrapped 'pull'", err)
	}
}

func TestPullImage_StreamReadFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/images/create") {
			// Hijack and write a malformed response then close. Either
			// the SDK HTTP transport errors out here, or io.Copy reads
			// the partial body and propagates; either way PullImage
			// must surface a non-nil error.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Skip("hijacker not supported")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_, _ = conn.Write([]byte("partial"))
			_ = conn.Close()
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	err := m.PullImage(context.Background(), "nginx:latest")
	if err == nil {
		t.Fatal("expected error from truncated pull stream")
	}
}

func TestContainerLogs_APIFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "no", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.ContainerLogs(context.Background(), "nanoku-app", 100)
	if err == nil || !strings.Contains(err.Error(), "container logs") {
		t.Errorf("err = %v, want wrapped 'container logs'", err)
	}
}

func TestContainerLogs_DemuxFailsOnSystemerrFrame(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/logs") {
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			// stdcopy header (8 bytes): prefix=Systemerr(3), size=11.
			header := []byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0b}
			_, _ = w.Write(header)
			_, _ = w.Write([]byte("daemon boom"))
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.ContainerLogs(context.Background(), "nanoku-app", 100)
	if err == nil || !strings.Contains(err.Error(), "demux") {
		t.Errorf("err = %v, want wrapped 'demux'", err)
	}
}

func TestStartStopRestartRemove_Failures(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		method string
		op     func(*Manager, context.Context, string) error
	}{
		{"start", "/containers/nanoku-app/start", http.MethodPost,
			func(m *Manager, ctx context.Context, n string) error { return m.StartContainer(ctx, n) }},
		{"stop", "/containers/nanoku-app/stop", http.MethodPost,
			func(m *Manager, ctx context.Context, n string) error { return m.StopContainer(ctx, n) }},
		{"restart", "/containers/nanoku-app/restart", http.MethodPost,
			func(m *Manager, ctx context.Context, n string) error { return m.RestartContainer(ctx, n) }},
		{"remove", "/containers/nanoku-app", http.MethodDelete,
			func(m *Manager, ctx context.Context, n string) error { return m.RemoveContainer(ctx, n) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := newFakeDaemon()
			defer fd.Close()
			fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
				http.Error(w, "nope", http.StatusInternalServerError)
			}
			m := newTestManager(t, fd, Config{})
			err := tc.op(m, context.Background(), "nanoku-app")
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "nanoku-app") {
				t.Errorf("err %v should mention container name", err)
			}
		})
	}
}

func TestContainerStatus_ListFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.ContainerStatus(context.Background(), "nanoku-app")
	if err == nil || !strings.Contains(err.Error(), "container list") {
		t.Errorf("err = %v, want wrapped 'container list'", err)
	}
}

func TestContainerStatus_MultipleNamesPerContainer(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == "/containers/json" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/other-name", "/nanoku-app"}, State: "running"},
			})
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	got, err := m.ContainerStatus(context.Background(), "nanoku-app")
	if err != nil {
		t.Fatalf("ContainerStatus: %v", err)
	}
	if got != "running" {
		t.Errorf("status = %q, want running", got)
	}
}

func TestListContainersByNamePrefix_NoMatches(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		_ = json.NewEncoder(w).Encode([]container.Summary{})
	}
	m := newTestManager(t, fd, Config{})
	got, err := m.ListContainersByNamePrefix(context.Background(), "nanoku-missing")
	if err != nil {
		t.Fatalf("ListContainersByNamePrefix: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestListContainersByNamePrefix_ListFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.ListContainersByNamePrefix(context.Background(), "nanoku-x")
	if err == nil {
		t.Error("expected error")
	}
}

func TestRemoveAppVolumes_NoMatchingVolumes(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	deleted := []string{}
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{
				Volumes: []*volume.Volume{
					{Name: "nanoku-other-vol-0"},
					{Name: "unrelated"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/volumes/") && r.Method == http.MethodDelete:
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/volumes/"))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{})
	if err := m.RemoveAppVolumes(context.Background(), "blog"); err != nil {
		t.Fatalf("RemoveAppVolumes: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("nothing should be deleted, got %v", deleted)
	}
}

func TestRemoveAppVolumes_ListFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{})
	err := m.RemoveAppVolumes(context.Background(), "blog")
	if err == nil || !strings.Contains(err.Error(), "volume list") {
		t.Errorf("err = %v, want wrapped 'volume list'", err)
	}
}

func TestRemoveAppVolumes_NonConflictError(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{
				Volumes: []*volume.Volume{{Name: "nanoku-blog-vol-0"}},
			})
		case strings.HasPrefix(r.URL.Path, "/volumes/") && r.Method == http.MethodDelete:
			http.Error(w, "driver gone", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{})
	err := m.RemoveAppVolumes(context.Background(), "blog")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "driver gone") && !strings.Contains(err.Error(), "rm volume") {
		t.Errorf("err = %v, want wrapped", err)
	}
}

func TestAllStats_ListFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.AllStats(context.Background())
	if err == nil || !strings.Contains(err.Error(), "container list") {
		t.Errorf("err = %v, want wrapped 'container list'", err)
	}
}

func TestAllStats_StatsFetchFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-a"}},
			})
		case strings.HasSuffix(r.URL.Path, "/stats"):
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.AllStats(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stats") {
		t.Errorf("err = %v, want wrapped 'stats'", err)
	}
}

func TestStatsByName_ContainerNotFound(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		_ = json.NewEncoder(w).Encode([]container.Summary{})
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.StatsByName(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), "no container named") {
		t.Errorf("err = %v, want 'no container named'", err)
	}
}

func TestStatsByName_StatsFetchFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-app"}},
			})
		case strings.HasSuffix(r.URL.Path, "/stats"):
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.StatsByName(context.Background(), "nanoku-app")
	if err == nil || !strings.Contains(err.Error(), "stats") {
		t.Errorf("err = %v, want wrapped 'stats'", err)
	}
}

func TestStatsOne_DecodeFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasSuffix(r.URL.Path, "/stats") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("not-json"))
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	_, err := m.statsOne(context.Background(), "any")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want wrapped 'decode'", err)
	}
}

func TestCreateAppContainer_NetworkFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	_, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx", 80, nil, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "network list") {
		t.Errorf("err = %v, want wrapped 'network list'", err)
	}
}

func TestCreateAppContainer_CreateFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			http.Error(w, "image not found", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	_, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx", 80, nil, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "create app container") {
		t.Errorf("err = %v, want wrapped 'create app container'", err)
	}
}

func TestCreateAppContainer_StartFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "id123"})
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			http.Error(w, "device full", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	_, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx", 80, nil, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "start app container") {
		t.Errorf("err = %v, want wrapped 'start app container'", err)
	}
}

func TestEnsureCaddyContainer_CreatesNewContainer(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	var created, started, pulled bool
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{Volumes: []*volume.Volume{{Name: "nanoku-data"}}})
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{})
		case strings.HasPrefix(r.URL.Path, "/images/create") && r.Method == http.MethodPost:
			pulled = true
			_, _ = w.Write([]byte(`{"status":"Pulling"}` + "\n"))
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			created = true
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "caddyid"})
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			started = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unhandled: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{
		ContainerName: "nanoku-caddy",
		Image:         "caddy:2",
		NetworkName:   "nanoku-net",
		VolumeName:    "nanoku-data",
	})
	if err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile"); err != nil {
		t.Fatalf("EnsureCaddyContainer: %v", err)
	}
	if !pulled {
		t.Error("expected image pull")
	}
	if !created {
		t.Error("expected container create")
	}
	if !started {
		t.Error("expected container start")
	}
}

// TestEnsureCaddyContainer_ResolvesRelativeCaddyfilePath covers the bug
// where a relative Caddyfile path (the default, "./Caddyfile") makes the
// Docker engine reject the bind mount with "mount path must be absolute".
// The manager must resolve the path to an absolute one before constructing
// the mount, while the in-process caddy.WriteAtomic caller keeps using the
// original string.
func TestEnsureCaddyContainer_ResolvesRelativeCaddyfilePath(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()

	var createBody string
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{Volumes: []*volume.Volume{{Name: "nanoku-data"}}})
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{})
		case strings.HasPrefix(r.URL.Path, "/images/create") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"status":"Pulling"}` + "\n"))
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r.Body)
			createBody = buf.String()
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "caddyid"})
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{
		ContainerName: "nanoku-caddy",
		Image:         "caddy:2",
		NetworkName:   "nanoku-net",
		VolumeName:    "nanoku-data",
	})

	// Run from a temp cwd so "./Caddyfile" doesn't accidentally resolve
	// to something inside the repo, and so the resolved absolute path is
	// something we can match against.
	dir := t.TempDir()
	t.Chdir(dir)

	if err := m.EnsureCaddyContainer(context.Background(), "./Caddyfile"); err != nil {
		t.Fatalf("EnsureCaddyContainer with relative path: %v", err)
	}
	if createBody == "" {
		t.Fatal("expected /containers/create to be called")
	}

	var parsed struct {
		HostConfig struct {
			Mounts []struct {
				Source string `json:"Source"`
				Target string `json:"Target"`
			} `json:"Mounts"`
		} `json:"HostConfig"`
	}
	if err := json.Unmarshal([]byte(createBody), &parsed); err != nil {
		t.Fatalf("decode create body: %v\nbody=%s", err, createBody)
	}
	if len(parsed.HostConfig.Mounts) == 0 {
		t.Fatalf("expected at least one mount in create body, got %s", createBody)
	}
	got := parsed.HostConfig.Mounts[0]
	if got.Target != "/etc/caddy/Caddyfile" {
		t.Errorf("mount target = %q, want /etc/caddy/Caddyfile", got.Target)
	}
	if !filepath.IsAbs(got.Source) {
		t.Errorf("mount source %q is not absolute — relative path was passed through unchanged", got.Source)
	}
	wantSource := filepath.Join(dir, "Caddyfile")
	if got.Source != wantSource {
		t.Errorf("mount source = %q, want %q (relative path must resolve against cwd)", got.Source, wantSource)
	}
}

func TestEnsureCaddyContainer_RestartExited(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{Volumes: []*volume.Volume{{Name: "nanoku-data"}}})
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-caddy"}, State: "exited"},
			})
		case r.URL.Path == "/containers/nanoku-caddy/start" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{
		ContainerName: "nanoku-caddy",
		NetworkName:   "nanoku-net",
		VolumeName:    "nanoku-data",
	})
	if err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile"); err != nil {
		t.Fatalf("EnsureCaddyContainer: %v", err)
	}
}

func TestEnsureCaddyContainer_NetworkFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{ContainerName: "nanoku-caddy", NetworkName: "nanoku-net", VolumeName: "nanoku-data"})
	err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil || !strings.Contains(err.Error(), "network list") {
		t.Errorf("err = %v, want wrapped 'network list'", err)
	}
}

func TestEnsureCaddyContainer_VolumeFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{ContainerName: "nanoku-caddy", NetworkName: "nanoku-net", VolumeName: "nanoku-data"})
	err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil || !strings.Contains(err.Error(), "volume list") {
		t.Errorf("err = %v, want wrapped 'volume list'", err)
	}
}

func TestEnsureCaddyContainer_StartExitedFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{Volumes: []*volume.Volume{{Name: "nanoku-data"}}})
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-caddy"}, State: "exited"},
			})
		case r.URL.Path == "/containers/nanoku-caddy/start" && r.Method == http.MethodPost:
			http.Error(w, "device gone", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{ContainerName: "nanoku-caddy", NetworkName: "nanoku-net", VolumeName: "nanoku-data"})
	err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil || !strings.Contains(err.Error(), "start caddy") {
		t.Errorf("err = %v, want wrapped 'start caddy'", err)
	}
}

func TestEnsureCaddyContainer_PullFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{Volumes: []*volume.Volume{{Name: "nanoku-data"}}})
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{})
		case strings.HasPrefix(r.URL.Path, "/images/create"):
			http.Error(w, "denied", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{ContainerName: "nanoku-caddy", Image: "caddy:2", NetworkName: "nanoku-net", VolumeName: "nanoku-data"})
	err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil || !strings.Contains(err.Error(), "pull") {
		t.Errorf("err = %v, want wrapped 'pull'", err)
	}
}

func TestEnsureCaddyContainer_CreateFails(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/volumes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(volume.ListResponse{Volumes: []*volume.Volume{{Name: "nanoku-data"}}})
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{})
		case strings.HasPrefix(r.URL.Path, "/images/create"):
			_, _ = w.Write([]byte(`{"status":"Pulling"}` + "\n"))
		case r.URL.Path == "/containers/create":
			http.Error(w, "label invalid", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{ContainerName: "nanoku-caddy", Image: "caddy:2", NetworkName: "nanoku-net", VolumeName: "nanoku-data"})
	err := m.EnsureCaddyContainer(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil || !strings.Contains(err.Error(), "create caddy container") {
		t.Errorf("err = %v, want wrapped 'create caddy container'", err)
	}
}

func TestReloadCaddy_NotRunning(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == "/containers/json" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id1", Names: []string{"/nanoku-caddy"}, State: "exited"},
			})
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{ContainerName: "nanoku-caddy"})
	err := m.ReloadCaddy(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("err = %v, want 'exited' status", err)
	}
}

// -------- edge cases & behaviour --------

func TestContainerLogs_TailZero_DefaultsTo100(t *testing.T) {
	gotTail := ""
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		gotTail = r.URL.Query().Get("tail")
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		_, _ = w.Write([]byte{})
	}
	m := newTestManager(t, fd, Config{})
	if _, err := m.ContainerLogs(context.Background(), "x", 0); err != nil {
		t.Fatalf("ContainerLogs: %v", err)
	}
	if gotTail != "100" {
		t.Errorf("tail=%q, want 100", gotTail)
	}
}

func TestContainerLogs_TailNegative_DefaultsTo100(t *testing.T) {
	gotTail := ""
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		gotTail = r.URL.Query().Get("tail")
		w.WriteHeader(http.StatusOK)
	}
	m := newTestManager(t, fd, Config{})
	if _, err := m.ContainerLogs(context.Background(), "x", -5); err != nil {
		t.Fatalf("ContainerLogs: %v", err)
	}
	if gotTail != "100" {
		t.Errorf("tail=%q, want 100", gotTail)
	}
}

func TestContainerLogs_TailTooLarge_ClampsTo5000(t *testing.T) {
	gotTail := ""
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		gotTail = r.URL.Query().Get("tail")
		w.WriteHeader(http.StatusOK)
	}
	m := newTestManager(t, fd, Config{})
	if _, err := m.ContainerLogs(context.Background(), "x", 99999); err != nil {
		t.Fatalf("ContainerLogs: %v", err)
	}
	if gotTail != "5000" {
		t.Errorf("tail=%q, want 5000", gotTail)
	}
}

func TestContainerLogs_EmptyResponse(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		w.WriteHeader(http.StatusOK)
	}
	m := newTestManager(t, fd, Config{})
	out, err := m.ContainerLogs(context.Background(), "x", 100)
	if err != nil {
		t.Fatalf("ContainerLogs: %v", err)
	}
	if out != "" {
		t.Errorf("got %q, want empty", out)
	}
}

func TestCreateAppContainer_WithEnv(t *testing.T) {
	var body string
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r.Body)
			body = buf.String()
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "id1"})
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if _, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx", 80, []string{"FOO=bar", "BAZ=qux"}, 0, nil); err != nil {
		t.Fatalf("CreateAppContainer: %v", err)
	}
	for _, want := range []string{"FOO=bar", "BAZ=qux"} {
		if !strings.Contains(body, want) {
			t.Errorf("create body missing %q: %s", want, body)
		}
	}
}

func TestCreateAppContainer_WithMounts(t *testing.T) {
	var body string
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r.Body)
			body = buf.String()
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "id1"})
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	mounts := []VolumeMount{
		{Type: "volume", Target: "/data"},
		{Type: "bind", Source: "/srv", Target: "/srv", ReadOnly: true},
	}
	if _, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx", 80, nil, 0, mounts); err != nil {
		t.Fatalf("CreateAppContainer: %v", err)
	}
	if !strings.Contains(body, "nanoku-blog-vol-0") {
		t.Errorf("expected auto-named volume, got: %s", body)
	}
	if !strings.Contains(body, `"/srv"`) || !strings.Contains(body, `"Type":"bind"`) {
		t.Errorf("bind mount missing, got: %s", body)
	}
}

func TestCreateAppContainer_WithHostPort(t *testing.T) {
	var body string
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r.Body)
			body = buf.String()
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "id1"})
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if _, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx", 8080, nil, 9090, nil); err != nil {
		t.Fatalf("CreateAppContainer: %v", err)
	}
	if !strings.Contains(body, "9090") || !strings.Contains(body, "8080/tcp") {
		t.Errorf("port mapping missing in body: %s", body)
	}
}

func TestCreateAppContainer_NoHostPort_NoPortMapping(t *testing.T) {
	var body string
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r.Body)
			body = buf.String()
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "id1"})
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if _, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx", 80, nil, 0, nil); err != nil {
		t.Fatalf("CreateAppContainer: %v", err)
	}
	if strings.Contains(body, `"HostPort"`) {
		t.Errorf("host port 0 should not produce HostPort mapping, got: %s", body)
	}
}

func TestCaddyContainerName(t *testing.T) {
	m := &Manager{containerName: "nanoku-caddy"}
	if got := m.CaddyContainerName(); got != "nanoku-caddy" {
		t.Errorf("got %q", got)
	}
}

func TestNetworkName(t *testing.T) {
	m := &Manager{networkName: "nanoku-net"}
	if got := m.NetworkName(); got != "nanoku-net" {
		t.Errorf("got %q", got)
	}
}

func TestClose_NilCloser_NoOp(t *testing.T) {
	m := &Manager{}
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestClose_PropagatesError(t *testing.T) {
	m := &Manager{cliCloser: errCloser{err: errFake}}
	if err := m.Close(); err == nil || err.Error() != "fake close error" {
		t.Errorf("Close err = %v, want fake", err)
	}
}

type errCloser struct{ err error }

func (e errCloser) Close() error { return e.err }

var errFake = &closeErr{}

type closeErr struct{}

func (*closeErr) Error() string { return "fake close error" }

// -------- statsResponseToStats edges --------

func TestStatsResponseToStats_ZeroSysDelta(t *testing.T) {
	v := container.StatsResponse{
		CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 1000}, SystemUsage: 1000},
		PreCPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 1000}, SystemUsage: 1000},
	}
	s := statsResponseToStats(v)
	if s.CPUPerc != 0 {
		t.Errorf("CPUPerc = %f, want 0 when sysDelta==0", s.CPUPerc)
	}
}

func TestStatsResponseToStats_NegativeMemUsed_ClampsToUsage(t *testing.T) {
	v := container.StatsResponse{
		MemoryStats: container.MemoryStats{Usage: 100, Stats: map[string]uint64{"cache": 200}},
	}
	s := statsResponseToStats(v)
	if s.MemUsed != 100 {
		t.Errorf("MemUsed = %d, want 100 (clamped from negative)", s.MemUsed)
	}
}

func TestStatsResponseToStats_ZeroMemLimit_NoPercent(t *testing.T) {
	v := container.StatsResponse{MemoryStats: container.MemoryStats{Usage: 100, Limit: 0}}
	s := statsResponseToStats(v)
	if s.MemPerc != 0 {
		t.Errorf("MemPerc = %f, want 0 when limit is 0", s.MemPerc)
	}
}

func TestStatsResponseToStats_EmptyNetworks(t *testing.T) {
	v := container.StatsResponse{Networks: nil}
	s := statsResponseToStats(v)
	if s.NetRxBytes != 0 || s.NetTxBytes != 0 {
		t.Errorf("net = %+v, want zeros", s)
	}
}

func TestStatsResponseToStats_EmptyBlkio(t *testing.T) {
	v := container.StatsResponse{}
	s := statsResponseToStats(v)
	if s.BlockRead != 0 || s.BlockWrite != 0 {
		t.Errorf("blkio = %+v, want zeros", s)
	}
}

func TestStatsResponseToStats_MultipleNetworksSummed(t *testing.T) {
	v := container.StatsResponse{
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: 100, TxBytes: 50},
			"eth1": {RxBytes: 200, TxBytes: 75},
		},
	}
	s := statsResponseToStats(v)
	if s.NetRxBytes != 300 || s.NetTxBytes != 125 {
		t.Errorf("net = rx=%d tx=%d, want 300/125", s.NetRxBytes, s.NetTxBytes)
	}
}

func TestStatsResponseToStats_OpVariants(t *testing.T) {
	v := container.StatsResponse{
		BlkioStats: container.BlkioStats{
			IoServiceBytesRecursive: []container.BlkioStatEntry{
				{Op: "Read", Value: 10},
				{Op: "Write", Value: 20},
				{Op: "read", Value: 30},
				{Op: "write", Value: 40},
				{Op: "Total", Value: 999},
			},
		},
	}
	s := statsResponseToStats(v)
	if s.BlockRead != 40 || s.BlockWrite != 60 {
		t.Errorf("blkio = r=%d w=%d, want 40/60", s.BlockRead, s.BlockWrite)
	}
}

// AttachComposeProjectToNetwork — covers the deploy-time hook that
// connects a freshly-`compose up`'d stack to nanoku-net so Caddy
// can resolve the project containers by name. The pattern is:
// the manager calls ContainerList filtered by the
// `com.docker.compose.project=<project>` label, then NetworkConnect
// each container that isn't already on the target network.

// Empty project name: the manager rejects the call before talking
// to Docker. A missing-name call would otherwise fall through to
// the ContainerList filter and either match everything in the
// daemon (catastrophic) or nothing (silently broken); the explicit
// error is the safer failure mode.
func TestAttachComposeProjectToNetwork_RejectsEmptyProject(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		// Any Docker call here would be a bug — the manager must
		// short-circuit on the empty project name.
		t.Errorf("unexpected Docker call: %s %s", r.Method, r.URL.Path)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if err := m.AttachComposeProjectToNetwork(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty project name, got nil")
	}
}

// No containers in the project: ComposeList returns an empty array,
// the manager returns nil without calling NetworkConnect. This is
// the no-op success path that protects the call from
// `docker compose up` failures that leave a project label
// dangling.
func TestAttachComposeProjectToNetwork_NoContainers(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == "/containers/json" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]container.Summary{})
			return
		}
		t.Errorf("unexpected Docker call: %s %s", r.Method, r.URL.Path)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if err := m.AttachComposeProjectToNetwork(context.Background(), "nanoku-ddd"); err != nil {
		t.Fatalf("AttachComposeProjectToNetwork: %v", err)
	}
	if got := fd.callsOf("/networks/nanoku-net/connect"); len(got) != 0 {
		t.Errorf("expected 0 network connect calls, got %d", len(got))
	}
}

// Two containers in the project, neither on nanoku-net: the manager
// must call NetworkConnect for each. This is the production
// "first deploy after a fresh stack" path — without these calls,
// Caddy would 502 every request to the stack.
func TestAttachComposeProjectToNetwork_ConnectsAllUnattached(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	connected := map[string]bool{}
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id-web-1234567890ab", Names: []string{"/nanoku-ddd-web-1"}},
				{ID: "id-worker-1234567890", Names: []string{"/nanoku-ddd-worker-1"}},
			})
		case strings.HasPrefix(r.URL.Path, "/networks/") && strings.HasSuffix(r.URL.Path, "/connect") && r.Method == http.MethodPost:
			var body struct {
				Container string `json:"Container"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			connected[body.Container] = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected Docker call: %s %s", r.Method, r.URL.Path)
			http.Error(w, "should not be called", http.StatusInternalServerError)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if err := m.AttachComposeProjectToNetwork(context.Background(), "nanoku-ddd"); err != nil {
		t.Fatalf("AttachComposeProjectToNetwork: %v", err)
	}
	if !connected["id-web-1234567890ab"] {
		t.Errorf("web container was not connected")
	}
	if !connected["id-worker-1234567890"] {
		t.Errorf("worker container was not connected")
	}
}

// One container already on the network (returned by ContainerList
// with the network in its NetworkSettings.Networks map): the
// manager must skip it. A second container still needs the
// connect. This is the "redeploy of a stack the previous run
// already wired" case — idempotency is the whole point of the
// pre-check, otherwise a re-deploy would 403 the
// already-connected container and fail the deploy.
func TestAttachComposeProjectToNetwork_SkipsAlreadyAttached(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	connected := map[string]bool{}
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{
					ID:    "id-already-1234567890",
					Names: []string{"/nanoku-ddd-web-1"},
					NetworkSettings: &container.NetworkSettingsSummary{
						Networks: map[string]*network.EndpointSettings{
							"nanoku-net": {},
						},
					},
				},
				{
					ID:    "id-new-1234567890ab",
					Names: []string{"/nanoku-ddd-worker-1"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/networks/") && strings.HasSuffix(r.URL.Path, "/connect") && r.Method == http.MethodPost:
			var body struct {
				Container string `json:"Container"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			connected[body.Container] = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected Docker call: %s %s", r.Method, r.URL.Path)
			http.Error(w, "should not be called", http.StatusInternalServerError)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if err := m.AttachComposeProjectToNetwork(context.Background(), "nanoku-ddd"); err != nil {
		t.Fatalf("AttachComposeProjectToNetwork: %v", err)
	}
	if connected["id-already-1234567890"] {
		t.Errorf("already-attached container should not be re-connected")
	}
	if !connected["id-new-1234567890ab"] {
		t.Errorf("new container was not connected")
	}
	if got := fd.callsOf("/networks/nanoku-net/connect"); len(got) != 1 {
		t.Errorf("expected 1 connect call (skip + connect), got %d", len(got))
	}
}

// NetworkConnect returns a 5xx: the manager must surface the
// failure as an error naming the offending container so the
// operator can see which service is blocking the attach.
func TestAttachComposeProjectToNetwork_PropagatesConnectError(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/containers/json" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "id-fail-1234567890ab", Names: []string{"/nanoku-ddd-bad-1"}},
			})
		case strings.HasSuffix(r.URL.Path, "/connect"):
			http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	err := m.AttachComposeProjectToNetwork(context.Background(), "nanoku-ddd")
	if err == nil {
		t.Fatal("expected error from connect failure, got nil")
	}
	if !strings.Contains(err.Error(), "nanoku-ddd-bad-1") {
		t.Errorf("error %q should name the offending container", err)
	}
	if !strings.Contains(err.Error(), "nanoku-net") {
		t.Errorf("error %q should name the target network", err)
	}
}