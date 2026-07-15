package docker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBin writes a small shell script that the docker CLI tests can shell
// out to. It echoes a configurable response on stdout, an optional stderr
// marker, and an optional exit code.
func fakeBin(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fakeBin only supported on unix")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-docker")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	return p
}

func TestComposeArgs(t *testing.T) {
	cases := []struct {
		name         string
		project      string
		filePath     string
		subcmd       string
		jsonProgress bool
		extra        []string
		want         []string
	}{
		{"project + file", "blog", "/etc/blog.yml", "up", false, []string{"-d"},
			[]string{"compose", "-p", "blog", "-f", "/etc/blog.yml", "up", "-d"}},
		{"project only", "blog", "", "stop", false, nil,
			[]string{"compose", "-p", "blog", "stop"}},
		{"file only", "", "/etc/x.yml", "down", false, nil,
			[]string{"compose", "-f", "/etc/x.yml", "down"}},
		{"bare", "", "", "ps", false, []string{"--format", "{{.Name}}"},
			[]string{"compose", "ps", "--format", "{{.Name}}"}},
		{"json progress injects --progress json before project/file", "blog", "/etc/blog.yml", "up", true, []string{"-d"},
			[]string{"compose", "--progress", "json", "-p", "blog", "-f", "/etc/blog.yml", "up", "-d"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := composeArgs(tc.project, tc.filePath, tc.subcmd, tc.jsonProgress, tc.extra...)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestComposeUp_AddsPullAlways(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBin(t, `printf '%s\n' "$@" > "$DOCKER_FAKE_ARGV"
exit 0`)
	t.Setenv("DOCKER_FAKE_ARGV", logPath)

	m := &Manager{composeBinary: bin}
	if err := m.ComposeUp(t.Context(), "blog", "/etc/blog.yml", true); err != nil {
		t.Fatalf("ComposeUp: %v", err)
	}
	got := readLines(t, logPath)
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--pull always") {
		t.Errorf("argv missing --pull always: %v", got)
	}
	if !strings.Contains(joined, "up") || !containsToken(got, "-d") {
		t.Errorf("argv missing `up` or `-d`: %v", got)
	}
}

func TestComposeUp_OmitsPullWhenFalse(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBin(t, `printf '%s\n' "$@" > "$DOCKER_FAKE_ARGV"
exit 0`)
	t.Setenv("DOCKER_FAKE_ARGV", logPath)

	m := &Manager{composeBinary: bin}
	if err := m.ComposeUp(t.Context(), "blog", "", false); err != nil {
		t.Fatalf("ComposeUp: %v", err)
	}
	got := readLines(t, logPath)
	if containsToken(got, "--pull") {
		t.Errorf("argv should not include --pull when pull=false: %v", got)
	}
	if !containsToken(got, "up") || !containsToken(got, "-d") {
		t.Errorf("argv missing `up` or `-d`: %v", got)
	}
}

func containsToken(argv []string, needle string) bool {
	for _, a := range argv {
		if a == needle {
			return true
		}
	}
	return false
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func TestComposeUp_HappyPath(t *testing.T) {
	bin := fakeBin(t, `printf 'pulled\nstarted\n'
exit 0`)
	m := &Manager{composeBinary: bin}
	if err := m.ComposeUp(t.Context(), "blog", "/etc/blog.yml", true); err != nil {
		t.Fatalf("ComposeUp: %v", err)
	}
}

func TestComposeUp_WithStream_TeesStdout(t *testing.T) {
	// The fake bin writes three progress lines to stdout and exits
	// cleanly. WithComposeStream should tee each line into the supplied
	// writer, and ComposeUp should still report nil error.
	bin := fakeBin(t, `printf 'Pulling blog ...\nCreating blog ... done\nStarting blog ... done\n'
exit 0`)
	m := &Manager{composeBinary: bin}
	var got strings.Builder
	if err := m.ComposeUp(t.Context(), "blog", "/etc/blog.yml", true, WithComposeStream(&got)); err != nil {
		t.Fatalf("ComposeUp: %v", err)
	}
	for _, want := range []string{"Pulling blog", "Creating blog", "Starting blog"} {
		if !strings.Contains(got.String(), want) {
			t.Errorf("missing line %q in stream: %q", want, got.String())
		}
	}
}

func TestComposeUp_WithStream_TeesStderr(t *testing.T) {
	// `docker compose up` writes ALL of its non-TTY progress (Pulling /
	// Pulled / Creating / Started) to stderr, not stdout. If runCLIStream
	// only teed stdout the deploy log would stay stuck on the
	// "→ compose up" annotation for the whole pull. Verify stderr lines
	// reach the live stream.
	bin := fakeBin(t, `printf 'Image blog Pulling \nImage blog Pulled \nContainer blog-1 Started \n' 1>&2
exit 0`)
	m := &Manager{composeBinary: bin}
	var got strings.Builder
	if err := m.ComposeUp(t.Context(), "blog", "/etc/blog.yml", true, WithComposeStream(&got)); err != nil {
		t.Fatalf("ComposeUp: %v", err)
	}
	for _, want := range []string{"Pulling", "Pulled", "Started"} {
		if !strings.Contains(got.String(), want) {
			t.Errorf("missing stderr progress line %q in stream: %q", want, got.String())
		}
	}
}

func TestComposeUp_WithStream_FoldsStderrOnFailure(t *testing.T) {
	// WithComposeStream changes the failure path: we don't capture
	// stdout (the operator is seeing it live), but the stderr text must
	// still surface in the returned error so the deploy row gets a
	// useful diagnostic.
	bin := fakeBin(t, `echo 'pull access denied for blog' 1>&2
exit 1`)
	m := &Manager{composeBinary: bin}
	var got strings.Builder
	err := m.ComposeUp(t.Context(), "blog", "/etc/blog.yml", true, WithComposeStream(&got))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pull access denied") {
		t.Errorf("error %q should contain stderr text", err)
	}
}

func TestComposeUp_WithProgress_ParsesJSONEvents(t *testing.T) {
	// WithComposeProgress runs with `--progress json` and decodes the
	// JSON event stream into (msg, key) pairs. The fake bin emits real
	// compose progress JSON on stderr (where compose v5 puts it). The
	// callback must receive keyed lines for layer events and the
	// `--progress json` flag must be in the argv.
	bin := fakeBin(t, `printf '%s\n' \
'{"id":"Image blog:latest","status":"Working","text":"Pulling"}' \
'{"id":"abc123","parent_id":"Image blog:latest","status":"Working","text":"Downloading","details":"48.51kB","current":48508,"total":3994465,"percent":1}' \
'{"id":"abc123","parent_id":"Image blog:latest","status":"Done","text":"Pull complete","details":"0B","percent":100}' \
'{"id":"Container blog-1","status":"Done","text":"Started"}' 1>&2
exit 0`)
	m := &Manager{composeBinary: bin}
	var lines []composeLine
	var argv []string
	// Capture argv by also writing it to a second env-backed file is
	// overkill; the WithProgress path builds argv via composeArgs and
	// we assert --progress json is present in TestComposeArgs. Here we
	// only assert the decoded events.
	_ = argv
	if err := m.ComposeUp(t.Context(), "blog", "/etc/blog.yml", true, WithComposeProgress(func(msg, key string) {
		lines = append(lines, composeLine{Msg: msg, Key: key})
	})); err != nil {
		t.Fatalf("ComposeUp: %v", err)
	}
	if len(lines) != 4 {
		t.Fatalf("got %d progress lines, want 4: %+v", len(lines), lines)
	}
	// Layer download + complete share the same key (replace in place).
	if lines[1].Key != "abc123" || lines[2].Key != "abc123" {
		t.Errorf("layer events should share key abc123: got %q / %q", lines[1].Key, lines[2].Key)
	}
	if !strings.Contains(lines[1].Msg, "Downloading") || !strings.Contains(lines[1].Msg, "48.51kB") {
		t.Errorf("download line = %q, want Downloading + details", lines[1].Msg)
	}
	if !strings.Contains(lines[2].Msg, "Pull complete") {
		t.Errorf("complete line = %q, want Pull complete", lines[2].Msg)
	}
	// Top-level events carry their own id as key.
	if lines[0].Key != "Image blog:latest" {
		t.Errorf("image event Key = %q, want Image blog:latest", lines[0].Key)
	}
	if lines[3].Key != "Container blog-1" {
		t.Errorf("container event Key = %q, want Container blog-1", lines[3].Key)
	}
}

func TestComposeUp_WithProgress_FoldsStderrOnFailure(t *testing.T) {
	// A fatal {error:true} object on stderr is surfaced as an append
	// line (no key) and the subprocess exit code still produces a
	// wrapped error carrying the trailing stderr.
	bin := fakeBin(t, `printf '%s\n' \
'{"id":"Image blog:latest","status":"Error","text":"Error","details":"TLS handshake timeout"}' \
'{"error":true,"message":"Error response from daemon: TLS handshake timeout"}' 1>&2
exit 1`)
	m := &Manager{composeBinary: bin}
	var lines []composeLine
	err := m.ComposeUp(t.Context(), "blog", "/etc/blog.yml", true, WithComposeProgress(func(msg, key string) {
		lines = append(lines, composeLine{Msg: msg, Key: key})
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "compose up") {
		t.Errorf("error %q should be wrapped with 'compose up'", err)
	}
	// The fatal error line must be present and carry no key (append).
	var fatal string
	for _, l := range lines {
		if l.Key == "" && strings.Contains(l.Msg, "TLS handshake timeout") {
			fatal = l.Msg
		}
	}
	if fatal == "" {
		t.Errorf("missing fatal error line among %d lines: %+v", len(lines), lines)
	}
}

func TestComposeUp_FoldsStderrIntoError(t *testing.T) {
	bin := fakeBin(t, `echo 'pull access denied for blog' 1>&2
echo 'partial stdout' 1>&2
exit 1`)
	m := &Manager{composeBinary: bin}
	err := m.ComposeUp(t.Context(), "blog", "/etc/blog.yml", true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pull access denied") {
		t.Errorf("error %q should contain stderr text", err)
	}
	// "partial stdout" is on stderr here, but it must NOT be confused with
	// the structured stdout we return on success. On failure, only the
	// stderr text ends up in the error string.
	if !strings.Contains(err.Error(), "compose up") {
		t.Errorf("error %q should contain 'compose up' wrapper", err)
	}
}

func TestComposeStopStartRestartDown(t *testing.T) {
	bin := fakeBin(t, `exit 0`)
	m := &Manager{composeBinary: bin}
	for _, fn := range []func() error{
		func() error { return m.ComposeStop(t.Context(), "blog", "/etc/blog.yml") },
		func() error { return m.ComposeStart(t.Context(), "blog", "/etc/blog.yml") },
		func() error { return m.ComposeRestart(t.Context(), "blog", "/etc/blog.yml") },
		func() error { return m.ComposeDown(t.Context(), "blog", "/etc/blog.yml") },
	} {
		if err := fn(); err != nil {
			t.Fatalf("%v", err)
		}
	}
}

func TestComposePSNames(t *testing.T) {
	bin := fakeBin(t, `printf 'nanoku-blog-web-1\nnanoku-blog-db-1\n\nother\n'
exit 0`)
	m := &Manager{composeBinary: bin}
	names, err := m.ComposePSNames(t.Context(), "blog", "/etc/blog.yml")
	if err != nil {
		t.Fatalf("ComposePSNames: %v", err)
	}
	want := []string{"nanoku-blog-web-1", "nanoku-blog-db-1", "other"}
	if len(names) != len(want) {
		t.Fatalf("got %d names, want %d (%v)", len(names), len(want), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestComposePSNames_EmptyWhenNoContainers(t *testing.T) {
	bin := fakeBin(t, `exit 0`)
	m := &Manager{composeBinary: bin}
	names, err := m.ComposePSNames(t.Context(), "blog", "")
	if err != nil {
		t.Fatalf("ComposePSNames: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("got %d names, want 0: %v", len(names), names)
	}
}

func TestRunCLI_KeepsStreamsSeparate(t *testing.T) {
	bin := fakeBin(t, `echo 'STDERR_WARNING' 1>&2
echo 'CLEAN_STDOUT'`)
	m := &Manager{composeBinary: bin}
	out, err := m.runCLI(t.Context(), "run", "--name", "x")
	if err != nil {
		t.Fatalf("runCLI: %v", err)
	}
	if !strings.Contains(out, "CLEAN_STDOUT") {
		t.Errorf("missing stdout: %q", out)
	}
	if strings.Contains(out, "STDERR_WARNING") {
		t.Errorf("stderr leaked into stdout: %q", out)
	}
}

func TestRunCLI_ErrorWrapsStderr(t *testing.T) {
	bin := fakeBin(t, `echo 'image not found' 1>&2
exit 1`)
	m := &Manager{composeBinary: bin}
	_, err := m.runCLI(t.Context(), "pull", "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "image not found") {
		t.Errorf("error %v should contain stderr text", err)
	}
}