package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/isaced/nanoku/internal/db/deploy"
)

// DeployLogStream serves the deploy log via SSE. The handler:
//
//   - validates the appID + deployID pair (deploy must belong to app;
//     otherwise an attacker can probe arbitrary deploy rows by ID);
//   - confirms the deploy's log file exists (404 if the worker
//     never wrote anything — e.g. an in-flight deploy that failed
//     before the first append);
//   - replays the file from offset 0 first so the UI gets the full
//     history, even if it opened the panel after the deploy
//     finished;
//   - keeps tailing with a 1s poll while the deploy worker is
//     still in flight (tracked via the in-memory inflight map);
//   - emits a terminal `end` event when the worker has finished,
//     so the browser's EventSource doesn't auto-reconnect into a
//     replay loop.
//
// Why poll instead of inotify/fsnotify: deploy logs are
// operator-facing, not interactive. A 1s latency on new lines is
// invisible next to a multi-minute pull, and avoiding a
// filesystem-watcher dependency keeps the binary lean and the
// test surface flat. The 1s tick is also where we re-check
// IsDeployInFlight, so the file size alone doesn't drive the
// stop condition.
func (h *Handlers) DeployLogStream(w http.ResponseWriter, r *http.Request) {
	appID, ok := pathIntID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid app id"))
		return
	}
	deployID, err := strconv.Atoi(r.PathValue("did"))
	if err != nil || deployID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("invalid deploy id"))
		return
	}
	// Authorize: the deploy row must belong to this app. We follow
	// the app edge (a JOIN) so a wrong appID in the URL doesn't
	// return another app's deploy log.
	dep, err := h.DB.Deploy.Get(r.Context(), deployID)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("deploy not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	depAppID, qerr := dep.QueryApp().OnlyID(r.Context())
	if qerr != nil {
		writeErr(w, http.StatusNotFound, errors.New("deploy not found for this app"))
		return
	}
	if depAppID != appID {
		writeErr(w, http.StatusNotFound, errors.New("deploy not found for this app"))
		return
	}
	if h.DeployLogs == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("deploy log store not initialized"))
		return
	}
	// Refuse up front when the file doesn't exist so the client
	// gets a clean 404 instead of an SSE stream that immediately
	// emits `end` (which the EventSource auto-reconnect would
	// turn into a loop, since we'd happily tell the next
	// connection "yes, this deploy is done, no lines").
	if _, err := h.DeployLogs.readAll(deployID); err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, errors.New("deploy log not available"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	sseHeaders(w)
	w.WriteHeader(http.StatusOK)
	streamDeployLog(r.Context(), w, h, deployID)
}

// streamDeployLog is the SSE pump. It replays the file then tails
// it until the deploy worker exits (IsDeployInFlight flips to
// false). The poll interval is the same as the keepalive — the
// keepalive is what tells the client the connection is alive
// during a quiet deploy (no new lines for many seconds while a
// big image layers is downloading).
func streamDeployLog(
	ctx context.Context,
	w http.ResponseWriter,
	h *Handlers,
	deployID int,
) {
	sw := newSSEWriter(w)
	_ = sw.sseRetry(2000)
	_ = sw.sseComment("nanoku deploy log stream")

	next, err := h.DeployLogs.tail(deployID)
	if err != nil {
		// File disappeared between the readAll existence check
		// and the tail open (very unlikely — the worker only
		// appends). Surface the end marker so the client closes
		// cleanly.
		_ = sw.sseEvent("end", "nanoku deploy log stream end")
		return
	}

	// Initial dump: read everything from offset 0. If the deploy
	// is already terminal (worker not in flight), one call is
	// enough and we emit `end` immediately.
	if !emitTail(sw, next) {
		return
	}
	if !h.IsDeployInFlight(deployID) {
		_ = sw.sseEvent("end", "nanoku deploy log stream end")
		return
	}

	// Live tail loop. We coalesce new lines into one read per
	// tick to avoid hammering the file when a chatty compose
	// progress stream writes a burst between polls.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	tail := time.NewTicker(1 * time.Second)
	defer tail.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tail.C:
			if !emitTail(sw, next) {
				return
			}
			if !h.IsDeployInFlight(deployID) {
				_ = sw.sseEvent("end", "nanoku deploy log stream end")
				return
			}
		case <-keepalive.C:
			if err := sw.sseComment("ka"); err != nil {
				return
			}
		}
	}
}

// emitTail calls next() once and emits every returned line as
// the correct SSE event. Returns false if the client connection
// has died (write error) so the caller can stop the loop.
func emitTail(sw *sseWriter, next func() ([]deployLogReadLine, error)) bool {
	lines, err := next()
	if err != nil {
		// A read error mid-tail is fatal for this stream but
		// shouldn't crash the server. Best we can do is emit
		// `end` and let the client move on; the operator
		// already has the lines that were emitted so far.
		_ = sw.sseEvent("end", "nanoku deploy log stream end")
		return false
	}
	for _, l := range lines {
		if err := writeLogLine(sw, l); err != nil {
			return false
		}
	}
	return true
}

// writeLogLine emits one deploy log line as the correct SSE event:
// `line-replace` (data = "key\tmsg") when the line carries a
// replacement key, or plain `line` (data = msg) when it doesn't.
// The frontend's `line-replace` handler splits on the first tab
// to recover (key, msg) and updates the matching row in place.
//
// The wire shape here is what the old in-memory hub emitted too,
// so the frontend (useLogStream.ts) needs no changes.
func writeLogLine(sw *sseWriter, l deployLogReadLine) error {
	if l.Key != "" {
		return sw.sseEvent("line-replace", l.Key+"\t"+l.Msg)
	}
	return sw.sseEvent("line", l.Msg)
}

// isTerminalStatus returns true if a deploy row is in a state
// that won't change without explicit intervention. Kept for
// callers that want to decide whether to wait for the hub
// equivalent (e.g. tests). The file-based handler doesn't need
// it — IsDeployInFlight is the source of truth for "is the
// worker still writing".
func isTerminalStatus(s string) bool {
	switch deploy.Status(s) {
	case deploy.StatusSuccess, deploy.StatusFailed, deploy.StatusRolledBack:
		return true
	}
	return false
}
