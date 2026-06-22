package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := CORS(inner)

	r := httptest.NewRequest(http.MethodOptions, "/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", w.Code)
	}
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if w.Header().Get(h) == "" {
			t.Errorf("missing CORS header %s", h)
		}
	}

	r = httptest.NewRequest(http.MethodGet, "/x", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET status = %d, want 200", w.Code)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		xff     string
		want    string
	}{
		{"remote only", "10.0.0.1:54321", "", "10.0.0.1"},
		{"xff single", "10.0.0.1:54321", "203.0.113.5", "203.0.113.5"},
		{"xff multi", "10.0.0.1:54321", "203.0.113.5, 10.0.0.1", "203.0.113.5"},
		{"xff spaces", "10.0.0.1:54321", "  203.0.113.5  ", "203.0.113.5"},
		{"ipv6", "[::1]:8080", "", "::1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}