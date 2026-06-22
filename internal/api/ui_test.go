package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUIHandler_ServesIndex(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	UIHandler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<!DOCTYPE html>") && !strings.Contains(w.Body.String(), "<html") {
		t.Errorf("expected HTML body, got %q", w.Body.String()[:min(120, len(w.Body.String()))])
	}
}

func TestUIHandler_APIRoutesAre404(t *testing.T) {
	// UI handler is mounted at "/" and must not swallow /api/* — those go to the API mux.
	req := httptest.NewRequest("GET", "/api/sites", nil)
	w := httptest.NewRecorder()
	UIHandler().ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("status = %d, want 404 for /api/* under UI handler", w.Code)
	}
}

func TestUIHandler_UnknownPathFallsBackToIndex(t *testing.T) {
	req := httptest.NewRequest("GET", "/some/spa/route", nil)
	w := httptest.NewRecorder()
	UIHandler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (SPA fallback)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Errorf("expected HTML fallback body, got %q", w.Body.String()[:min(80, len(w.Body.String()))])
	}
}
