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

// RotateTriggerToken issues a new random token for the app's trigger
// endpoint. The new token is returned in the response body — caller must
// copy it before discarding the response; subsequent Get/List calls do
// not return it.
func (h *Handlers) RotateTriggerToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r.URL.Path, "/api/apps/")
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if a.TriggerToken == nil || *a.TriggerToken == "" {
		writeErr(w, http.StatusConflict, errors.New("trigger not configured for this app"))
		return
	}
	tok, err := randomToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("generate token: %w", err))
		return
	}
	a, err = h.DB.App.UpdateOneID(id).SetTriggerToken(tok).Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	dto := h.toAppDTO(r.Context(), a)
	dto.TriggerToken = tok
	writeJSON(w, http.StatusOK, dto)
}

// ensureAppTriggerToken generates a token for an app that doesn't have one
// yet. Called from CreateApp / UpdateApp when an app first becomes
// trigger-capable. Centralized so the generation rules (length, encoding)
// live in one place and we never have to think about which path to take.
func (h *Handlers) ensureAppTriggerToken(a *db.App) error {
	if a.TriggerToken != nil && *a.TriggerToken != "" {
		return nil
	}
	tok, err := randomToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	a.TriggerToken = &tok
	return nil
}

// appHasToken is a small predicate used by DTO assembly to decide whether
// to surface the `triggerConfigured` flag to the UI without leaking the
// actual token.
func appHasToken(a *db.App) bool {
	return a != nil && a.TriggerToken != nil && *a.TriggerToken != ""
}
