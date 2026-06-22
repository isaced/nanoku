package docker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Manager struct {
	binary        string
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
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("docker CLI not found in PATH: %w", err)
	}
	return &Manager{
		binary:        bin,
		containerName: cfg.ContainerName,
		image:         cfg.Image,
		volumeName:    cfg.VolumeName,
		networkName:   cfg.NetworkName,
	}, nil
}

func (m *Manager) Ping(ctx context.Context) error {
	_, err := m.run(ctx, "info", "--format", "{{.ServerVersion}}")
	return err
}

func (m *Manager) EnsureNetwork(ctx context.Context) error {
	out, err := m.run(ctx, "network", "ls", "--filter", "name="+m.networkName, "--format", "{{.Name}}")
	if err != nil {
		return fmt.Errorf("network ls: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return nil
	}
	if _, err := m.run(ctx, "network", "create", "--label", "nanoku.managed=true", m.networkName); err != nil {
		return fmt.Errorf("network create: %w", err)
	}
	return nil
}

func (m *Manager) EnsureVolume(ctx context.Context) error {
	out, err := m.run(ctx, "volume", "ls", "--filter", "name="+m.volumeName, "--format", "{{.Name}}")
	if err != nil {
		return fmt.Errorf("volume ls: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return nil
	}
	if _, err := m.run(ctx, "volume", "create", "--label", "nanoku.managed=true", m.volumeName); err != nil {
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
		if _, err := m.run(ctx, "container", "start", m.containerName); err != nil {
			return fmt.Errorf("start caddy: %w", err)
		}
		return nil
	}

	if _, err := m.run(ctx, "image", "pull", m.image); err != nil {
		return fmt.Errorf("pull %s: %w", m.image, err)
	}

	args := []string{
		"run", "-d",
		"--name", m.containerName,
		"--restart", "unless-stopped",
		"--network", m.networkName,
		"--label", "nanoku.managed=true",
		"--label", "nanoku.role=caddy",
		"--mount", "type=bind,source=" + caddyfileHostPath + ",target=/etc/caddy/Caddyfile,readonly",
		"--mount", "type=volume,source="+m.volumeName+",target=/data",
		"--mount", "type=volume,source="+m.volumeName+",target=/config",
		"-p", "80:80",
		"-p", "443:443",
		m.image,
		"caddy", "run", "--watch", "--config", "/etc/caddy/Caddyfile",
	}
	if _, err := m.run(ctx, args...); err != nil {
		return fmt.Errorf("run caddy: %w", err)
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

func (m *Manager) CaddyContainerName() string {
	return m.containerName
}

func (m *Manager) NetworkName() string { return m.networkName }

// PullImage pulls a docker image into the local daemon.
func (m *Manager) PullImage(ctx context.Context, image string) error {
	if _, err := m.run(ctx, "image", "pull", image); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

// Login authenticates to a docker registry. stdout is suppressed; password
// is passed via stdin to keep it out of the process list / logs.
func (m *Manager) Login(ctx context.Context, registry, username, password string) error {
	if username == "" {
		return fmt.Errorf("registry_username is required for login")
	}
	args := []string{"login"}
	if registry != "" {
		args = append(args, registry)
	}
	args = append(args, "-u", username, "--password-stdin")
	cmd := exec.CommandContext(ctx, m.binary, args...)
	cmd.Stdin = strings.NewReader(password)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Logout removes credentials for a registry. Best-effort; ignores errors.
func (m *Manager) Logout(ctx context.Context, registry string) error {
	args := []string{"logout"}
	if registry != "" {
		args = append(args, registry)
	}
	_, err := m.run(ctx, args...)
	return err
}

// WithRegistry wraps fn with a docker login/logout if creds are provided.
// registry="" with creds means "the default registry" (docker hub via
// index.docker.io).
func (m *Manager) WithRegistry(ctx context.Context, registry, user, pass string, fn func() error) error {
	if user != "" {
		if err := m.Login(ctx, registry, user, pass); err != nil {
			return err
		}
		if registry != "" {
			defer func() { _ = m.Logout(ctx, registry) }()
		}
	}
	return fn()
}

// composeArgs returns the standard "compose subcmd" argv starting at "compose".
// filePath="" means: use the file in the current working dir.
func composeArgs(project, filePath string, subcmd string, extra ...string) []string {
	args := []string{"compose"}
	if project != "" {
		args = append(args, "-p", project)
	}
	if filePath != "" {
		args = append(args, "-f", filePath)
	}
	args = append(args, subcmd)
	args = append(args, extra...)
	return args
}

// ComposeUp brings up the compose stack (creates containers, networks,
// pulls missing images if --pull is included). `pull` should be true
// when you want to refresh images from the registry.
func (m *Manager) ComposeUp(ctx context.Context, project, filePath string, pull bool) error {
	extra := []string{"-d"}
	if pull {
		extra = append([]string{"--pull", "always"}, extra...)
	}
	out, err := m.run(ctx, composeArgs(project, filePath, "up", extra...)...)
	if err != nil {
		return fmt.Errorf("compose up: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// ComposeStop / Start / Restart / Down wrap their compose subcommands.
func (m *Manager) ComposeStop(ctx context.Context, project, filePath string) error {
	out, err := m.run(ctx, composeArgs(project, filePath, "stop")...)
	if err != nil {
		return fmt.Errorf("compose stop: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}
func (m *Manager) ComposeStart(ctx context.Context, project, filePath string) error {
	out, err := m.run(ctx, composeArgs(project, filePath, "start")...)
	if err != nil {
		return fmt.Errorf("compose start: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}
func (m *Manager) ComposeRestart(ctx context.Context, project, filePath string) error {
	out, err := m.run(ctx, composeArgs(project, filePath, "restart")...)
	if err != nil {
		return fmt.Errorf("compose restart: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}
func (m *Manager) ComposeDown(ctx context.Context, project, filePath string) error {
	out, err := m.run(ctx, composeArgs(project, filePath, "down")...)
	if err != nil {
		return fmt.Errorf("compose down: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// ComposePSNames returns the names of all containers currently in the project.
// Returns (nil, nil) if no containers are running (docker compose ps exits 0
// with empty output).
func (m *Manager) ComposePSNames(ctx context.Context, project, filePath string) ([]string, error) {
	out, err := m.run(ctx, composeArgs(project, filePath, "ps", "--format", "{{.Name}}")...)
	if err != nil {
		return nil, fmt.Errorf("compose ps: %w: %s", err, strings.TrimSpace(out))
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// ListContainersByNamePrefix returns the names of containers whose name
// starts with the given prefix (uses `docker ps --filter name=<prefix>`).
// Used to discover the random-suffix name nanoku just generated for a
// freshly-deployed app.
func (m *Manager) ListContainersByNamePrefix(ctx context.Context, prefix string) ([]string, error) {
	out, err := m.run(ctx, "ps",
		"--all",
		"--filter", "name="+prefix,
		"--format", "{{.Names}}",
	)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// CreateAppContainer runs a new container for an app and returns its docker ID and generated name.
// namePrefix: short app slug (e.g. "myapp"); port: container-internal port; env: key=value pairs.
// hostPort: 0 = no host port mapping (default — proxy via caddy network).
func (m *Manager) CreateAppContainer(ctx context.Context, namePrefix, image string, port int, env []string, hostPort int) (dockerID string, containerName string, err error) {
	if err := m.EnsureNetwork(ctx); err != nil {
		return "", "", err
	}
	suffix, err := randHex(4)
	if err != nil {
		return "", "", err
	}
	containerName = fmt.Sprintf("nanoku-%s-%s", namePrefix, suffix)

	args := []string{
		"run", "-d",
		"--name", containerName,
		"--restart", "unless-stopped",
		"--network", m.networkName,
		"--label", "nanoku.managed=true",
		"--label", "nanoku.role=app",
		"--label", "nanoku.app=" + namePrefix,
	}
	if port > 0 {
		args = append(args, "--expose", strconv.Itoa(port))
	}
	if hostPort > 0 {
		args = append(args, "-p", fmt.Sprintf("%d:%d", hostPort, port))
	}
	for _, kv := range env {
		args = append(args, "-e", kv)
	}
	args = append(args, image)

	out, err := m.run(ctx, args...)
	if err != nil {
		return "", "", fmt.Errorf("run app container: %w", err)
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", "", fmt.Errorf("docker run returned empty id")
	}
	return id, containerName, nil
}

func (m *Manager) StartContainer(ctx context.Context, name string) error {
	if _, err := m.run(ctx, "container", "start", name); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	return nil
}

func (m *Manager) StopContainer(ctx context.Context, name string) error {
	if _, err := m.run(ctx, "container", "stop", "--time", "10", name); err != nil {
		return fmt.Errorf("stop %s: %w", name, err)
	}
	return nil
}

func (m *Manager) RestartContainer(ctx context.Context, name string) error {
	if _, err := m.run(ctx, "container", "restart", "--time", "10", name); err != nil {
		return fmt.Errorf("restart %s: %w", name, err)
	}
	return nil
}

// RemoveContainer force-removes a container. Stops first if running.
func (m *Manager) RemoveContainer(ctx context.Context, name string) error {
	if _, err := m.run(ctx, "container", "rm", "-f", name); err != nil {
		return fmt.Errorf("rm %s: %w", name, err)
	}
	return nil
}

func (m *Manager) containerStatus(ctx context.Context, name string) (string, error) {
	out, err := m.run(ctx, "container", "ls",
		"--all",
		"--filter", "name="+name,
		"--format", "{{.Names}}\t{{.State}}",
	)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 2 && strings.TrimPrefix(fields[0], "/") == name {
			return fields[1], nil
		}
	}
	return "not_found", nil
}

func (m *Manager) ContainerStatus(ctx context.Context, name string) (string, error) {
	return m.containerStatus(ctx, name)
}

// ContainerLogs returns the last `tail` lines of logs for a container.
func (m *Manager) ContainerLogs(ctx context.Context, name string, tail int) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	if tail > 5000 {
		tail = 5000
	}
	out, err := m.run(ctx, "container", "logs",
		"--tail", strconv.Itoa(tail),
		"--timestamps",
		name,
	)
	if err != nil {
		return "", err
	}
	return out, nil
}

// ContainerStats is a single snapshot of resource usage for a container.
type ContainerStats struct {
	Name        string  `json:"name"`
	CPUPerc     float64 `json:"cpuPerc"`
	MemUsed     int64   `json:"memUsedBytes"`
	MemLimit    int64   `json:"memLimitBytes"`
	MemPerc     float64 `json:"memPerc"`
	NetRxBytes  int64   `json:"netRxBytes"`
	NetTxBytes  int64   `json:"netTxBytes"`
	BlockRead   int64   `json:"blockReadBytes"`
	BlockWrite  int64   `json:"blockWriteBytes"`
	PIDs        int     `json:"pids"`
}

// AllStats returns a snapshot of stats for every nanoku-managed container
// (apps + caddy). Identified by name prefix "nanoku-" to catch both
// single-container deploys (name=nanoku-<app>-<rand>) and compose stacks
// (name=nanoku-<app>-<service>-<n>). Skips the nanoku self container
// (which uses a different name).
func (m *Manager) AllStats(ctx context.Context) ([]ContainerStats, error) {
	namesOut, err := m.run(ctx, "ps",
		"--all",
		"--filter", "name=nanoku-",
		"--format", "{{.Names}}",
	)
	if err != nil {
		return nil, fmt.Errorf("ps filter: %w", err)
	}
	names := make([]string, 0, 4)
	for _, line := range strings.Split(strings.TrimSpace(namesOut), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	args := []string{
		"stats", "--no-stream",
		"--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.NetIO}}\t{{.BlockIO}}\t{{.PIDs}}",
	}
	args = append(args, names...)
	out, err := m.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var stats []ContainerStats
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		memUsed, memLimit := parseMemUsage(fields[2])
		netRx, netTx := parseIO(fields[4])
		blockR, blockW := parseIO(fields[5])
		stats = append(stats, ContainerStats{
			Name:       fields[0],
			CPUPerc:    parsePerc(fields[1]),
			MemUsed:    memUsed,
			MemLimit:   memLimit,
			MemPerc:    parsePerc(fields[3]),
			NetRxBytes: netRx,
			NetTxBytes: netTx,
			BlockRead:  blockR,
			BlockWrite: blockW,
			PIDs:       atoiSafe(fields[6]),
		})
	}
	return stats, nil
}

// StatsByName returns a snapshot for a single named container.
func (m *Manager) StatsByName(ctx context.Context, name string) (*ContainerStats, error) {
	out, err := m.run(ctx, "stats",
		"--no-stream",
		"--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.NetIO}}\t{{.BlockIO}}\t{{.PIDs}}",
		name,
	)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		memUsed, memLimit := parseMemUsage(fields[2])
		netRx, netTx := parseIO(fields[4])
		blockR, blockW := parseIO(fields[5])
		return &ContainerStats{
			Name:       fields[0],
			CPUPerc:    parsePerc(fields[1]),
			MemUsed:    memUsed,
			MemLimit:   memLimit,
			MemPerc:    parsePerc(fields[3]),
			NetRxBytes: netRx,
			NetTxBytes: netTx,
			BlockRead:  blockR,
			BlockWrite: blockW,
			PIDs:       atoiSafe(fields[6]),
		}, nil
	}
	return nil, fmt.Errorf("no stats for %s", name)
}

func parsePerc(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	n, _ := strconv.ParseFloat(s, 64)
	return n
}

// parseMemUsage parses docker stats "USED / LIMIT" (e.g. "35.1MiB / 7.677GiB").
func parseMemUsage(s string) (used, limit int64) {
	parts := strings.Split(s, "/")
	if len(parts) >= 1 {
		used = parseBytes(strings.TrimSpace(parts[0]))
	}
	if len(parts) >= 2 {
		limit = parseBytes(strings.TrimSpace(parts[1]))
	}
	return
}

// parseIO parses docker stats "RX / TX" (e.g. "1.3kB / 0B").
func parseIO(s string) (rx, tx int64) {
	parts := strings.Split(s, "/")
	if len(parts) >= 1 {
		rx = parseBytes(strings.TrimSpace(parts[0]))
	}
	if len(parts) >= 2 {
		tx = parseBytes(strings.TrimSpace(parts[1]))
	}
	return
}

// parseBytes converts "35.1MiB" / "7.677GiB" / "1.3kB" / "0B" to bytes.
func parseBytes(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mults := []struct {
		suffix string
		mult   int64
	}{
		{"TiB", 1 << 40},
		{"GiB", 1 << 30},
		{"MiB", 1 << 20},
		{"KiB", 1 << 10},
		{"TB", 1e12},
		{"GB", 1e9},
		{"MB", 1e6},
		{"kB", 1e3},
		{"B", 1},
	}
	for _, m := range mults {
		if strings.HasSuffix(s, m.suffix) {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(s, m.suffix), 64)
			return int64(n * float64(m.mult))
		}
	}
	n, _ := strconv.ParseFloat(s, 64)
	return int64(n)
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func (m *Manager) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, m.binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String() + stderr.String()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}