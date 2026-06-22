package docker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBin writes a tiny executable that echoes its argv onto stdout and an
// optional stderr marker, so tests can assert run() keeps the two streams
// separate. The script reads its argv from $@.
func fakeBin(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// `.bat` would need different quoting; the suite runs on darwin/linux in
		// CI and the assertions don't depend on the windows path.
		t.Skip("fakeBin only supported on unix")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-docker")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	return p
}

// TestRun_StdoutExcludesStderr is the regression test for the bug where run()
// returned stdout+stderr concatenated. A deprecation warning on stderr must
// not pollute the parsed container ID on stdout.
func TestRun_StdoutExcludesStderr(t *testing.T) {
	bin := fakeBin(t, `echo 'DEPRECATED: some warning' 1>&2
echo 'abc123fullid'`)
	m := &Manager{binary: bin}

	out, err := m.run(t.Context(), "run", "--name", "x")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(out); got != "abc123fullid" {
		t.Errorf("stdout = %q, want %q (stderr must not leak in)", got, "abc123fullid")
	}
}

// TestRun_ErrorWrapsStderr confirms the stderr text is preserved in the
// error message when the command exits non-zero (so diagnostics aren't lost).
func TestRun_ErrorWrapsStderr(t *testing.T) {
	bin := fakeBin(t, `echo 'pull access denied' 1>&2
exit 1`)
	m := &Manager{binary: bin}

	_, err := m.run(t.Context(), "pull", "nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "pull access denied") {
		t.Errorf("error = %v, want it to contain stderr text", err)
	}
}

// TestRunCombined_MergesStreams confirms runCombined returns both streams
// merged (used for `docker logs` where the container's own stderr is payload).
func TestRunCombined_MergesStreams(t *testing.T) {
	bin := fakeBin(t, `echo 'stdout line'
echo 'stderr line' 1>&2`)
	m := &Manager{binary: bin}

	out, err := m.runCombined(t.Context(), "logs", "x")
	if err != nil {
		t.Fatalf("runCombined: %v", err)
	}
	if !strings.Contains(out, "stdout line") {
		t.Errorf("missing stdout in combined output: %q", out)
	}
	if !strings.Contains(out, "stderr line") {
		t.Errorf("missing stderr in combined output: %q", out)
	}
}
