package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// sseHeaders sets the standard text/event-stream response headers and
// disables any proxy buffering. The Content-Type must be set before
// WriteHeader is called.
func sseHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
}

// defaultKeepalive is the period between SSE comment ("ka") frames
// that we emit so proxies don't idle the connection out. 15s is well
// under nginx's default 60s proxy_read_timeout and matches the
// convention used by SSE libraries like the EventSource polyfills.
const defaultKeepalive = 15 * time.Second

// sseWriter wraps the response writer with a bufio.Writer so each
// Flush produces a single small TCP write. Without buffering, the
// per-line WriteString + Flush pair would emit a separate write per
// line; with it, all lines in a tick are coalesced into one packet.
type sseWriter struct {
	bw *bufio.Writer
	w  http.ResponseWriter
}

func newSSEWriter(w http.ResponseWriter) *sseWriter {
	return &sseWriter{bw: bufio.NewWriterSize(w, 4096), w: w}
}

func (s *sseWriter) Flush() error {
	return s.bw.Flush()
}

// sseEvent writes a single named event. data may contain newlines;
// they are split into multiple data: lines per the SSE spec.
func (s *sseWriter) sseEvent(event, data string) error {
	if event != "" {
		if _, err := fmt.Fprintf(s.bw, "event: %s\n", event); err != nil {
			return err
		}
	}
	for _, line := range strings.Split(data, "\n") {
		if _, err := fmt.Fprintf(s.bw, "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := s.bw.WriteString("\n"); err != nil {
		return err
	}
	return s.Flush()
}

// sseComment writes an SSE comment line. Useful as a periodic
// keepalive so the connection doesn't idle out behind a proxy.
func (s *sseWriter) sseComment(text string) error {
	if _, err := fmt.Fprintf(s.bw, ": %s\n\n", text); err != nil {
		return err
	}
	return s.Flush()
}

// sseRetry sets the browser-side reconnect delay.
func (s *sseWriter) sseRetry(ms int) error {
	_, err := fmt.Fprintf(s.bw, "retry: %d\n\n", ms)
	if err != nil {
		return err
	}
	return s.Flush()
}

// streamToSSE is the shared pump that copies line events from a
// LogStream (or any chan string) into SSE events. It exits when:
//
//   - the source channels are closed (natural EOF),
//   - the request context is canceled (client disconnected), or
//   - the writer fails (broken pipe).
//
// On exit it always calls cancel to release the underlying Docker
// engine stream. keepaliveInterval is exposed for tests so the
// "ka" frame can be verified without a 15-second wait; production
// callers should pass 0 to get the default.
func streamToSSE(
	ctx context.Context,
	w http.ResponseWriter,
	lines <-chan string,
	errs <-chan error,
	cancel func(),
	keepaliveInterval ...time.Duration,
) {
	sw := newSSEWriter(w)
	defer cancel()

	// Browser-side retry: 2s. Short enough that a brief proxy
	// hiccup recovers quickly; long enough that a server crash
	// doesn't get reconnected to in a tight loop.
	_ = sw.sseRetry(2000)
	_ = sw.sseComment("nanoku log stream")

	keepalive := defaultKeepalive
	if len(keepaliveInterval) > 0 && keepaliveInterval[0] > 0 {
		keepalive = keepaliveInterval[0]
	}
	ticker := time.NewTicker(keepalive)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Client disconnect / server shutdown. Wrap up; don't
			// try to write a final event because the connection is
			// gone.
			return
		case line, ok := <-lines:
			if !ok {
				// Lines channel closed. Drain any pending error
				// (the read goroutine defers close(lines) before
				// close(errs) — so a queued error may still be in
				// the errs buffer when we see lines close). We
				// prefer surfacing an error event over a clean
				// end-of-stream comment so the UI shows the cause.
				select {
				case err, eok := <-errs:
					if eok && err != nil {
						_ = sw.sseEvent("log-error", err.Error())
					}
				default:
				}
				_ = sw.sseComment("nanoku log stream end")
				return
			}
			if err := sw.sseEvent("line", line); err != nil {
				return
			}
		case err, ok := <-errs:
			if ok && err != nil {
				// Don't block on write if the client is already
				// gone. Event name is `log-error` to avoid
				// colliding with the browser's built-in `error`
				// event on EventSource (which fires for
				// connection-level problems; we want a separate,
				// server-controlled signal).
				if wErr := sw.sseEvent("log-error", err.Error()); wErr != nil {
					return
				}
			}
			// Either way, end-of-stream — return so cancel() runs.
			return
		case <-ticker.C:
			if err := sw.sseComment("ka"); err != nil {
				return
			}
		}
	}
}
