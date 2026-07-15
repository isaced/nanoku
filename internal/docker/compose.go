package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// ComposeOption mutates a compose call. The zero value is a no-op so the
// existing ComposeUp(ctx, project, filePath, pull) signature still works.
type ComposeOption func(*composeConfig)

type composeConfig struct {
	stream io.Writer // nil = fold stdout into return value (legacy behavior)
}

// WithComposeStream tees the raw `docker compose` stdout (and stderr on
// failure) into w as the command runs. Used by the deploy SSE handler to
// surface compose progress (Pulling / Creating / Starting lines) to the
// operator in real time.
func WithComposeStream(w io.Writer) ComposeOption {
	return func(c *composeConfig) { c.stream = w }
}

func (m *Manager) ComposeUp(ctx context.Context, project, filePath string, pull bool, opts ...ComposeOption) error {
	cfg := composeConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	extra := []string{"-d"}
	if pull {
		extra = append([]string{"--pull", "always"}, extra...)
	}
	if cfg.stream != nil {
		// Stream the raw subprocess output; we still capture stdout for
		// the error-on-failure path so we can surface the trailing
		// diagnostic, but it goes to a discardable sink on success.
		_, err := m.runCLIStream(ctx, cfg.stream, composeArgs(project, filePath, "up", extra...)...)
		if err != nil {
			return fmt.Errorf("compose up: %w", err)
		}
		return nil
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

// runCLIStream is the streaming variant of runCLI: it pipes the
// subprocess's stdout AND stderr straight into w as the command runs
// (so a slow consumer sees progress in real time), and on failure folds
// the captured stderr into the error so the diagnostic isn't lost.
//
// Both streams are teed into w because `docker compose up` (non-TTY)
// writes *all* of its progress — Pulling/Pulled/Creating/Started — to
// stderr, not stdout. Streaming only stdout left the deploy log stuck
// on the "→ compose up" annotation with no progress for the entire
// pull, which looked like a hang. A trailing copy of stderr is kept so
// a failure still surfaces a useful diagnostic; on success that copy
// is discarded.
func (m *Manager) runCLIStream(ctx context.Context, w io.Writer, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, m.composeBinary, args...)
	var stderr bytes.Buffer
	cmd.Stdout = w
	cmd.Stderr = io.MultiWriter(w, &stderr)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return "", nil
}