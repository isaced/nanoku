package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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
	return nil
}

func (m *Manager) CaddyContainerStatus(ctx context.Context) (string, error) {
	return m.containerStatus(ctx, m.containerName)
}

func (m *Manager) ContainerStatus(ctx context.Context, name string) (string, error) {
	return m.containerStatus(ctx, name)
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

func (m *Manager) PullImage(ctx context.Context, imageRef string) error {
	rc, err := m.cli.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", imageRef, err)
	}
	defer rc.Close()
	if _, err := io.Copy(io.Discard, rc); err != nil {
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

	createResp, err := m.cli.ContainerCreate(ctx, cfg, host, networking, nil, containerName)
	if err != nil {
		return "", "", fmt.Errorf("create app container: %w", err)
	}
	if err := m.cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return "", "", fmt.Errorf("start app container: %w", err)
	}
	return createResp.ID, containerName, nil
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