package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func lookDocker() (string, error) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return "", fmt.Errorf("docker CLI not found in PATH: %w", err)
	}
	return bin, nil
}

// Login authenticates to a docker registry. Password is passed via stdin
// to keep it out of the process list / logs.
func (m *Manager) Login(ctx context.Context, registry, username, password string) error {
	if username == "" {
		return fmt.Errorf("registry_username is required for login")
	}
	args := []string{"login"}
	if registry != "" {
		args = append(args, registry)
	}
	args = append(args, "-u", username, "--password-stdin")
	cmd := exec.CommandContext(ctx, m.loginBinary, args...)
	cmd.Stdin = strings.NewReader(password)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (m *Manager) Logout(ctx context.Context, registry string) error {
	args := []string{"logout"}
	if registry != "" {
		args = append(args, registry)
	}
	cmd := exec.CommandContext(ctx, m.loginBinary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker logout: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
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