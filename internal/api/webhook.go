package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/isaced/nanoku/internal/db/app"
)

const (
	githubSignatureHeader = "X-Hub-Signature-256"
	githubEventHeader     = "X-GitHub-Event"
	maxWebhookBody        = 1 << 20 // 1 MiB
)

// imageTagRe is the subset of OCI distribution tag rules we accept on a
// webhook payload. Restrictive on purpose: the tag is interpolated into a
// `docker pull <image_repo>:<tag>` argv.
var imageTagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

type webhookPayload struct {
	Tag           string `json:"tag"`
	CommitMessage string `json:"commit_message,omitempty"`
}

// Webhook receives a build-notify from the user's CI (GitHub Actions etc).
// Auth is by HMAC-SHA256 over the raw body using the app's webhook_secret;
// see X-Hub-Signature-256 in the GitHub webhook spec.
//
// On success the deploy runs in a goroutine and the response returns 202
// immediately so the caller doesn't time out on long builds/pulls.
func (h *Handlers) Webhook(w http.ResponseWriter, r *http.Request) {
	if h.DeployLock == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("deploy lock not configured"))
		return
	}

	appName := r.PathValue("name")
	if appName == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing app name"))
		return
	}

	// Body size cap: pre-emptively avoid unbounded memory if the signature
	// never matches and a malicious caller spams garbage.
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("read body: %w", err))
		return
	}

	app, err := h.DB.App.Query().Where(app.NameEQ(appName)).Only(r.Context())
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("app not found"))
		return
	}
	if app.WebhookSecret == nil || *app.WebhookSecret == "" || app.ImageRepo == nil || *app.ImageRepo == "" {
		writeErr(w, http.StatusNotFound, errors.New("webhook not configured for this app"))
		return
	}

	if !verifySignature(*app.WebhookSecret, body, r.Header.Get(githubSignatureHeader)) {
		writeErr(w, http.StatusUnauthorized, errors.New("invalid signature"))
		return
	}

	// Only honor push-style notifies. Other events are accepted silently.
	if ev := r.Header.Get(githubEventHeader); ev != "" && ev != "push" {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ignored": true,
			"reason":  fmt.Sprintf("event %q not handled", ev),
		})
		return
	}

	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("parse payload: %w", err))
		return
	}
	if !imageTagRe.MatchString(p.Tag) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid tag %q", p.Tag))
		return
	}

	if !h.DeployLock.TryAcquire(app.ID) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": false,
			"reason":   "another deploy is already running for this app",
		})
		return
	}

	dep, err := h.DB.Deploy.Create().
		SetAppID(app.ID).
		SetTrigger("webhook").
		SetStatus("running").
		SetCommitSha(p.Tag).
		SetCommitMessage(p.CommitMessage).
		SetStartedAt(time.Now().UTC()).
		Save(r.Context())
	if err != nil {
		h.DeployLock.Release(app.ID)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Detached context: webhook deploys must outlive the HTTP request.
	go h.executeWebhookDeploy(context.Background(), app.ID, dep.ID, p.Tag, p.CommitMessage)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":  true,
		"deployId":  dep.ID,
		"imageRepo": *app.ImageRepo,
		"tag":       p.Tag,
	})
}

// verifySignature returns true when `header` equals "sha256=<hex(hmac(secret, body))>".
// Uses hmac.Equal (constant time) to thwart timing oracles.
func verifySignature(secret string, body []byte, header string) bool {
	if header == "" {
		return false
	}
	const prefix = "sha256="
	if len(header) < len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	wantHex := header[len(prefix):]
	got, err := hex.DecodeString(wantHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
