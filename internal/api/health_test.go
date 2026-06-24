package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeHealth(t *testing.T, body []byte) HealthResponse {
	t.Helper()
	var out HealthResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (raw=%q)", err, string(body))
	}
	return out
}

func TestHealthz_OK_DBOnly(t *testing.T) {
	h := &Handlers{DB: newTestDB(t), SkipCaddyReload: true}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.Healthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	resp := decodeHealth(t, w.Body.Bytes())
	if resp.Status != "ok" {
		t.Errorf("Status = %q, want ok", resp.Status)
	}
	if !resp.Checks["db"].OK {
		t.Errorf("db check not ok: %+v", resp.Checks["db"])
	}
	if _, present := resp.Checks["docker"]; present {
		t.Errorf("docker check should be absent in SkipCaddyReload mode")
	}
}

func TestHealthz_DegradedWhenDBClosed(t *testing.T) {
	d := newTestDB(t)
	// Close the DB so SELECT 1 fails. Handler must report 503 with the
	// db check's error text in the body for operator debugging.
	_ = d.Close()
	h := &Handlers{DB: d}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.Healthz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	resp := decodeHealth(t, w.Body.Bytes())
	if resp.Status != "degraded" {
		t.Errorf("Status = %q, want degraded", resp.Status)
	}
	if resp.Checks["db"].OK {
		t.Errorf("db check should be failing: %+v", resp.Checks["db"])
	}
	if resp.Checks["db"].Error == "" {
		t.Errorf("db check should include error text for operators")
	}
}

func TestHealthz_DegradedWhenDockerMissing(t *testing.T) {
	// SkipCaddyReload=false + Docker=nil → docker check must fail and the
	// top-level status must be degraded (503). This is the typical "user
	// enabled the managed Caddy mode but the docker daemon is down" state.
	h := &Handlers{DB: newTestDB(t), SkipCaddyReload: false, Docker: nil}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.Healthz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	resp := decodeHealth(t, w.Body.Bytes())
	if resp.Status != "degraded" {
		t.Errorf("Status = %q, want degraded", resp.Status)
	}
	docker, ok := resp.Checks["docker"]
	if !ok {
		t.Fatalf("docker check missing: %+v", resp.Checks)
	}
	if docker.OK {
		t.Errorf("docker check should fail when manager is nil")
	}
}

func TestHealthz_DBThenDockerIndependent(t *testing.T) {
	// Both checks failing should still return 503 with both errors visible.
	// Operators triage from the body — having only the first failure
	// surface would force a curl-the-second-time loop to see what's wrong.
	d := newTestDB(t)
	_ = d.Close()
	h := &Handlers{DB: d, SkipCaddyReload: false, Docker: nil}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.Healthz(w, req)

	resp := decodeHealth(t, w.Body.Bytes())
	if resp.Checks["db"].OK || resp.Checks["docker"].OK {
		t.Errorf("both checks should fail: %+v", resp.Checks)
	}
}

func TestHealthz_NilDBHandler(t *testing.T) {
	// A handler constructed without a DB (shouldn't happen in production,
	// but defensive) must report the missing dependency, not panic.
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.Healthz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	resp := decodeHealth(t, w.Body.Bytes())
	if resp.Checks["db"].OK {
		t.Errorf("db check should fail when DB is nil")
	}
}

func TestHealthz_JSONShape(t *testing.T) {
	// Pin the JSON shape — orchestrators and the dashboard both depend on
	// the field names being stable.
	h := &Handlers{DB: newTestDB(t), SkipCaddyReload: true}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.Healthz(w, req)

	body := w.Body.String()
	for _, key := range []string{`"status":"ok"`, `"checks":{`, `"db":{`, `"ok":true`} {
		if !containsString(body, key) {
			t.Errorf("body missing %s\nbody: %s", key, body)
		}
	}
}