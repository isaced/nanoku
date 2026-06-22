package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
		r.URL.Path = stripVersionPrefix(r.URL.Path)
		fd.mu.Lock()
		fd.calls = append(fd.calls, fakeCall{method: r.Method, path: r.URL.Path, body: body.String()})
		handler := fd.dispatch
		fd.mu.Unlock()
		if handler != nil {
			handler(w, r, body.String())
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