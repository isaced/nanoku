package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

type Manager struct {
	cli           client.APIClient
	cliCloser     io.Closer
	loginBinary   string
	composeBinary string
	containerName string
	image         string
	volumeName    string
	networkName   string
}

type Config struct {
	ContainerName string
	Image         string
	VolumeName    string
	NetworkName   string
}

func NewManager(cfg Config) (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	loginBin, err := lookDocker()
	if err != nil {
		_ = cli.Close()
		return nil, err
	}
	return &Manager{
		cli:           cli,
		cliCloser:     cli,
		loginBinary:   loginBin,
		composeBinary: loginBin,
		containerName: cfg.ContainerName,
		image:         cfg.Image,
		volumeName:    cfg.VolumeName,
		networkName:   cfg.NetworkName,
	}, nil
}

func newManagerWithClient(cfg Config, cli client.APIClient) *Manager {
	return &Manager{
		cli:           cli,
		containerName: cfg.ContainerName,
		image:         cfg.Image,
		volumeName:    cfg.VolumeName,
		networkName:   cfg.NetworkName,
	}
}

// NewManagerWithClient is the public form of newManagerWithClient. It
// exists so tests in other packages (notably internal/api) can wire a
// Manager to a fake / mocked Docker Engine API client without going
// through NewManager (which dials the real socket).
func NewManagerWithClient(cfg Config, cli client.APIClient) *Manager {
	return newManagerWithClient(cfg, cli)
}

func (m *Manager) Close() error {
	if m.cliCloser == nil {
		return nil
	}
	return m.cliCloser.Close()
}

func (m *Manager) Ping(ctx context.Context) error {
	_, err := m.cli.Ping(ctx)
	return err
}

func (m *Manager) CaddyContainerName() string { return m.containerName }
func (m *Manager) NetworkName() string         { return m.networkName }

func (m *Manager) EnsureNetwork(ctx context.Context) error {
	args := filters.NewArgs()
	args.Add("name", m.networkName)
	list, err := m.cli.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		return fmt.Errorf("network list: %w", err)
	}
	if len(list) > 0 {
		return nil
	}
	if _, err := m.cli.NetworkCreate(ctx, m.networkName, network.CreateOptions{
		Labels: map[string]string{"nanoku.managed": "true"},
	}); err != nil {
		return fmt.Errorf("network create: %w", err)
	}
	return nil
}

func (m *Manager) EnsureVolume(ctx context.Context) error {
	args := filters.NewArgs()
	args.Add("name", m.volumeName)
	list, err := m.cli.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		return fmt.Errorf("volume list: %w", err)
	}
	for _, v := range list.Volumes {
		if v != nil && v.Name == m.volumeName {
			return nil
		}
	}
	if _, err := m.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   m.volumeName,
		Labels: map[string]string{"nanoku.managed": "true"},
	}); err != nil {
		return fmt.Errorf("volume create: %w", err)
	}
	return nil
}

func (m *Manager) EnsureCaddyContainer(ctx context.Context, caddyfileHostPath string) error {
	// Docker bind mounts require absolute source paths. Without this, a
	// relative path like "./Caddyfile" works fine for the in-process
	// Caddyfile writer (it resolves against cwd) but the engine rejects
	// the mount with "invalid mount path: mount path must be absolute".
	// Resolve once, here, so the same string is used for both the write
	// site (caddy.WriteAtomic on cfg.CaddyfilePath) and the mount source
	// below — as long as cwd doesn't change between them, both end up
	// pointing at the same file.
	abs, err := filepath.Abs(caddyfileHostPath)
	if err != nil {
		return fmt.Errorf("resolve caddyfile path: %w", err)
	}
	caddyfileHostPath = abs

	if err := m.EnsureNetwork(ctx); err != nil {
		return err
	}
	if err := m.EnsureVolume(ctx); err != nil {
		return err
	}

	status, err := m.CaddyContainerStatus(ctx)
	if err != nil {
		return err
	}
	switch status {
	case "running":
		return nil
	case "exited", "created", "paused", "restarting":
		if err := m.cli.ContainerStart(ctx, m.containerName, container.StartOptions{}); err != nil {
			return fmt.Errorf("start caddy: %w", err)
		}
		return nil
	}

	if _, err := m.cli.ImagePull(ctx, m.image, image.PullOptions{}); err != nil {
		return fmt.Errorf("pull %s: %w", m.image, err)
	}

	port80, _ := nat.NewPort("tcp", "80")
	port443, _ := nat.NewPort("tcp", "443")
	cfg := &container.Config{
		Image:    m.image,
		Cmd:      []string{"caddy", "run", "--watch", "--config", "/etc/caddy/Caddyfile"},
		Labels:   map[string]string{"nanoku.managed": "true", "nanoku.role": "caddy"},
		ExposedPorts: nat.PortSet{port80: struct{}{}, port443: struct{}{}},
	}
	host := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: caddyfileHostPath, Target: "/etc/caddy/Caddyfile", ReadOnly: true},
			{Type: mount.TypeVolume, Source: m.volumeName, Target: "/data"},
			{Type: mount.TypeVolume, Source: m.volumeName, Target: "/config"},
		},
		PortBindings: nat.PortMap{
			port80:  []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "80"}},
			port443: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "443"}},
		},
	}
	networking := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			m.networkName: {},
		},
	}
	if _, err := m.cli.ContainerCreate(ctx, cfg, host, networking, nil, m.containerName); err != nil {
		return fmt.Errorf("create caddy container: %w", err)
	}
	if err := m.cli.ContainerStart(ctx, m.containerName, container.StartOptions{}); err != nil {
		return fmt.Errorf("start caddy: %w", err)
	}
	return nil
}

func (m *Manager) ReloadCaddy(ctx context.Context) error {
	status, err := m.CaddyContainerStatus(ctx)
	if err != nil {
		return err
	}
	if status != "running" {
		return fmt.Errorf("caddy container not running (status=%s)", status)
	}
	// Actually reload. The old implementation only checked container
	// status and relied on caddy's `--watch` flag, which has two real
	// problems: it polls (seconds of delay) and silently swallows
	// config-syntax errors. We exec `caddy reload` in the container,
	// which talks to caddy's admin API on localhost:2019, validates
	// the config, and applies it atomically — surfacing errors as a
	// non-zero exit that we propagate.
	exec, err := m.cli.ContainerExecCreate(ctx, m.containerName, container.ExecOptions{
		Cmd:          []string{"caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--force"},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("caddy reload exec create: %w", err)
	}
	if err := m.cli.ContainerExecStart(ctx, exec.ID, container.ExecStartOptions{}); err != nil {
		return fmt.Errorf("caddy reload exec start: %w", err)
	}
	// Inspect the exit code so syntax errors don't go unnoticed.
	inspect, err := m.cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return fmt.Errorf("caddy reload exec inspect: %w", err)
	}
	if inspect.ExitCode != 0 {
		return fmt.Errorf("caddy reload exited with code %d", inspect.ExitCode)
	}
	return nil
}

func (m *Manager) CaddyContainerStatus(ctx context.Context) (string, error) {
	return m.containerStatus(ctx, m.containerName)
}

func (m *Manager) ContainerStatus(ctx context.Context, name string) (string, error) {
	return m.containerStatus(ctx, name)
}

// ListNanokuContainerStatuses returns a single map of every container whose
// name starts with "nanoku-" (including the caddy container) to its current
// docker state. One ContainerList call replaces the N+1 pattern of calling
// ContainerStatus once per app.
func (m *Manager) ListNanokuContainerStatuses(ctx context.Context) (map[string]string, error) {
	args := filters.NewArgs()
	args.Add("name", "nanoku-")
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("container list: %w", err)
	}
	out := make(map[string]string, len(list))
	for _, c := range list {
		for _, n := range c.Names {
			name := strings.TrimPrefix(n, "/")
			out[name] = c.State
		}
	}
	return out, nil
}

func (m *Manager) containerStatus(ctx context.Context, name string) (string, error) {
	args := filters.NewArgs()
	args.Add("name", name)
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return "", fmt.Errorf("container list: %w", err)
	}
	for _, c := range list {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return c.State, nil
			}
		}
	}
	return "not_found", nil
}

// PullOption mutates a pull call. The zero value is a no-op so existing
// callers (PullImage(ctx, ref)) keep working unchanged.
type PullOption func(*pullConfig)

type pullConfig struct {
	stream io.Writer // nil = discard the JSON progress stream (legacy behavior)
}

// WithPullStream tees the raw ImagePull JSON progress stream (one
// {"status":"…"} object per line) into w. Used by the deploy SSE handler
// to surface pull progress to the operator in real time.
func WithPullStream(w io.Writer) PullOption {
	return func(c *pullConfig) { c.stream = w }
}

func (m *Manager) PullImage(ctx context.Context, imageRef string, opts ...PullOption) error {
	cfg := pullConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	rc, err := m.cli.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", imageRef, err)
	}
	defer rc.Close()
	sink := cfg.stream
	if sink == nil {
		sink = io.Discard
	}
	if _, err := io.Copy(sink, rc); err != nil {
		return fmt.Errorf("pull %s: read stream: %w", imageRef, err)
	}
	return nil
}

func (m *Manager) StartContainer(ctx context.Context, name string) error {
	if err := m.cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	return nil
}

func (m *Manager) StopContainer(ctx context.Context, name string) error {
	if err := m.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: intPtr(10)}); err != nil {
		return fmt.Errorf("stop %s: %w", name, err)
	}
	return nil
}

func (m *Manager) RestartContainer(ctx context.Context, name string) error {
	if err := m.cli.ContainerRestart(ctx, name, container.StopOptions{Timeout: intPtr(10)}); err != nil {
		return fmt.Errorf("restart %s: %w", name, err)
	}
	return nil
}

func (m *Manager) RemoveContainer(ctx context.Context, name string) error {
	if err := m.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("rm %s: %w", name, err)
	}
	return nil
}

func (m *Manager) ContainerLogs(ctx context.Context, name string, tail int) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	if tail > 5000 {
		tail = 5000
	}
	rc, err := m.cli.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return "", fmt.Errorf("container logs %s: %w", name, err)
	}
	defer rc.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, rc); err != nil {
		return "", fmt.Errorf("container logs %s: demux: %w", name, err)
	}
	return stdout.String() + stderr.String(), nil
}

// LogStream is the live tail of a container's logs. Lines is a buffered
// channel of newline-terminated log lines (the stdcopy stdout/stderr
// demuxer guarantees line boundaries; timestamps are kept as the engine
// emits them). Err receives at most one terminal error and is then
// closed. Cancel releases the Docker engine stream — call it on client
// disconnect so we don't keep a follow-stream open for an SSE client that
// navigated away.
//
// The channel buffer is small (64) so the engine can apply backpressure
// when the consumer is slow, which is what we want on a live tail: drop
// at the consumer, not the producer.
type LogStream struct {
	Lines  <-chan string
	Err    <-chan error
	Cancel func()
}

// ContainerLogsStream returns a live tail of the named container's
// stdout+stderr. When follow is false it behaves like the buffered
// ContainerLogs call (drain the channel until EOF, then Err is closed).
// When follow is true the stream stays open and pushes new lines until
// the caller cancels or the engine closes the connection (typically when
// the container exits).
func (m *Manager) ContainerLogsStream(ctx context.Context, name string, tail int, follow bool) (*LogStream, error) {
	if tail < 0 {
		tail = 0
	}
	if tail > 5000 {
		tail = 5000
	}
	rc, err := m.cli.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Follow:     follow,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return nil, fmt.Errorf("container logs %s: %w", name, err)
	}

	// Buffered so the engine reader goroutine never blocks on a slow
	// consumer — the contract is that a busy client may miss lines, not
	// that a slow one backpressures into Docker.
	lines := make(chan string, 64)
	errs := make(chan error, 1)
	// Cancel closes the underlying Docker reader, which unblocks the
	// read goroutine and lets it drain. The sync.Once prevents
	// double-close when the caller races Cancel with the natural EOF.
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			_ = rc.Close()
		})
	}

	go func() {
		defer close(lines)
		defer close(errs)
		defer cancel() // always release the engine stream
		// stdcopy.StdCopy demuxes the engine's 8-byte-framed stream and
		// writes one line at a time to the chosen sink. We hand it a
		// thin writer that splits on '\n' and forwards each line to
		// the channel, so the SSE handler can flush line-by-line. We
		// don't distinguish stdout vs stderr in the output — the
		// channel sees them in the same order the engine emitted them.
		mw := lineWriter{w: lines}
		if _, copyErr := stdcopy.StdCopy(&mw, &lineWriter{w: lines}, rc); copyErr != nil {
			errs <- fmt.Errorf("container logs %s: demux: %w", name, copyErr)
			return
		}
	}()

	return &LogStream{
		Lines:  lines,
		Err:    errs,
		Cancel: cancel,
	}, nil
}

// lineWriter splits the bytes StdCopy hands it on '\n' and forwards each
// line (including the trailing newline) to the channel. It blocks on a
// full channel so the engine reader applies backpressure to Docker when
// the consumer is slow. Partial lines (no terminating '\n') are buffered
// and emitted on the next write, so a single frame split across two
// engine reads still reassembles into one consumer-visible line.
type lineWriter struct {
	w   chan<- string
	buf []byte
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		idx := bytes.IndexByte(l.buf, '\n')
		if idx < 0 {
			break
		}
		// The newline is part of the line for downstream consumers
		// (the SSE handler writes it as-is, the frontend renders it as
		// whitespace), so include it.
		line := string(l.buf[:idx+1])
		l.w <- line
		// Shift the buffer: copy the tail over the consumed prefix.
		copy(l.buf, l.buf[idx+1:])
		l.buf = l.buf[:len(l.buf)-(idx+1)]
	}
	return len(p), nil
}

func (m *Manager) ListContainersByNamePrefix(ctx context.Context, prefix string) ([]string, error) {
	args := filters.NewArgs()
	args.Add("name", prefix)
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, c := range list {
		for _, n := range c.Names {
			n = strings.TrimPrefix(n, "/")
			if strings.HasPrefix(n, prefix) {
				names = append(names, n)
			}
		}
	}
	return names, nil
}

func (m *Manager) CreateAppContainer(ctx context.Context, namePrefix, imageRef string, port int, env []string, hostPort int, mounts []VolumeMount) (dockerID string, containerName string, err error) {
	if err := m.EnsureNetwork(ctx); err != nil {
		return "", "", err
	}
	containerName = "nanoku-" + namePrefix

	var envFilePath string
	var envCleanup func()
	if len(env) > 0 {
		envFilePath, envCleanup, err = WriteEnvFile(env)
		if err != nil {
			return "", "", err
		}
		defer envCleanup()
	}

	cfg := &container.Config{
		Image:  imageRef,
		Labels: map[string]string{"nanoku.managed": "true", "nanoku.role": "app", "nanoku.app": namePrefix},
	}
	if port > 0 {
		p, _ := nat.NewPort("tcp", strconv.Itoa(port))
		cfg.ExposedPorts = nat.PortSet{p: struct{}{}}
	}
	if envFilePath != "" {
		b, rerr := os.ReadFile(envFilePath)
		if rerr != nil {
			return "", "", fmt.Errorf("read env file: %w", rerr)
		}
		for _, line := range bytes.Split(b, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			cfg.Env = append(cfg.Env, string(line))
		}
	}

	host := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}
	if hostPort > 0 && port > 0 {
		containerPort, _ := nat.NewPort("tcp", strconv.Itoa(port))
		host.PortBindings = nat.PortMap{
			containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
		}
	}
	for i, mt := range mounts {
		source := mt.Source
		if mt.Type == "volume" && source == "" {
			source = AutoAppVolumeName(namePrefix, i)
		}
		host.Mounts = append(host.Mounts, mount.Mount{
			Type:     mountType(mt.Type),
			Source:   source,
			Target:   mt.Target,
			ReadOnly: mt.ReadOnly,
		})
	}
	networking := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			m.networkName: {},
		},
	}

	// Clean up any leftover container with this name before we
	// create the new one. A previous deploy that crashed between
	// ContainerCreate and ContainerStart, or that errored before
	// the retire step, leaves a stub in the daemon with the
	// desired name; the next deploy's create call would otherwise
	// fail with "name already in use". Idempotent: no-op if the
	// name is free.
	if err := m.removeContainerIfExists(ctx, containerName); err != nil {
		return "", "", err
	}
	createResp, err := m.cli.ContainerCreate(ctx, cfg, host, networking, nil, containerName)
	if err != nil {
		return "", "", fmt.Errorf("create app container: %w", err)
	}
	if err := m.cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return "", "", fmt.Errorf("start app container: %w", err)
	}
	return createResp.ID, containerName, nil
}

// removeContainerIfExists force-removes a container with the given
// name if it exists, regardless of its current state (running, exited,
// created, paused, …). It is the recovery path for "name already in
// use" — a previous deploy that created the container but failed to
// start it (or a crash that aborted the retire step) leaves a stub
// entry in the docker daemon with the desired name, and the next
// deploy must evict it before the create call can succeed.
//
// The function swallows "not found" so it's safe to call
// unconditionally. Other errors are returned so the caller can
// surface them — a permission error here is real and should not be
// masked.
func (m *Manager) removeContainerIfExists(ctx context.Context, name string) error {
	insp, err := m.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	// Prefer a graceful stop (so processes can clean up) but
	// force-remove the container record either way. Force=true
	// also tears down a running container, so we don't have to
	// reason about state.
	_ = m.cli.ContainerStop(ctx, insp.ID, container.StopOptions{Timeout: intPtr(5)})
	if err := m.cli.ContainerRemove(ctx, insp.ID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove stale %s: %w", name, err)
	}
	return nil
}

func (m *Manager) RemoveAppVolumes(ctx context.Context, appName string) error {
	args := filters.NewArgs()
	args.Add("label", "nanoku.managed=true")
	list, err := m.cli.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		return fmt.Errorf("volume list: %w", err)
	}
	prefix := fmt.Sprintf("nanoku-%s-vol-", appName)
	var firstErr error
	for _, v := range list.Volumes {
		if v == nil {
			continue
		}
		if !strings.HasPrefix(v.Name, prefix) {
			continue
		}
		if err := m.cli.VolumeRemove(ctx, v.Name, false); err != nil && firstErr == nil {
			if !errdefs.IsConflict(err) {
				firstErr = fmt.Errorf("rm volume %s: %w", v.Name, err)
			}
		}
	}
	return firstErr
}

type ContainerStats struct {
	Name       string  `json:"name"`
	CPUPerc    float64 `json:"cpuPerc"`
	MemUsed    int64   `json:"memUsedBytes"`
	MemLimit   int64   `json:"memLimitBytes"`
	MemPerc    float64 `json:"memPerc"`
	NetRxBytes int64   `json:"netRxBytes"`
	NetTxBytes int64   `json:"netTxBytes"`
	BlockRead  int64   `json:"blockReadBytes"`
	BlockWrite int64   `json:"blockWriteBytes"`
	PIDs       int     `json:"pids"`
}

// AllStats returns a snapshot for every container whose name starts with "nanoku-".
func (m *Manager) AllStats(ctx context.Context) ([]ContainerStats, error) {
	args := filters.NewArgs()
	args.Add("name", "nanoku-")
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("container list: %w", err)
	}
	if len(list) == 0 {
		return nil, nil
	}
	out := make([]ContainerStats, 0, len(list))
	for _, c := range list {
		cs, err := m.statsOne(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		cs.Name = name
		out = append(out, *cs)
	}
	return out, nil
}

func (m *Manager) StatsByName(ctx context.Context, name string) (*ContainerStats, error) {
	id, err := m.containerIDByName(ctx, name)
	if err != nil {
		return nil, err
	}
	cs, err := m.statsOne(ctx, id)
	if err != nil {
		return nil, err
	}
	cs.Name = name
	return cs, nil
}

func (m *Manager) containerIDByName(ctx context.Context, name string) (string, error) {
	args := filters.NewArgs()
	args.Add("name", name)
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return "", fmt.Errorf("container list: %w", err)
	}
	for _, c := range list {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return c.ID, nil
			}
		}
	}
	return "", errors.New("no container named " + name)
}

func (m *Manager) statsOne(ctx context.Context, id string) (*ContainerStats, error) {
	resp, err := m.cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("stats %s: %w", id, err)
	}
	defer resp.Body.Close()
	var v container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, fmt.Errorf("stats %s: decode: %w", id, err)
	}
	return statsResponseToStats(v), nil
}

func statsResponseToStats(v container.StatsResponse) *ContainerStats {
	cs := &ContainerStats{}
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage - v.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(v.CPUStats.SystemUsage - v.PreCPUStats.SystemUsage)
	if sysDelta > 0 && cpuDelta > 0 {
		cs.CPUPerc = (cpuDelta / sysDelta) * 100.0
	}
	cs.MemUsed = int64(v.MemoryStats.Usage - v.MemoryStats.Stats["cache"])
	if cs.MemUsed < 0 {
		cs.MemUsed = int64(v.MemoryStats.Usage)
	}
	cs.MemLimit = int64(v.MemoryStats.Limit)
	if cs.MemLimit > 0 {
		cs.MemPerc = float64(cs.MemUsed) / float64(cs.MemLimit) * 100.0
	}
	for _, n := range v.Networks {
		cs.NetRxBytes += int64(n.RxBytes)
		cs.NetTxBytes += int64(n.TxBytes)
	}
	for _, b := range v.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(b.Op) {
		case "read", "read ":
			cs.BlockRead += int64(b.Value)
		case "write", "write ":
			cs.BlockWrite += int64(b.Value)
		}
	}
	cs.PIDs = int(v.PidsStats.Current)
	return cs
}

func mountType(t string) mount.Type {
	switch strings.ToLower(t) {
	case "bind":
		return mount.TypeBind
	case "volume":
		return mount.TypeVolume
	case "tmpfs":
		return mount.TypeTmpfs
	}
	return mount.Type(t)
}

func intPtr(i int) *int { return &i }