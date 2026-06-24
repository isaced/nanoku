package api

import (
	"context"
	"errors"
	"log"
	"time"
)

// DrainInflight waits for in-flight deploy goroutines to finish, up to
// drainTimeout. Returns when either the WaitGroup reaches zero (clean drain)
// or the timeout/cancel fires (stragglers).
//
// On timeout, every Deploy row still in the inflight map is marked failed
// with a "server shutting down" error so the UI doesn't show ghost "running"
// entries after a restart. The map is replaced (under the same lock) so the
// executor's deferred delete lands on a fresh, empty map and can't undo the
// fail. A late-finishing executor that subsequently writes its real status
// will overwrite the placeholder — that's the desired behavior: the real
// outcome is the final state.
//
// No-op when ShutdownWG is nil (unit tests, dry-run mode).
func (h *Handlers) DrainInflight(ctx context.Context, drainTimeout time.Duration) {
	if h.ShutdownWG == nil {
		return
	}

	done := make(chan struct{})
	go func() {
		h.ShutdownWG.Wait()
		close(done)
	}()

	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()

	select {
	case <-done:
		return
	case <-ctx.Done():
	case <-timer.C:
	}

	h.inflightMu.Lock()
	stragglers := make(map[int]int, len(h.inflight))
	for depID, appID := range h.inflight {
		stragglers[depID] = appID
	}
	h.inflight = make(map[int]int)
	h.inflightMu.Unlock()

	if len(stragglers) == 0 {
		return
	}

	log.Printf("shutdown: %d in-flight deploy(s) did not finish within %s; marking failed", len(stragglers), drainTimeout)
	for depID := range stragglers {
		h.markDeployFailed(context.Background(), depID, errors.New("server shutting down: deploy aborted"))
	}
}
