package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPathID exercises the router-PathValue-based id parser. A request that
// has no {id} path value yields (0, false); a set integer value yields the id.
func TestPathID(t *testing.T) {
	tests := []struct {
		name   string
		idVal  string
		wantID int
		wantOK bool
	}{
		{"numeric", "42", 42, true},
		{"zero", "0", 0, true},
		{"empty", "", 0, false},
		{"non-numeric", "abc", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/apps/"+tc.idVal, nil)
			if tc.idVal != "" {
				req.SetPathValue("id", tc.idVal)
			}
			id, ok := pathID(req)
			if id != tc.wantID || ok != tc.wantOK {
				t.Fatalf("pathID(id=%q) = (%d, %v), want (%d, %v)", tc.idVal, id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

// TestPathID_NoMisparse is the regression guard for the old string-prefix
// bug: a non-numeric segment under /api/apps/ must not be silently parsed as
// id=0. With the router-PathValue approach, a missing {id} value is a clean
// (0, false) — the handler returns 400 instead of acting on id=0.
func TestPathID_NoMisparse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/apps/abc/env", nil)
	// No SetPathValue: the router would only set "id" for the declared
	// {id} segment, so a stray segment never reaches pathID as a value.
	id, ok := pathID(req)
	if ok {
		t.Errorf("pathID with no {id} value returned (%d, true), want (0, false)", id)
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
