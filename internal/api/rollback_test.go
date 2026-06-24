package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/deploy"
)

// rollbackSeededApp creates an app with two successful deploys (each with an
// image snapshot). Returns the app, the older deploy id (rollback target),
// and the newer deploy id.
func rollbackSeededApp(t *testing.T, h *Handlers) (*db.App, int, int) {
	t.Helper()
	ctx := context.Background()
	a, err := h.DB.App.Create().
		SetName("rb-app").
		SetImage("nginx:1.27").
		SetPort(80).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	older, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus(deploy.StatusSuccess).
		SetImage("nginx:1.26").
		SetCommitSha("aaaaaaa").
		SetStartedAt(time.Now().UTC()).
		SetFinishedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed older deploy: %v", err)
	}
	newer, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus(deploy.StatusSuccess).
		SetImage("nginx:1.27").
		SetCommitSha("bbbbbbb").
		SetStartedAt(time.Now().UTC()).
		SetFinishedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed newer deploy: %v", err)
	}
	return a, older.ID, newer.ID
}

func postRollback(t *testing.T, h *Handlers, appID, deployID int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(RollbackRequest{DeployID: deployID})
	req := httptest.NewRequest(http.MethodPost,
		"/api/apps/"+itoa(appID)+"/rollback",
		bytes.NewReader(body))
	req.SetPathValue("id", itoa(appID))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.RollbackApp(w, req)
	return w
}

func itoa(n int) string {
	return jsonNumber(n)
}

// jsonNumber avoids pulling strconv into every test helper; only used in
// path segments. (Tests in this file have to embed numbers in URLs.)
func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestRollback_HappyPath(t *testing.T) {
	h := newManualDeployHandlers(t)
	a, targetID, _ := rollbackSeededApp(t, h)

	w := postRollback(t, h, a.ID, targetID)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted, _ := resp["accepted"].(bool); !accepted {
		t.Errorf("accepted = false; resp=%v", resp)
	}
	if _, ok := resp["deployId"]; !ok {
		t.Errorf("missing deployId; resp=%v", resp)
	}
	if rbFrom, _ := resp["rolledBackFrom"].(float64); int(rbFrom) != targetID {
		t.Errorf("rolledBackFrom = %v, want %d", rbFrom, targetID)
	}

	ctx := context.Background()
	// Target deploy must now be marked rolled_back.
	target, _ := h.DB.Deploy.Get(ctx, targetID)
	if target.Status != deploy.StatusRolledBack {
		t.Errorf("target deploy status = %s, want rolled_back", target.Status)
	}
	// New rollback deploy must exist with the right image + trigger.
	deps, err := h.DB.Deploy.Query().
		Where(deploy.HasAppWith(app.IDEQ(a.ID)), deploy.TriggerEQ(deploy.TriggerRollback)).
		All(ctx)
	if err != nil {
		t.Fatalf("query rollback deploys: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("rollback deploy count = %d, want 1", len(deps))
	}
	if deps[0].Image == nil || *deps[0].Image != "nginx:1.26" {
		t.Errorf("rollback deploy image = %v, want nginx:1.26", deps[0].Image)
	}
}

func TestRollback_DeployNotFound(t *testing.T) {
	h := newManualDeployHandlers(t)
	a, _, _ := rollbackSeededApp(t, h)

	w := postRollback(t, h, a.ID, 9999)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestRollback_DeployBelongsToDifferentApp(t *testing.T) {
	h := newManualDeployHandlers(t)
	_, _, targetID := rollbackSeededApp(t, h)
	// Create a different app and try to roll back to targetID (which belongs
	// to the original app). Expect 404 — the deploy simply isn't visible
	// from this app.
	other, err := h.DB.App.Create().
		SetName("other-app").
		SetImage("nginx:1.27").
		SetPort(80).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed other: %v", err)
	}
	w := postRollback(t, h, other.ID, targetID)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (deploy belongs to a different app)", w.Code)
	}
}

func TestRollback_FailedTargetRejected(t *testing.T) {
	h := newManualDeployHandlers(t)
	a, _, _ := rollbackSeededApp(t, h)
	failed, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus(deploy.StatusFailed).
		SetImage("nginx:1.25").
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed failed deploy: %v", err)
	}

	w := postRollback(t, h, a.ID, failed.ID)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestRollback_NoImageSnapshotRejected(t *testing.T) {
	h := newManualDeployHandlers(t)
	a, _, _ := rollbackSeededApp(t, h)
	legacy, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus(deploy.StatusSuccess).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed legacy deploy: %v", err)
	}

	w := postRollback(t, h, a.ID, legacy.ID)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (no image snapshot)", w.Code)
	}
}

func TestRollback_ComposeModeRejected(t *testing.T) {
	h := newManualDeployHandlers(t)
	ctx := context.Background()
	a, err := h.DB.App.Create().
		SetName("compose-app").
		SetDeployMethod("compose").
		SetComposeContent("services: {}\n").
		SetPort(80).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	target, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus(deploy.StatusSuccess).
		SetImage("nginx:1.27").
		Save(ctx)
	if err != nil {
		t.Fatalf("seed deploy: %v", err)
	}

	w := postRollback(t, h, a.ID, target.ID)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (compose mode not yet supported)", w.Code)
	}
}

func TestRollback_BusyLockReturns202(t *testing.T) {
	h := newManualDeployHandlers(t)
	a, targetID, _ := rollbackSeededApp(t, h)

	// Pre-acquire the per-app lock to simulate an in-flight deploy.
	if !h.DeployLock.TryAcquire(a.ID) {
		t.Fatal("setup: lock pre-acquire failed")
	}
	defer h.DeployLock.Release(a.ID)

	w := postRollback(t, h, a.ID, targetID)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("another deploy")) {
		t.Errorf("body should mention another deploy in flight: %s", w.Body.String())
	}
	// Critically, the rollback must NOT have marked the target as rolled_back
	// if it didn't actually run.
	dep, _ := h.DB.Deploy.Get(context.Background(), targetID)
	if dep.Status == deploy.StatusRolledBack {
		t.Errorf("target should NOT be marked rolled_back when rollback was rejected")
	}
}

func TestRollback_InvalidRequestBody(t *testing.T) {
	h := newManualDeployHandlers(t)
	a, _, _ := rollbackSeededApp(t, h)

	for name, body := range map[string][]byte{
		"missing deployId":  []byte(`{}`),
		"zero deployId":     []byte(`{"deployId":0}`),
		"negative deployId": []byte(`{"deployId":-1}`),
		"empty body":        nil,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost,
				"/api/apps/"+itoa(a.ID)+"/rollback",
				bytes.NewReader(body))
			req.SetPathValue("id", itoa(a.ID))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.RollbackApp(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("body=%s status=%d, want 400", name, w.Code)
			}
		})
	}
}