package api

import (
	"errors"
	"log"
	"net/http"

	"entgo.io/ent/dialect/sql/sqlgraph"

	"github.com/isaced/nanoku/internal/db"
)

// ErrInternal is the stable, public-facing message returned to clients when
// an unexpected server-side error occurs. The underlying cause is logged
// server-side but never sent over the wire — ent/SQL/filesystem errors can
// leak schema details, absolute paths, or host identifiers.
const ErrInternal = "internal server error"

// ErrAlreadyExists is the stable message returned with 409 Conflict when a
// unique-constraint violation surfaces. We deliberately don't echo the
// violating field back to the client — callers should be explicit about
// which field collided (e.g. "app name is already taken") by checking
// before Save or by post-validating against a 409 response.
const ErrAlreadyExists = "resource already exists"

// writeInternalErr logs cause to the server log and writes a stable 500 to
// the client. Use it for any StatusInternalServerError path where err may
// carry internal detail (ent errors, filesystem errors, docker errors that
// embed paths). Prefer writeErr with a hand-written message when the error
// is intentional and safe to surface (validation, "not found", etc.).
func writeInternalErr(w http.ResponseWriter, err error) {
	if err != nil {
		log.Printf("nanoku: internal error: %v", err)
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": ErrInternal})
}

// writeDBErr translates an ent/database error into the right public
// response. Use this for any handler that needs to surface a DB error
// safely — i.e. instead of the legacy `writeErr(..., err)` pattern that
// would echo the raw ent error (with SQL column names, file paths, and
// other internals) to the client.
//
//   - Unique-constraint violations → 409 Conflict with ErrAlreadyExists.
//   - Any other database error     → 500 internal (full err logged).
//
// Callers that need to contextualize the conflict ("app name is already
// taken" vs. "domain is already taken") should validate against the DB
// before issuing the write, or branch on the err returned by this
// helper. Don't try to parse err to recover the field — the ent error
// text isn't part of the public API.
func writeDBErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if isConstraintError(err) {
		writeErr(w, http.StatusConflict, errors.New(ErrAlreadyExists))
		return
	}
	writeInternalErr(w, err)
}

// isNotFound reports whether err is an ent NotFound / the package sentinel.
// Used to decide whether a NotFound status is appropriate vs. a 500.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, db.ErrNotFound) {
		return true
	}
	return db.IsNotFound(err)
}

// isConstraintError reports whether err is a unique-constraint (or other
// DB-level constraint) violation. It's a var (not a const / pure func)
// so tests in this package can stub the underlying check without
// standing up a real ent client to forge a constraint error. Production
// callers should never reassign it.
var isConstraintError = func(err error) bool {
	return sqlgraph.IsConstraintError(err)
}
