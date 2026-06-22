package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/isaced/nanoku/internal/db"
)

// ErrInternal is the stable, public-facing message returned to clients when
// an unexpected server-side error occurs. The underlying cause is logged
// server-side but never sent over the wire — ent/SQL/filesystem errors can
// leak schema details, absolute paths, or host identifiers.
const ErrInternal = "internal server error"

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
