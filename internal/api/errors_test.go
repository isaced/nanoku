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

// TestWriteDBErr_ConstraintReturns409 covers the case the new helper was
// added for: a unique-constraint violation from ent should surface as
// 409 Conflict (a class the client can act on by changing the input),
// not 500 internal. Stable message is "resource already exists" —
// the handler should add field-level context if it wants a more
// specific message.
func TestWriteDBErr_ConstraintReturns409(t *testing.T) {
	w := httptest.NewRecorder()
	// We can't easily forge a real sqlgraph constraint error in a unit
	// test without standing up a real DB, but the helper's behavior
	// only depends on isConstraintError, so we monkey-patch for the
	// duration of the test.
	orig := isConstraintError
	isConstraintError = func(error) bool { return true }
	t.Cleanup(func() { isConstraintError = orig })

	writeDBErr(w, errors.New("ent: constraint failed: UNIQUE constraint failed: apps.name"))

	if w.Code != 409 {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), ErrAlreadyExists) {
		t.Errorf("body = %q, want stable %q", w.Body.String(), ErrAlreadyExists)
	}
	// The raw ent message must not leak — it carries the table name
	// and the column that violated the constraint.
	if strings.Contains(w.Body.String(), "apps.name") {
		t.Errorf("response leaked schema detail: %q", w.Body.String())
	}
}

// TestWriteDBErr_NonConstraintReturns500 covers the fall-through path:
// any DB error that isn't a constraint violation is treated as
// internal (500 with stable message, full err logged server-side).
func TestWriteDBErr_NonConstraintReturns500(t *testing.T) {
	w := httptest.NewRecorder()
	// Make sure we exercise the non-constraint branch regardless of
	// what isConstraintError actually does in this test environment.
	orig := isConstraintError
	isConstraintError = func(error) bool { return false }
	t.Cleanup(func() { isConstraintError = orig })

	writeDBErr(w, errors.New("ent: SQLITE_BUSY: database is locked"))

	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), ErrInternal) {
		t.Errorf("body = %q, want stable %q", w.Body.String(), ErrInternal)
	}
	// The raw message must not leak.
	if strings.Contains(w.Body.String(), "SQLITE_BUSY") {
		t.Errorf("response leaked SQL detail: %q", w.Body.String())
	}
}

// TestWriteDBErr_NilIsNoop documents that writeDBErr(nil) is safe
// and produces no response — callers don't have to guard against a
// nil err they just got from a function that returns (..., err)
// and may not have actually failed.
func TestWriteDBErr_NilIsNoop(t *testing.T) {
	w := httptest.NewRecorder()
	writeDBErr(w, nil)
	if w.Code != 200 {
		// httptest.NewRecorder starts at 200; a no-op should leave
		// it alone. We assert the body is empty too, since the helper
		// shouldn't write anything when err is nil.
		t.Fatalf("status = %d, want 200 (no-op)", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", w.Body.String())
	}
}
