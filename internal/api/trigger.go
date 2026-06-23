package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
)

const (
	// bearerPrefix is the standard RFC 7617 prefix we accept for the
	// Authorization header. Anything else (Basic, custom schemes) is
	// rejected for the trigger endpoint.
	bearerPrefix = "Bearer "

	// maxTriggerBody caps the trigger payload size. Real payloads are
	// < 1 KiB; the cap exists only to bound memory if an attacker
	// spams garbage after auth.
	maxTriggerBody = 1 << 20 // 1 MiB

	// authHeader is the only header we read for trigger auth.
	authHeader = "Authorization"
)

// imageTagRe is the subset of OCI distribution tag rules we accept. The tag
// gets interpolated into a `docker pull <repo>:<tag>` argv so the constraint
// is non-negotiable: any character that could be interpreted as a flag, env
// var, or shell metacharacter is excluded.
var imageTagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// triggerPayload is the JSON body shape accepted on the trigger endpoint.
// Both fields are validated server-side; we never trust the client.
type triggerPayload struct {
	Tag           string `json:"tag"`
	CommitMessage string `json:"commit_message,omitempty"`
}

// Trigger is the HTTP handler at POST /api/apps/{name}/trigger. It is mounted
// outside the BasicAuth chain (see main.go): the only credential is the app's
// per-app trigger_token presented as `Authorization: Bearer <token>`.
//
// The deploy runs in a goroutine after a Deploy row has been recorded with
// status=running, and the response returns 202 immediately so a slow pull
// never holds the caller open.
func (h *Handlers) Trigger(w http.ResponseWriter, r *http.Request) {
	if h.DeployLock == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("deploy lock not configured"))
		return
	}

	appName := r.PathValue("name")
	if appName == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing app name"))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTriggerBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("read body: %w", err))
		return
	}

	a, err := h.DB.App.Query().Where(app.NameEQ(appName)).Only(r.Context())
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("app not found"))
		return
	}
	if a.TriggerToken == nil || *a.TriggerToken == "" {
		writeErr(w, http.StatusNotFound, errors.New("trigger not configured for this app"))
		return
	}

	if !verifyToken(*a.TriggerToken, r.Header.Get(authHeader)) {
		writeErr(w, http.StatusUnauthorized, errors.New("invalid or missing bearer token"))
		return
	}

	var p triggerPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("parse payload: %w", err))
		return
	}
	if !imageTagRe.MatchString(p.Tag) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid tag %q", p.Tag))
		return
	}

	image, err := resolveTriggerImage(a, p.Tag)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}

	if !h.DeployLock.TryAcquire(a.ID) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": false,
			"reason":   "another deploy is already running for this app",
		})
		return
	}

	dep, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("trigger").
		SetStatus("running").
		SetCommitSha(p.Tag).
		SetCommitMessage(p.CommitMessage).
		SetStartedAt(time.Now().UTC()).
		Save(r.Context())
	if err != nil {
		h.DeployLock.Release(a.ID)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Detached context: the goroutine must outlive the HTTP request,
	// otherwise a client disconnect would kill an in-flight pull.
	go h.executeDeploy(context.Background(), a.ID, dep.ID, image)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"deployId": dep.ID,
		"image":    image,
		"tag":      p.Tag,
	})
}

// verifyToken returns true when header is `Bearer <secret>` and
// `<secret>` matches expected in constant time. We do NOT treat the
// expected token as a prefix or substring — it must match exactly.
func verifyToken(expected, header string) bool {
	if !strings.HasPrefix(header, bearerPrefix) {
		return false
	}
	got := header[len(bearerPrefix):]
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// resolveTriggerImage builds `<app.Image repo>:<tag>` for the trigger flow.
// The image field on the app may be either "repo:tag" (manual deploy form)
// or "repo" (bare). We always strip the existing tag if any and append the
// payload tag, so trigger deploys can never get a stale tag from the form.
//
// Falls back to an error if the app's image is empty — without a repo we
// have nothing to pull.
func resolveTriggerImage(a *db.App, tag string) (string, error) {
	if a.Image == nil || *a.Image == "" {
		return "", errors.New("app has no image configured")
	}
	ref := *a.Image
	// `strings.LastIndex` to handle registry hostnames with ports
	// (e.g. "registry.local:5000/app") and registry-only refs (e.g. "nginx").
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref + ":" + tag, nil
}
