package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/isaced/nanoku/internal/db"
)

// randomToken returns a 32-byte (256-bit) base64url-encoded random string.
// The output is the bearer token for /api/apps/{name}/trigger; treat it as
// a credential. base64url (no padding) keeps it safe in headers and shells
// without the visual noise of hex.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// RotateTriggerToken issues a fresh random token for the app's trigger
// endpoint and returns it in the response body — the caller must copy it
// before discarding the response; subsequent Get/List calls do not return
// it. It doubles as the "generate" action: it mints a token whether or not
// one already existed (rotating an existing token, or creating the first
// one for a trigger that was enabled without a credential yet).
func (h *Handlers) RotateTriggerToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	tok, err := randomToken()
	if err != nil {
		writeInternalErr(w, fmt.Errorf("generate token: %w", err))
		return
	}
	encTok, err := h.Secret.EncryptString(tok)
	if err != nil {
		writeInternalErr(w, fmt.Errorf("encrypt token: %w", err))
		return
	}
	a, err = h.DB.App.UpdateOneID(id).SetTriggerToken(encTok).Save(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	dto := h.toAppDTO(r.Context(), a)
	dto.TriggerToken = tok
	writeJSON(w, http.StatusOK, dto)
}

// appHasToken is a small predicate used by DTO assembly to decide whether
// to surface the `triggerConfigured` flag to the UI without leaking the
// actual token.
func appHasToken(a *db.App) bool {
	return a != nil && a.TriggerToken != nil && *a.TriggerToken != ""
}
