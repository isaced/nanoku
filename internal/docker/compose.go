package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// composeArgs builds the argv for a `docker compose` subcommand.
// filePath="" means: use the file in the current working dir.
func composeArgs(project, filePath, subcmd string, extra ...string) []string {
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

func (m *Manager) ComposeUp(ctx context.Context, project, filePath string, pull bool) error {
	extra := []string{"-d"}
	if pull {
		extra = append([]string{"--pull", "always"}, extra...)
	}
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "up", extra...)...)
	if err != nil {
		return fmt.Errorf("compose up: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposeStop(ctx context.Context, project, filePath string) error {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "stop")...)
	if err != nil {
		return fmt.Errorf("compose stop: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposeStart(ctx context.Context, project, filePath string) error {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "start")...)
	if err != nil {
		return fmt.Errorf("compose start: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposeRestart(ctx context.Context, project, filePath string) error {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "restart")...)
	if err != nil {
		return fmt.Errorf("compose restart: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposeDown(ctx context.Context, project, filePath string) error {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "down")...)
	if err != nil {
		return fmt.Errorf("compose down: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposePSNames(ctx context.Context, project, filePath string) ([]string, error) {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "ps", "--format", "{{.Name}}")...)
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

// runCLI shells out to the docker CLI for subcommands not exposed by the
// Engine API (notably `docker compose`). stdout and stderr are kept
// separate; stderr is folded into the error on failure so diagnostics
// aren't lost, but never bleeds into returned stdout (where callers
// parse structured output).
func (m *Manager) runCLI(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, m.composeBinary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}