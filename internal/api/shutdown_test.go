package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// withShutdownWG attaches a fresh WaitGroup to the handler so DrainInflight
// has something to wait on. Returns the WG so the test can manually Add to
// it to simulate in-flight work.
func withShutdownWG(t *testing.T, h *Handlers) *sync.WaitGroup {
	t.Helper()
	var wg sync.WaitGroup
	h.ShutdownWG = &wg
	return &wg
}

// fakeInflight simulates the bookkeeping the real executor does on entry
// (add to inflight map + bump WG) without running any of the deploy work.
// Cleanup mirrors the executor's deferred teardown.
func fakeInflight(t *testing.T, h *Handlers, wg *sync.WaitGroup, depID, appID int) {
	t.Helper()
	h.inflightMu.Lock()
	if h.inflight == nil {
		h.inflight = make(map[int]int)
	}
	h.inflight[depID] = appID
	h.inflightMu.Unlock()
	wg.Add(1)
}

func TestDrainInflight_NoWGIsNoop(t *testing.T) {
	h, a := newExecutorHandlers(t)
	// ShutdownWG intentionally nil.
	h.DrainInflight(context.Background(), 10*time.Millisecond)
	// Sanity: nothing was created or modified.
	if _, err := h.DB.App.Get(context.Background(), a.ID); err != nil {
		t.Fatalf("app unexpectedly missing: %v", err)
	}
}

func TestDrainInflight_WaitsForCleanCompletion(t *testing.T) {
	h, a := newExecutorHandlers(t)
	wg := withShutdownWG(t, h)
	depID := seedRunningDeploy(t, h, a.ID)

	fakeInflight(t, h, wg, depID, a.ID)

	// Simulate an executor that finishes quickly with success.
	go func() {
		defer wg.Done()
		defer func() {
			h.inflightMu.Lock()
			delete(h.inflight, depID)
			h.inflightMu.Unlock()
		}()
		time.Sleep(20 * time.Millisecond)
		_, _ = h.DB.Deploy.UpdateOneID(depID).
			SetStatus("success").
			SetFinishedAt(time.Now().UTC()).
			Save(context.Background())
	}()

	start := time.Now()
	h.DrainInflight(context.Background(), 2*time.Second)
	elapsed := time.Since(start)

	if elapsed >= 2*time.Second {
		t.Errorf("DrainInflight blocked for the full timeout (%s); should have returned on WG completion", elapsed)
	}

	status, _ := readDeployError(t, h, depID)
	if status != "success" {
		t.Errorf("status = %s, want success (executor should have been allowed to finish)", status)
	}
}

func TestDrainInflight_MarksStragglersFailed(t *testing.T) {
	h, a := newExecutorHandlers(t)
	wg := withShutdownWG(t, h)
	depID := seedRunningDeploy(t, h, a.ID)

	fakeInflight(t, h, wg, depID, a.ID)
	// Note: wg.Done() is intentionally never called — simulates a stuck
	// executor (e.g. a docker pull that never returns).
	t.Cleanup(func() { wg.Done() })

	h.DrainInflight(context.Background(), 50*time.Millisecond)

	status, msg := readDeployError(t, h, depID)
	if status != "failed" {
		t.Errorf("status = %s, want failed", status)
	}
	if !strings.Contains(msg, "shutting down") {
		t.Errorf("error = %q, want contains 'shutting down'", msg)
	}
}

func TestDrainInflight_MarksMultipleStragglers(t *testing.T) {
	h, a := newExecutorHandlers(t)
	wg := withShutdownWG(t, h)

	dep1 := seedRunningDeploy(t, h, a.ID)
	dep2 := seedRunningDeploy(t, h, a.ID)
	fakeInflight(t, h, wg, dep1, a.ID)
	fakeInflight(t, h, wg, dep2, a.ID)
	t.Cleanup(func() { wg.Done(); wg.Done() })

	h.DrainInflight(context.Background(), 50*time.Millisecond)

	for _, depID := range []int{dep1, dep2} {
		status, msg := readDeployError(t, h, depID)
		if status != "failed" {
			t.Errorf("dep %d status = %s, want failed", depID, status)
		}
		if !strings.Contains(msg, "shutting down") {
			t.Errorf("dep %d error = %q, want contains 'shutting down'", depID, msg)
		}
	}
}

func TestDrainInflight_ContextCancelBeatsTimeout(t *testing.T) {
	h, a := newExecutorHandlers(t)
	wg := withShutdownWG(t, h)
	depID := seedRunningDeploy(t, h, a.ID)

	fakeInflight(t, h, wg, depID, a.ID)
	t.Cleanup(func() { wg.Done() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	// drainTimeout is 5s but ctx fires first — should still mark failed.
	h.DrainInflight(ctx, 5*time.Second)

	status, _ := readDeployError(t, h, depID)
	if status != "failed" {
		t.Errorf("status = %s, want failed", status)
	}
}

// TestDrainInflight_MixedDrainOnlyFailsStragglers covers the realistic
// production case: some in-flight deploys finish during the drain window,
// others are stuck. Only the stuck ones should be marked failed — the
// fast ones keep their real terminal status written by their own executor.
func TestDrainInflight_MixedDrainOnlyFailsStragglers(t *testing.T) {
	h, a := newExecutorHandlers(t)
	wg := withShutdownWG(t, h)

	fastDep := seedRunningDeploy(t, h, a.ID)
	slowDep := seedRunningDeploy(t, h, a.ID)
	fakeInflight(t, h, wg, fastDep, a.ID)
	fakeInflight(t, h, wg, slowDep, a.ID)
	// Stuck executor never calls Done; the fast one does.
	t.Cleanup(func() { wg.Done() })

	// Fast executor: finishes well inside the drain window with success.
	go func() {
		defer wg.Done()
		defer func() {
			h.inflightMu.Lock()
			delete(h.inflight, fastDep)
			h.inflightMu.Unlock()
		}()
		time.Sleep(20 * time.Millisecond)
		_, _ = h.DB.Deploy.UpdateOneID(fastDep).
			SetStatus("success").
			SetFinishedAt(time.Now().UTC()).
			Save(context.Background())
	}()

	h.DrainInflight(context.Background(), 100*time.Millisecond)

	fastStatus, _ := readDeployError(t, h, fastDep)
	if fastStatus != "success" {
		t.Errorf("fast dep status = %s, want success (should not have been touched by drain)", fastStatus)
	}

	slowStatus, slowMsg := readDeployError(t, h, slowDep)
	if slowStatus != "failed" {
		t.Errorf("slow dep status = %s, want failed", slowStatus)
	}
	if !strings.Contains(slowMsg, "shutting down") {
		t.Errorf("slow dep error = %q, want contains 'shutting down'", slowMsg)
	}
}

// TestDrainInflight_LateExecutorWriteWins verifies the documented design
// behavior in shutdown.go: a straggler that finishes *after* the drain
// timeout and writes its real outcome (here: success) overwrites the
// drain's placeholder "failed" status. The real outcome is the final
// state — the placeholder is best-effort and only matters if the
// executor never reports back at all.
func TestDrainInflight_LateExecutorWriteWins(t *testing.T) {
	h, a := newExecutorHandlers(t)
	wg := withShutdownWG(t, h)
	depID := seedRunningDeploy(t, h, a.ID)

	fakeInflight(t, h, wg, depID, a.ID)
	// No t.Cleanup(wg.Done()): the executor goroutine below is
	// guaranteed to finish (we wait on executorDone), and a Cleanup
	// Done would double-decrement and panic.

	executorDone := make(chan struct{})
	go func() {
		defer close(executorDone)
		defer wg.Done()
		defer func() {
			h.inflightMu.Lock()
			delete(h.inflight, depID)
			h.inflightMu.Unlock()
		}()
		// Sleep longer than the drain timeout so drain gives up first
		// and marks the deploy failed as a placeholder.
		time.Sleep(150 * time.Millisecond)
		_, _ = h.DB.Deploy.UpdateOneID(depID).
			SetStatus("success").
			SetFinishedAt(time.Now().UTC()).
			Save(context.Background())
	}()

	h.DrainInflight(context.Background(), 30*time.Millisecond)
	<-executorDone

	status, _ := readDeployError(t, h, depID)
	if status != "success" {
		t.Errorf("status = %s, want success (executor final write should win over drain placeholder)", status)
	}
}
