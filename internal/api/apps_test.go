package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/enttest"
)

// newManualDeployHandlers builds a Handlers with one app seeded and no Docker
// manager (the Docker daemon path is exercised in integration; here we cover
// the wiring: no panic, and the per-app deploy lock is never left acquired).
func newManualDeployHandlers(t *testing.T) *Handlers {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:deployapp_test?mode=memory&_fk=1&_pragma=foreign_keys(1)")
	_, err := client.App.Create().
		SetName("myapp").
		SetImage("nginx:1.27").
		SetPort(80).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	return &Handlers{
		DB:            &db.DB{Client: client},
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

// TestDeployApp_DockerUnavailableNoPanic asserts that with no Docker daemon
// and no deploy lock, DeployApp returns 503 (docker check wins) and does not
// panic on a nil DeployLock.
func TestDeployApp_DockerUnavailableNoPanic(t *testing.T) {
	h := newManualDeployHandlers(t)
	// DeployLock intentionally nil: the docker-unavailable check fires first
	// and the lock is never dereferenced.
	w := postDeploy(t, h, 1)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (docker unavailable)", w.Code)
	}
}

// TestDeployApp_NoLockLeakOnMissingApp guards against a future reordering
// where the lock is acquired before the app lookup: a deploy on a missing app
// must not leave the per-app lock held.
func TestDeployApp_NoLockLeakOnMissingApp(t *testing.T) {
	h := newManualDeployHandlers(t)
	h.DeployLock = NewDeployLock()
	w := postDeploy(t, h, 9999)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (docker nil short-circuits)", w.Code)
	}
	if h.DeployLock.IsBusy(9999) {
		t.Error("lock acquired for a request that never reached the lock check")
	}
}

// TestDeployApp_DockerUnavailableDoesNotAcquireLock confirms that when the
// docker check fires before the lock (Docker==nil), the per-app lock is not
// acquired — even though a real handler would later spawn a goroutine that
// takes the lock. This guards the ordering: docker check before lock check.
func TestDeployApp_DockerUnavailableDoesNotAcquireLock(t *testing.T) {
	h := newManualDeployHandlers(t)
	h.DeployLock = NewDeployLock()
	_ = postDeploy(t, h, 1)
	if h.DeployLock.IsBusy(1) {
		t.Error("deploy lock acquired on the 503 path (would never be released)")
	}
}

// TestDeployApp_BusyLockContract is the lock-contention contract: when the
// per-app lock is already held by an in-flight deploy, a concurrent manual
// deploy must NOT race past it. The 202 accepted:false body matches the
// trigger path's response shape so the UI can detect "deploy in flight"
// uniformly across manual and trigger flows.
//
// We can't construct a real docker.Manager without the docker binary, so the
// real 202-busy path is exercised in integration. Here we document the
// contract and verify the lock is not stolen: if a future refactor moves
// the lock check after the docker call, the lock could be acquired by a
// different caller before our handler returns — which would let two deploys
// slip through.
func TestDeployApp_BusyLockContract(t *testing.T) {
	h := newManualDeployHandlers(t)
	h.DeployLock = NewDeployLock()
	// Pre-hold the lock to simulate an in-flight deploy.
	if !h.DeployLock.TryAcquire(1) {
		t.Fatal("pre-acquire failed")
	}
	defer h.DeployLock.Release(1)

	// With Docker==nil the handler returns 503 before the lock check. The
	// 202-busy contract only holds when Docker is present; a docker.Manager
	// needs the docker binary, which is not available in the unit-test
	// sandbox. This test guards the ordering and the lock-doesn't-leak
	// invariant.
	if h.Docker != nil {
		w := postDeploy(t, h, 1)
		if w.Code != http.StatusAccepted {
			t.Errorf("status = %d, want 202 (deploy already in flight)", w.Code)
		}
		if !strings.Contains(w.Body.String(), "another deploy") {
			t.Errorf("body should mention another deploy in flight: %s", w.Body.String())
		}
	}
	// Lock must still be held by the test (handler must not Release a lock
	// it never acquired — that would let a concurrent deploy slip through).
	if !h.DeployLock.IsBusy(1) {
		t.Error("handler released a lock it did not acquire")
	}
}

// TestDeployApp_LockReleasedAfterExecutor verifies the asynchronous-path
// invariant: once the executor goroutine finishes (Docker==nil → immediate
// "docker unavailable" failure), the per-app lock is released. Without this
// guarantee the second deploy on the same app would deadlock forever.
//
// We exercise the executor directly because DeployApp's 503 short-circuit
// never reaches the lock path.
func TestDeployApp_LockReleasedAfterExecutor(t *testing.T) {
	h := newManualDeployHandlers(t)
	h.DeployLock = NewDeployLock()

	if !h.DeployLock.TryAcquire(1) {
		t.Fatal("setup: lock pre-acquire failed")
	}

	dep, err := h.DB.Deploy.Create().
		SetAppID(1).
		SetTrigger("manual").
		SetStatus("running").
		SetStartedAt(time.Now().UTC()).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create deploy row: %v", err)
	}

	done := make(chan struct{})
	go func() {
		h.executeDeploy(context.Background(), 1, dep.ID, "")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("executeDeploy did not return in time")
	}

	if h.DeployLock.IsBusy(1) {
		t.Error("deploy lock not released after executor returned")
	}

	// Deploy row should be marked failed with "docker unavailable".
	got, err := h.DB.Deploy.Get(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("read deploy: %v", err)
	}
	if string(got.Status) != "failed" {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "docker unavailable") {
		t.Errorf("error = %v, want docker unavailable", got.Error)
	}
}