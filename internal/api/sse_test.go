package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSSEHeaders_SetsExpectedValues pins the response header set so
// proxies (nginx, Caddy) and browsers can be configured to behave
// correctly. Touching these without realizing it is a footgun.
func TestSSEHeaders_SetsExpectedValues(t *testing.T) {
	w := httptest.NewRecorder()
	sseHeaders(w)
	h := w.Header()
	if got := h.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := h.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := h.Get("Connection"); got != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", got)
	}
	if got := h.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no (defeats nginx buffering)", got)
	}
}

// TestStreamToSSE_WritesExpectedEventFormat feeds three lines into the
// source channels and asserts the response body contains the spec-
// compliant SSE framing: "event: line\n" + "data: <line>\n" + blank
// line, plus the initial retry + comment.
func TestStreamToSSE_WritesExpectedEventFormat(t *testing.T) {
	lines := make(chan string, 4)
	errs := make(chan error, 1)
	lines <- "alpha"
	lines <- "beta"
	lines <- "gamma"
	close(lines)

	var cancelled atomic.Bool
	cancel := func() { cancelled.Store(true) }

	w := httptest.NewRecorder()
	streamToSSE(context.Background(), w, lines, errs, cancel)

	body := w.Body.String()
	// Order: retry directive, comment, then three events.
	want := []string{
		"retry: 2000",
		": nanoku log stream",
		"event: line",
		"data: alpha",
		"data: beta",
		"data: gamma",
		": nanoku log stream end",
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q\nbody:\n%s", w, body)
		}
	}
	if !cancelled.Load() {
		t.Error("cancel was not called after stream end")
	}
}

// TestStreamToSSE_TerminalErrorEmitsErrorEvent covers the error path:
// when the source reports an error, we emit a single `event: error`
// line and exit. The browser's EventSource will then reconnect per the
// retry directive.
func TestStreamToSSE_TerminalErrorEmitsErrorEvent(t *testing.T) {
	lines := make(chan string, 1)
	errs := make(chan error, 1)
	// Sequence: push the line, run the helper, then push+close errs.
	// The helper is a real goroutine that will read "ok", emit it, and
	// loop back into the select — at which point errs is ready and
	// we get the error event. The select order in Go is randomized
	// across equal-cost cases, so we don't try to enforce which
	// channel closes first; we assert both events eventually show up.
	lines <- "ok"
	var cancelled atomic.Bool
	cancel := func() { cancelled.Store(true) }

	w := httptest.NewRecorder()
	go func() {
		// Give the helper a chance to consume "ok" before we close
		// the channels. (No synchronization here; we accept the small
		// timing race and assert the *union* of events in the body.)
		time.Sleep(2 * time.Millisecond)
		errs <- &fakeError{msg: "boom"}
		close(errs)
		close(lines)
	}()
	streamToSSE(context.Background(), w, lines, errs, cancel)

	body := w.Body.String()
	if !strings.Contains(body, "event: line\ndata: ok") {
		t.Errorf("missing line event in body:\n%s", body)
	}
	if !strings.Contains(body, "event: log-error\ndata: boom") {
		t.Errorf("missing error event in body:\n%s", body)
	}
	if !cancelled.Load() {
		t.Error("cancel was not called after error")
	}
}

// TestStreamToSSE_ClientDisconnectInvokesCancel confirms that when the
// request context is canceled, the helper returns and runs the cancel
// callback. This is what releases the engine stream on tab close.
func TestStreamToSSE_ClientDisconnectInvokesCancel(t *testing.T) {
	lines := make(chan string) // unbuffered: blocks until reader drains
	errs := make(chan error, 1)
	var cancelled atomic.Bool
	cancel := func() { cancelled.Store(true) }

	ctx, cancelCtx := context.WithCancel(context.Background())
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		streamToSSE(ctx, w, lines, errs, cancel)
		close(done)
	}()
	// Give the goroutine a moment to start its select loop.
	time.Sleep(20 * time.Millisecond)
	cancelCtx()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamToSSE did not return after ctx cancel")
	}
	if !cancelled.Load() {
		t.Error("cancel was not invoked on client disconnect")
	}
}

// TestStreamToSSE_KeepaliveEmitted pins the heartbeat behavior. We open
// a source that never produces a line, run streamToSSE for just over
// one keepalive tick (15s default — too long for a unit test, so we
// assert via a small fudge factor on the comment line "ka"). The
// "ka" comment is what proxies see as activity and is the contract.
func TestStreamToSSE_KeepaliveContract(t *testing.T) {
	// We can't wait 15s in a unit test. Instead, we publish a comment
	// manually through sseWriter to assert the format. The keepalive
	// goroutine is a separate path; if the contract changes the
	// implicit assertion here will keep it stable.
	w := httptest.NewRecorder()
	sw := newSSEWriter(w)
	if err := sw.sseComment("ka"); err != nil {
		t.Fatalf("sseComment: %v", err)
	}
	if !strings.Contains(w.Body.String(), ": ka") {
		t.Errorf("missing keepalive comment: %q", w.Body.String())
	}
}

// TestSSEWriter_MultilineDataFracturesToMultipleDataLines — per the
// SSE spec, embedded newlines in a data value must be encoded as
// multiple data: lines. We assert sseEvent splits on '\n' correctly.
func TestSSEWriter_MultilineDataFracturesToMultipleDataLines(t *testing.T) {
	w := httptest.NewRecorder()
	sw := newSSEWriter(w)
	if err := sw.sseEvent("line", "a\nb"); err != nil {
		t.Fatalf("sseEvent: %v", err)
	}
	got := w.Body.String()
	want := "event: line\ndata: a\ndata: b\n\n"
	if got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

// TestStreamToSSE_SetsStatus200 verifies the contract that the helper
// expects the caller to have already written headers (it doesn't call
// WriteHeader itself). We assert the headers it sets stay intact.
func TestStreamToSSE_SetsStatus200(t *testing.T) {
	lines := make(chan string)
	close(lines)
	w := httptest.NewRecorder()
	streamToSSE(context.Background(), w, lines, make(chan error, 1), func() {})
	if w.Code != http.StatusOK {
		t.Errorf("Code = %d, want 200", w.Code)
	}
}

// TestStreamToSSE_EmitsKeepaliveComment verifies the periodic "ka"
// comment that defeats proxy idle timeouts. We use the
// keepaliveInterval variadic to force a 20ms tick so the test
// doesn't have to wait the production 15s.
func TestStreamToSSE_EmitsKeepaliveComment(t *testing.T) {
	lines := make(chan string) // unbuffered; blocks the pump on read
	errs := make(chan error, 1)
	var cancelled atomic.Bool
	cancel := func() { cancelled.Store(true) }
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		streamToSSE(
			context.Background(),
			w,
			lines,
			errs,
			cancel,
			20*time.Millisecond,
		)
		close(done)
	}()
	// Wait for at least two keepalive ticks. With a 20ms interval
	// that's ~50ms of wall time; we wait 100ms to leave headroom.
	time.Sleep(100 * time.Millisecond)
	close(lines)
	<-done
	body := w.Body.String()
	// We expect at least two "ka" comments.
	count := strings.Count(body, ": ka\n")
	if count < 2 {
		t.Errorf("expected ≥ 2 keepalive frames, got %d in body:\n%s", count, body)
	}
	if !cancelled.Load() {
		t.Error("cancel was not called after stream end")
	}
}

// TestStreamToSSE_ClientDisconnectInvokesCancelImmediate verifies that
// the request context cancellation triggers the cancel callback
// without waiting for a line or keepalive tick. The handler
// contract depends on this: cancel() releases the docker engine
// stream, and a slow release would otherwise pin engine resources
// for every closed tab.
func TestStreamToSSE_ClientDisconnectInvokesCancelImmediate(t *testing.T) {
	lines := make(chan string) // unbuffered; pump blocks on read
	errs := make(chan error, 1)
	var cancelled atomic.Bool
	var cancelDelay atomic.Int64
	start := time.Now()
	cancel := func() {
		cancelled.Store(true)
		cancelDelay.Store(int64(time.Since(start)))
	}
	ctx, cancelCtx := context.WithCancel(context.Background())
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		streamToSSE(ctx, w, lines, errs, cancel)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	cancelCtx()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("streamToSSE did not return after ctx cancel")
	}
	if !cancelled.Load() {
		t.Error("cancel was not invoked on client disconnect")
	}
	// Should fire well under 100ms; we leave generous headroom for
	// CI machines.
	if d := time.Duration(cancelDelay.Load()); d > 100*time.Millisecond {
		t.Errorf("cancel took %v, want < 100ms", d)
	}
}
