package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicAuth(t *testing.T) {
	const user, pass = "admin", "secret"
	h := BasicAuth(user, pass)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		user, pass string
		wantCode   int
	}{
		{"valid", "admin", "secret", http.StatusOK},
		{"wrong pass", "admin", "nope", http.StatusUnauthorized},
		{"wrong user", "root", "secret", http.StatusUnauthorized},
		{"empty", "", "", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.user != "" || tc.pass != "" {
				r.SetBasicAuth(tc.user, tc.pass)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantCode)
			}
			if tc.wantCode == http.StatusUnauthorized {
				if got := w.Header().Get("WWW-Authenticate"); got == "" {
					t.Errorf("missing WWW-Authenticate header on 401")
				}
			}
		})
	}
}

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
