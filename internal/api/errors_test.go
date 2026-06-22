package api

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isaced/nanoku/internal/db"
)

// TestWriteInternalErr_MasksCause asserts the underlying error is never
// serialized to the client — only the stable ErrInternal string is. The cause
// is logged server-side (captured here via the stdlib log default; we only
// assert on the response body).
func TestWriteInternalErr_MasksCause(t *testing.T) {
	w := httptest.NewRecorder()
	// An error carrying a path + SQL detail that must never reach the client.
	cause := errors.New("ent: open sqlite3 file /Users/secret/nanoku.db: no such file")
	writeInternalErr(w, cause)

	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "/Users/secret") {
		t.Errorf("response leaked host path: %q", body)
	}
	if strings.Contains(body, "sqlite3") {
		t.Errorf("response leaked SQL detail: %q", body)
	}
	if !strings.Contains(body, ErrInternal) {
		t.Errorf("response = %q, want stable %q", body, ErrInternal)
	}
}

func TestWriteInternalErr_NilErr(t *testing.T) {
	// Defensive: a nil cause should still produce a stable 500, not panic.
	w := httptest.NewRecorder()
	writeInternalErr(w, nil)
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), ErrInternal) {
		t.Errorf("body = %q, want %q", w.Body.String(), ErrInternal)
	}
}

// TestIsNotFound covers the two shapes ent returns for a missing row: the
// package sentinel db.ErrNotFound and the generated NotFoundError.
func TestIsNotFound(t *testing.T) {
	if !isNotFound(db.ErrNotFound) {
		t.Error("db.ErrNotFound should be recognized as not-found")
	}
	// The generated NotFoundError is db.IsNotFound-true.
	nf := &db.NotFoundError{}
	if !isNotFound(nf) {
		t.Error("generated NotFoundError should be recognized as not-found")
	}
	if isNotFound(errors.New("some other error")) {
		t.Error("unrelated error must not be treated as not-found")
	}
	if isNotFound(nil) {
		t.Error("nil must not be treated as not-found")
	}
}
