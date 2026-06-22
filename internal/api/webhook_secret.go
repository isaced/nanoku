package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
)

// randomSecret returns a 32-byte (256-bit) hex-encoded random string. The
// output is the HMAC key for /api/webhook/{name}; treat it as a credential.
func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RotateWebhookSecret issues a new random secret for the app's webhook. The
// new secret is returned in the response body — caller must copy it before
// discarding the response; subsequent Get/List calls do not return it.
func (h *Handlers) RotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
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
	if a.ImageRepo == nil || *a.ImageRepo == "" {
		writeErr(w, http.StatusConflict, errors.New("webhook not configured: image_repo is empty"))
		return
	}
	secret, err := randomSecret()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("generate secret: %w", err))
		return
	}
	a, err = h.DB.App.UpdateOneID(id).SetWebhookSecret(secret).Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	dto := h.toAppDTO(r.Context(), a)
	dto.WebhookSecret = secret
	writeJSON(w, http.StatusOK, dto)
}
