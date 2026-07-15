package docker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
)

// TestPullImage_WithPullStream_TeesProgress exercises the WithPullStream
// functional option. PullImage should still complete successfully (the
// JSON stream is consumed), and the supplied writer should see each line
// of the engine's progress output verbatim.
func TestPullImage_WithPullStream_TeesProgress(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/images/create") {
			_, _ = io.WriteString(w, `{"status":"Pulling fs layer"}`+"\n")
			_, _ = io.WriteString(w, `{"status":"Downloading"}`+"\n")
			_, _ = io.WriteString(w, `{"status":"Pull complete"}`+"\n")
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	var captured bytes.Buffer
	if err := m.PullImage(context.Background(), "nginx:1.27", WithPullStream(&captured)); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	got := captured.String()
	for _, want := range []string{`{"status":"Pulling fs layer"}`, `{"status":"Downloading"}`, `{"status":"Pull complete"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing line %q in stream: %q", want, got)
		}
	}
}

// TestPullImage_NoStream_NoOptionsKeepsLegacy asserts the no-option call
// still discards the stream (so a regression that accidentally writes
// to a nil sink panics) and the existing PullImage(ctx, ref) signature
// keeps compiling.
func TestPullImage_NoStream_NoOptionsKeepsLegacy(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/images/create") {
			_, _ = io.WriteString(w, "ignored\n")
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	if err := m.PullImage(context.Background(), "nginx:1.27"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
}

// TestContainerLogsStream_DemuxesStdoutAndStderrEndToEnd checks the
// line-channel output of ContainerLogsStream. We hand the fake daemon a
// docker raw-stream payload (one stdout frame + one stderr frame) and
// assert that both lines arrive on the channel in the same order the
// engine emitted them.
func TestContainerLogsStream_DemuxesStdoutAndStderrEndToEnd(t *testing.T) {
	var payload bytes.Buffer
	stdcopy.NewStdWriter(&payload, stdcopy.Stdout).Write([]byte("out-1\n"))
	stdcopy.NewStdWriter(&payload, stdcopy.Stderr).Write([]byte("err-1\n"))
	stdcopy.NewStdWriter(&payload, stdcopy.Stdout).Write([]byte("out-2\n"))

	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/logs") {
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			_, _ = w.Write(payload.Bytes())
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	stream, err := m.ContainerLogsStream(context.Background(), "nanoku-app", 100, false)
	if err != nil {
		t.Fatalf("ContainerLogsStream: %v", err)
	}
	defer stream.Cancel()

	want := []string{"out-1\n", "err-1\n", "out-2\n"}
	for i, w := range want {
		select {
		case line, ok := <-stream.Lines:
			if !ok {
				t.Fatalf("stream closed early at line %d", i)
			}
			if line != w {
				t.Errorf("line %d: got %q, want %q", i, line, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for line %d", i)
		}
	}
	// Err channel should close cleanly (no demux error).
	select {
	case err, ok := <-stream.Err:
		if ok && err != nil {
			t.Errorf("unexpected stream error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("err channel did not close")
	}
}

// TestContainerLogsStream_CancelReleasesEngineStream verifies that
// calling Cancel unblocks a read goroutine waiting on the engine (we
// simulate this with a slow / never-finishing stream from the fake
// daemon). Without Cancel the test would block forever on the line
// read; with it we exit cleanly within a second.
func TestContainerLogsStream_CancelReleasesEngineStream(t *testing.T) {
	release := make(chan struct{})
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/logs") {
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			flusher, _ := w.(http.Flusher)
			// First, send one line so the consumer has something to
			// read. Then block on `release` so the engine stream stays
			// open — we want Cancel to be the thing that unblocks the
			// read goroutine, not the daemon returning.
			_, _ = stdcopy.NewStdWriter(w, stdcopy.Stdout).Write([]byte("first\n"))
			if flusher != nil {
				flusher.Flush()
			}
			<-release
			return
		}
		http.NotFound(w, r)
	}
	m := newTestManager(t, fd, Config{})
	stream, err := m.ContainerLogsStream(context.Background(), "nanoku-app", 100, true)
	if err != nil {
		t.Fatalf("ContainerLogsStream: %v", err)
	}
	select {
	case line, ok := <-stream.Lines:
		if !ok || line != "first\n" {
			t.Fatalf("first line: ok=%v got=%q", ok, line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first line never arrived")
	}
	// Trigger cancel; the read goroutine should exit.
	done := make(chan struct{})
	go func() {
		stream.Cancel()
		// Drain Err to confirm the goroutine ended.
		for range stream.Err {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel did not unblock reader")
	}
	close(release)
}

// TestLineWriter_PreservesPartialLines confirms that a Write that doesn't
// end with '\n' is buffered, and the next Write that does end with '\n'
// flushes both halves as a single line. This is what stdcopy hands us:
// one frame at a time, and the frame may or may not be a complete line.
func TestLineWriter_PreservesPartialLines(t *testing.T) {
	ch := make(chan string, 8)
	w := lineWriter{w: ch}
	w.Write([]byte("hello "))
	w.Write([]byte("world\n"))
	select {
	case got := <-ch:
		if got != "hello world\n" {
			t.Errorf("got %q, want %q", got, "hello world\n")
		}
	default:
		t.Fatal("expected a line after final write")
	}
}

// TestLineWriter_SplitsMultipleLinesInOneWrite covers the common case
// where a single Write contains several '\n'-terminated lines.
func TestLineWriter_SplitsMultipleLinesInOneWrite(t *testing.T) {
	ch := make(chan string, 8)
	w := lineWriter{w: ch}
	w.Write([]byte("a\nb\nc\n"))
	want := []string{"a\n", "b\n", "c\n"}
	for i, w := range want {
		select {
		case got := <-ch:
			if got != w {
				t.Errorf("line %d: got %q, want %q", i, got, w)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout at line %d", i)
		}
	}
}
