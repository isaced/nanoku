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
// When jsonProgress is true, the global `--progress json` flag is injected
// (before -p/-f/subcmd) so compose emits one JSON object per progress event
// on stderr instead of plain text.
func composeArgs(project, filePath, subcmd string, jsonProgress bool, extra ...string) []string {
	args := []string{"compose"}
	if jsonProgress {
		args = append(args, "--progress", "json")
	}
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
	// stream is the legacy raw-text tee (stdout+stderr into w). Mutually
	// exclusive with progress: if progress is set, stream is ignored.
	stream io.Writer
	// progress is the structured-progress callback. When set, ComposeUp
	// runs `docker compose --progress json up` and decodes the JSON event
	// stream into (msg, key) pairs: a non-empty key means "replace the
	// previous line with this key in-place" (per-layer download ticks),
	// an empty key means "append". This gives the operator a single
	// updating row per layer instead of dozens of scrolling lines.
	progress func(msg, key string)
}

// WithComposeStream tees the raw `docker compose` stdout AND stderr into w
// as the command runs. Compose writes all of its non-TTY progress
// (Pulling / Creating / Starting) to stderr, so both streams must be teed
// or the operator sees nothing during a long pull.
//
// Prefer WithComposeProgress for the deploy path: it parses the structured
// `--progress json` stream so download progress updates in-place rather
// than scrolling. WithComposeStream is retained for callers that want raw
// text (e.g. a future plain log dump).
func WithComposeStream(w io.Writer) ComposeOption {
	return func(c *composeConfig) { c.stream = w }
}

// WithComposeProgress routes compose progress through the structured
// `--progress json` stream. Each decoded event is handed to emit as
// (msg, key): when key != "" the caller should replace the last line with
// that key in-place (per-layer download/extraction progress); when
// key == "" the line appends (fatal errors, non-progress text).
func WithComposeProgress(emit func(msg, key string)) ComposeOption {
	return func(c *composeConfig) { c.progress = emit }
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
	if cfg.progress != nil {
		// Structured progress: run with `--progress json` and decode the
		// JSON event stream (emitted on stderr) through
		// composeProgressWriter, which translates each event into a
		// (msg, key) pair. A non-empty key means "replace the same-key
		// row in-place" so a 50 MB layer download updates one row
		// instead of scrolling dozens of lines. stdout is discarded
		// (compose writes nothing meaningful there in json mode); a
		// trailing copy of stderr is kept for the failure diagnostic.
		pw := newComposeProgressWriter(func(cl composeLine) {
			cfg.progress(cl.Msg, cl.Key)
		})
		var stderr bytes.Buffer
		args := composeArgs(project, filePath, "up", true, extra...)
		cmd := exec.CommandContext(ctx, m.composeBinary, args...)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.MultiWriter(pw, &stderr)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("compose up: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	if cfg.stream != nil {
		// Legacy raw-text tee (stdout + stderr into w). Kept for callers
		// that don't opt into structured progress.
		_, err := m.runCLIStream(ctx, cfg.stream, composeArgs(project, filePath, "up", false, extra...)...)
		if err != nil {
			return fmt.Errorf("compose up: %w", err)
		}
		return nil
	}
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "up", false, extra...)...)
	if err != nil {
		return fmt.Errorf("compose up: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposeStop(ctx context.Context, project, filePath string) error {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "stop", false)...)
	if err != nil {
		return fmt.Errorf("compose stop: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposeStart(ctx context.Context, project, filePath string) error {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "start", false)...)
	if err != nil {
		return fmt.Errorf("compose start: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposeRestart(ctx context.Context, project, filePath string) error {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "restart", false)...)
	if err != nil {
		return fmt.Errorf("compose restart: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposeDown(ctx context.Context, project, filePath string) error {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "down", false)...)
	if err != nil {
		return fmt.Errorf("compose down: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *Manager) ComposePSNames(ctx context.Context, project, filePath string) ([]string, error) {
	out, err := m.runCLI(ctx, composeArgs(project, filePath, "ps", false, "--format", "{{.Name}}")...)
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