package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPathID(t *testing.T) {
	tests := []struct {
		path, prefix string
		wantID       int
		wantOK       bool
	}{
		{"/api/sites/42", "/api/sites/", 42, true},
		{"/api/sites/42/toggle", "/api/sites/", 42, true},
		{"/api/apps/7", "/api/apps/", 7, true},
		{"/api/sites/", "/api/sites/", 0, false},
		{"/api/sites/abc", "/api/sites/", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			id, ok := pathID(tc.path, tc.prefix)
			if id != tc.wantID || ok != tc.wantOK {
				t.Fatalf("pathID(%q, %q) = (%d, %v), want (%d, %v)", tc.path, tc.prefix, id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

func TestWriteJSONAndErr(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 201, map[string]string{"k": "v"})
	if w.Code != 201 {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(w.Body.String(), `"k":"v"`) {
		t.Errorf("body = %q, missing key", w.Body.String())
	}

	w = httptest.NewRecorder()
	writeErr(w, 400, errStub("boom"))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "boom") {
		t.Errorf("body = %q, want to contain 'boom'", w.Body.String())
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }
