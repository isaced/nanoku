package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newManualDeployHandlers builds a Handlers with one app seeded and no Docker
// manager (the Docker daemon path is exercised in integration; here we cover
// the wiring: no panic, and the per-app deploy lock is released after the
// async executor finishes).
func newManualDeployHandlers(t *testing.T) *Handlers {
	t.Helper()
	d := newTestDB(t)
	_, err := d.App.Create().
		SetName("myapp").
		SetImage("nginx:1.27").
		SetPort(80).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	return &Handlers{
		DB:            d,
		DeployLock:    NewDeployLock(),
		CaddyfilePath: t.TempDir() + "/Caddyfile",
	}
}

func postDeploy(t *testing.T, h *Handlers, appID int) *httptest.ResponseRecorder {
	t.Helper()
	// DeployApp reads the id via r.PathValue("id"), which the real mux sets.
	// In a unit test there is no mux, so we set it explicitly. The path still
	// carries the id for anything that inspects r.URL.Path.
	req := httptest.NewRequest(http.MethodPost, "/api/apps/"+strconv.Itoa(appID)+"/deployments", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	w := httptest.NewRecorder()
	h.DeployApp(w, req)
	return w
}

// TestDeployApp_Returns202WithDeployId confirms the manual path matches the
// trigger path's response shape: a successful acquire returns 202 with
// {accepted:true, deployId}. The actual pull happens in a goroutine — the
// Deploy row transitions to failed (Docker=nil) without the response
// waiting on it.
func TestDeployApp_Returns202WithDeployId(t *testing.T) {
	h := newManualDeployHandlers(t)
	w := postDeploy(t, h, 1)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response body parse: %v", err)
	}
	if accepted, _ := resp["accepted"].(bool); !accepted {
		t.Errorf("expected accepted=true, got %v", resp)
	}
	if _, ok := resp["deployId"]; !ok {
		t.Errorf("response missing deployId: %v", resp)
	}

	// Wait for the executor goroutine to finish so the lock is released
	// before any subsequent test or shutdown observes it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !h.DeployLock.IsBusy(1) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.DeployLock.IsBusy(1) {
		t.Error("deploy lock still held after executor returned")
	}
}

// TestDeployApp_NoLockLeakOnMissingApp guards against a future reordering
// where the lock is acquired before the app lookup: a deploy on a missing
// app must not leave the per-app lock held.
func TestDeployApp_NoLockLeakOnMissingApp(t *testing.T) {
	h := newManualDeployHandlers(t)
	w := postDeploy(t, h, 9999)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (app not found)", w.Code)
	}
	if h.DeployLock.IsBusy(9999) {
		t.Error("lock acquired for a request that never reached the lock check")
	}
}

// TestDeployApp_BusyLockContract is the lock-contention contract: when the
// per-app lock is already held by an in-flight deploy, a concurrent manual
// deploy returns 202 accepted:false so the UI knows a deploy is in flight.
// This matches the trigger path's response shape exactly.
func TestDeployApp_BusyLockContract(t *testing.T) {
	h := newManualDeployHandlers(t)
	if !h.DeployLock.TryAcquire(1) {
		t.Fatal("pre-acquire failed")
	}
	defer h.DeployLock.Release(1)

	w := postDeploy(t, h, 1)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202 (deploy already in flight); body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "another deploy") {
		t.Errorf("body should mention another deploy in flight: %s", w.Body.String())
	}
	// Lock must still be held by the test (handler must not Release a lock
	// it never acquired — that would let a concurrent deploy slip through).
	if !h.DeployLock.IsBusy(1) {
		t.Error("handler released a lock it did not acquire")
	}
}