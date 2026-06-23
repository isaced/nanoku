package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newSystemStatusHandlers(version, commit, date, buildType string) *Handlers {
	return &Handlers{
		SkipCaddyReload: true,
		Version:         version,
		Commit:          commit,
		Date:            date,
		BuildType:       buildType,
	}
}

func getSystemStatus(t *testing.T, h *Handlers) (int, SystemStatus) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	w := httptest.NewRecorder()
	h.SystemStatus(w, req)

	var out SystemStatus
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v (raw=%q)", err, w.Body.String())
	}
	return w.Code, out
}

func TestSystemStatus_BuildMetadataEmpty(t *testing.T) {
	h := newSystemStatusHandlers("", "", "", "")
	code, got := getSystemStatus(t, h)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Version != "" || got.Commit != "" || got.Date != "" || got.BuildType != "" {
		t.Errorf("expected empty build metadata, got %+v", got)
	}
}

func TestSystemStatus_BuildMetadataSource(t *testing.T) {
	// Defaults match the `source` build flavor (go run / go build without ldflags).
	h := newSystemStatusHandlers("dev", "none", "unknown", "source")
	code, got := getSystemStatus(t, h)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Version != "dev" {
		t.Errorf("Version = %q, want %q", got.Version, "dev")
	}
	if got.Commit != "none" {
		t.Errorf("Commit = %q, want %q", got.Commit, "none")
	}
	if got.Date != "unknown" {
		t.Errorf("Date = %q, want %q", got.Date, "unknown")
	}
	if got.BuildType != "source" {
		t.Errorf("BuildType = %q, want %q", got.BuildType, "source")
	}
}

func TestSystemStatus_BuildMetadataRelease(t *testing.T) {
	// Mimics a goreleaser / docker build with ldflags set.
	h := newSystemStatusHandlers("0.1.0", "abc1234", "2026-06-24T01:00:00Z", "release")
	code, got := getSystemStatus(t, h)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Version != "0.1.0" {
		t.Errorf("Version = %q, want %q", got.Version, "0.1.0")
	}
	if got.Commit != "abc1234" {
		t.Errorf("Commit = %q, want %q", got.Commit, "abc1234")
	}
	if got.Date != "2026-06-24T01:00:00Z" {
		t.Errorf("Date = %q, want %q", got.Date, "2026-06-24T01:00:00Z")
	}
	if got.BuildType != "release" {
		t.Errorf("BuildType = %q, want %q", got.BuildType, "release")
	}
}

func TestSystemStatus_JSONShape(t *testing.T) {
	// Verify the JSON tags use camelCase — the UI consumes this directly.
	h := newSystemStatusHandlers("0.1.0", "deadbeef", "2026-06-24T01:00:00Z", "release")
	req := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	w := httptest.NewRecorder()
	h.SystemStatus(w, req)

	body := w.Body.String()
	for _, key := range []string{
		`"version":"0.1.0"`,
		`"commit":"deadbeef"`,
		`"date":"2026-06-24T01:00:00Z"`,
		`"buildType":"release"`,
	} {
		if !containsString(body, key) {
			t.Errorf("response missing %s\nbody: %s", key, body)
		}
	}
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
