package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/isaced/nanoku/internal/db/deploy"
)

// DeployLogStream serves the live deploy log via SSE. The handler:
//
//   - validates the appID + deployID pair (deploy must belong to app;
//     otherwise an attacker can probe arbitrary deploy rows by ID);
//   - subscribes to the in-process deploy log hub;
//   - replays the bounded history first so the UI gets the start of
//     the deploy even if it opened the panel a few seconds late;
//   - streams live lines until the deploy reaches a terminal state.
//
// We don't need a tail parameter (unlike container logs): deploys are
// short, the hub already keeps a bounded replay, and the operator
// always wants the full feed.
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
	// Authorize: the deploy row must belong to this app. We follow the
	// app edge (a JOIN) so a wrong appID in the URL doesn't return
	// another app's deploy log.
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
		writeErr(w, http.StatusServiceUnavailable, errors.New("deploy log hub not initialized"))
		return
	}
	history, live, done, unsub, ok := h.DeployLogs.subscribe(r.Context(), deployID)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, errors.New("deploy log stream unavailable"))
		return
	}
	sseHeaders(w)
	w.WriteHeader(http.StatusOK)
	defer unsub()
	streamDeployLog(r.Context(), w, history, live, done, string(dep.Status))
}

// streamDeployLog pumps history + live lines into SSE events. The
// `terminalStatus` argument is the deploy row's status at subscribe
// time; if it's already terminal (deploy finished before the user
// opened the panel) we replay history and exit without waiting on
// the done channel.
func streamDeployLog(
	ctx context.Context,
	w http.ResponseWriter,
	history []deployLogLine,
	live <-chan deployLogLine,
	done <-chan struct{},
	terminalStatus string,
) {
	sw := newSSEWriter(w)
	_ = sw.sseRetry(2000)
	_ = sw.sseComment("nanoku deploy log stream")

	// Snapshot whether the deploy is already terminal at subscribe
	// time. If it is, we'll drain the live channel until the publisher
	// closes it (post-markTerminal) and then exit; we don't need to
	// wait for `done` because the publisher closes `done` first and
	// then closes the live channel.
	terminalNow := isTerminalStatus(terminalStatus)

	// Drain any pre-existing lines from `live` that arrived between
	// our subscribe() call and now. Without this we'd see them in
	// order with the history (good) but possibly after a small delay
	// (bad). A 5ms budget is plenty for the publisher to settle.
	drainPre := func() []deployLogLine {
		out := make([]deployLogLine, 0, 8)
		t := time.NewTimer(5 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case l, ok := <-live:
				if !ok {
					return out
				}
				out = append(out, l)
			case <-t.C:
				return out
			}
		}
	}

	preDrained := drainPre()
	all := append(append([]deployLogLine{}, history...), preDrained...)
	for _, l := range all {
		if err := sw.sseEvent("line", l.msg); err != nil {
			return
		}
	}

	if terminalNow {
		// The deploy is done. Drain whatever the publisher already
		// queued into the live channel (it may have flushed lines
		// between subscribe and the terminal-mark), then exit.
		drainClosed(live, func(s string) bool {
			return sw.sseEvent("line", s) == nil
		})
		_ = sw.sseComment("nanoku deploy log stream end")
		return
	}

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case l, ok := <-live:
			if !ok {
				_ = sw.sseComment("nanoku deploy log stream end")
				return
			}
			if err := sw.sseEvent("line", l.msg); err != nil {
				return
			}
		case <-done:
			// Deploy reached a terminal status. Drain the live channel
			// (the publisher is still allowed to flush a few final
			// lines before closing it), then exit.
			drainClosed(live, func(s string) bool {
				return sw.sseEvent("line", s) == nil
			})
			_ = sw.sseComment("nanoku deploy log stream end")
			return
		case <-keepalive.C:
			if err := sw.sseComment("ka"); err != nil {
				return
			}
		}
	}
}

// drainClosed reads from ch until it closes, calling write for each
// value. Stops early if write returns false (consumer gone).
func drainClosed(ch <-chan deployLogLine, write func(string) bool) {
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				return
			}
			if !write(l.msg) {
				return
			}
		default:
			return
		}
	}
}

// isTerminalStatus returns true if a deploy row is in a state that
// won't change without explicit intervention. Used to decide whether
// to wait for the hub's done channel.
func isTerminalStatus(s string) bool {
	switch deploy.Status(s) {
	case deploy.StatusSuccess, deploy.StatusFailed, deploy.StatusRolledBack:
		return true
	}
	return false
}
