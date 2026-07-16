package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRouter_PublicLoginReachable pins the unauthenticated POST
// /api/login surface: anyone can hit it (the handler does the
// credential check itself). Without this, an auth-wrap regression
// would lock the operator out of the UI on a fresh install.
func TestRouter_PublicLoginReachable(t *testing.T) {
	h := &Handlers{
		DB:       newTestDB(t),
		Secret:   newTestSealer(t),
		Sessions: newSessionStoreForTest(t, h_unused{}),
	}
	r := Router(h, h.Sessions)
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// 400 (bad credentials) or 401 — anything but 404 "no route"
	// proves the endpoint is registered and not gated.
	if w.Code == http.StatusNotFound {
		t.Fatalf("login route missing: %d %q", w.Code, w.Body.String())
	}
}

// TestRouter_AuthGatesAdminSurface is the other half: every /api
// path other than login/logout/trigger must require a session.
// We hit /api/sites without a session and expect 401, not the
// actual list (200) and not "no route" (404).
func TestRouter_AuthGatesAdminSurface(t *testing.T) {
	h := &Handlers{
		DB:       newTestDB(t),
		Secret:   newTestSealer(t),
		Sessions: newSessionStoreForTest(t, h_unused{}),
	}
	r := Router(h, h.Sessions)
	req := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Code = %d, want 401 (no session); body=%q", w.Code, w.Body.String())
	}
}

// TestRouter_NilSessionsPanics guards the documented contract:
// passing nil to Router is a programming error and must fail
// loudly at boot, not silently bypass auth (which would be the
// far worse failure mode — every endpoint would become public).
func TestRouter_NilSessionsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Router(nil) did not panic")
		}
	}()
	Router(&Handlers{DB: newTestDB(t), Secret: newTestSealer(t)}, nil)
}

// TestHealthRouter_BypassesAuth confirms /healthz is reachable
// without a session cookie. The whole point of the route is to
// be probe-able by orchestrators that don't carry one.
func TestHealthRouter_BypassesAuth(t *testing.T) {
	h := &Handlers{DB: newTestDB(t), SkipCaddyReload: true}
	r := HealthRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	// Body shape: {"status":"ok","checks":{"db":{"ok":true},...}}
	var body struct {
		Status string            `json:"status"`
		Checks map[string]any    `json:"checks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if _, ok := body.Checks["db"]; !ok {
		t.Errorf("missing db check in: %+v", body.Checks)
	}
}

// TestHealthWrap_RoutesCorrectly confirms the HealthWrap
// composition: /healthz hits the health handler, everything else
// falls through to the primary.
func TestHealthWrap_RoutesCorrectly(t *testing.T) {
	h := &Handlers{DB: newTestDB(t), SkipCaddyReload: true}
	primary := Router(h, newSessionStoreForTest(t, h_unused{}))
	health := HealthRouter(h)
	wrapped := HealthWrap(primary, health)

	// /healthz → health handler
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Errorf("/healthz Code = %d, want 200", w.Code)
	}

	// /api/sites (no session) → primary handler → 401
	w2 := httptest.NewRecorder()
	wrapped.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/sites", nil))
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("/api/sites Code = %d, want 401", w2.Code)
	}
}

// h_unused is a tiny shim type so newSessionStoreForTest can take
// it as a "context" parameter. We only need a *Handlers in there
// to satisfy the helper's signature; the helper itself constructs
// its own Handlers internally.
type h_unused struct{}

// newSessionStoreForTest builds a SessionStore backed by a fresh
// DB so router tests don't share the test-package's main DB. The
// unused-Handlers argument is a placeholder for callers that
// want to reuse an existing DB; tests that don't need that
// relationship can pass the empty struct.
func newSessionStoreForTest(t *testing.T, _ h_unused) *SessionStore {
	t.Helper()
	store := NewSessionStore(newTestDB(t))
	t.Cleanup(func() { store.PurgeExpired(t.Context()) })
	return store
}
