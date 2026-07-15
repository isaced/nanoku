package docker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestPullImage_WithPullProgress_TranslatesJSON checks that
// WithPullProgress turns the engine's raw JSON progress stream into a
// clean human-readable line per state transition. We feed it a
// representative engine output (mix of interesting + noisy events) and
// assert the captured output contains only the milestone lines.
func TestPullImage_WithPullProgress_TranslatesJSON(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/images/create") {
			// Realistic slice of a pull progress stream. The "noisy"
			// entries (Downloading, Waiting, Verifying Checksum) must
			// be dropped; the milestones (Pulling fs layer, Download
			// complete, Extracting, Pull complete) must be translated
			// into clean lines.
			lines := []string{
				`{"status":"Pulling from library/nginx","id":"latest"}`,
				`{"status":"Pulling fs layer","id":"59f54fbcd984a3d9b8a82b7e7f2c8a1b6e9d4c5d6f7a8b9c0d1e2f3a4b5c6d7e8"}`,
				`{"status":"Waiting","id":"59f54fbcd984"}`,
				`{"status":"Downloading","progressDetail":{"current":1,"total":10},"id":"59f54fbcd984"}`,
				`{"status":"Verifying Checksum","id":"59f54fbcd984"}`,
				`{"status":"Download complete","id":"59f54fbcd984"}`,
				`{"status":"Extracting","id":"59f54fbcd984"}`,
				`{"status":"Pull complete","id":"59f54fbcd984"}`,
			}
			for _, l := range lines {
				_, _ = io.WriteString(w, l+"\n")
			}
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	var captured bytes.Buffer
	if err := m.PullImage(context.Background(), "nginx:1.27", WithPullProgress(&captured)); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	got := captured.String()
	// Milestones that must appear.
	for _, want := range []string{
		"Pulling 59f54fbcd984",
		"Layer 59f54fbcd984 downloaded",
		"Extracting 59f54fbcd984",
		"Pull complete",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing milestone %q in output:\n%s", want, got)
		}
	}
	// Noisy events that must NOT appear.
	for _, banned := range []string{
		"Waiting",
		"Downloading",
		"Verifying Checksum",
		`"id":"`,
		`{"status":`,
	} {
		if strings.Contains(got, banned) {
			t.Errorf("noisy event %q leaked into output:\n%s", banned, got)
		}
	}
}

// TestPullImage_WithPullProgress_SurfacesError confirms that an engine
// error in the progress stream surfaces as a single "pull error: ..."
// line — the most important thing in a deploy log must never be
// silently swallowed by the translator.
func TestPullImage_WithPullProgress_SurfacesError(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/images/create") {
			_, _ = io.WriteString(w, `{"errorDetail":{"message":"pull access denied for foo"},"error":"pull access denied for foo"}`+"\n")
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	var captured bytes.Buffer
	if err := m.PullImage(context.Background(), "ghcr.io/private/foo", WithPullProgress(&captured)); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	if !strings.Contains(captured.String(), "pull error: pull access denied for foo") {
		t.Errorf("expected error line, got: %q", captured.String())
	}
}

// TestProgressWriter_HandlesChunkedWrites checks the buffer management
// inside progressWriter: a JSON object split across two Writes must be
// reassembled and translated as a single event. The engine doesn't
// guarantee line-aligned writes, and the translator must cope.
func TestProgressWriter_HandlesChunkedWrites(t *testing.T) {
	var out bytes.Buffer
	p := newProgressWriter(&out)
	// Same event written in three chunks: partial JSON, continuation, newline.
	_, _ = p.Write([]byte(`{"status":"Pull`))
	_, _ = p.Write([]byte(`ing fs layer","id`))
	_, _ = p.Write([]byte(`":"abc123456789"}` + "\n"))
	if got := out.String(); got != "Pulling abc123456789\n" {
		t.Errorf("got %q, want %q", got, "Pulling abc123456789\n")
	}
	// The buffer must be empty after the newline so the next Write
	// doesn't accidentally prepend the tail of the previous event.
	_, _ = p.Write([]byte(`{"status":"Pull complete"}` + "\n"))
	if got := out.String(); got != "Pulling abc123456789\nPull complete\n" {
		t.Errorf("buffer leaked previous partial: %q", got)
	}
}
