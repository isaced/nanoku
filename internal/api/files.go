package api

// files.go owns the HTTP surface for the admin UI's Files tab:
//   - GET /api/apps/{id}/containers/{name}/files?path=…
//   - GET /api/apps/{id}/containers/{name}/file?path=…[&download=1]
//
// The handlers are thin: they translate path params + query
// strings into docker.Manager calls after verifying the
// requested container actually belongs to the requested app.
// That ownership check is the security boundary — the docker
// package itself trusts whatever container ID it gets — and
// it works the same way for both deploy modes:
//
//   - compose:  the container must come from ListAppContainers
//               for the app's compose project (so a request
//               can't reach into another app's service).
//   - docker:   the container name must equal the app's
//               current_container name (so stopped-then-replaced
//               containers from older deploys can't be poked).
//
// Path validation is the other half of the boundary. The
// callers ask for absolute paths only; we additionally reject
// paths containing a ".." component so a malicious or
// accidental `path=/etc/../../root/.ssh` doesn't walk out of
// the container root. The container's filesystem is still
// fully readable to the operator, so this is belt-and-
// suspenders rather than a real sandbox — but it keeps the
// request log clean of "is this URL even valid?" failures
// and surfaces obviously-wrong paths at the API edge.

import (
	"encoding/base64"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/isaced/nanoku/internal/db"
)

// maxFileReadBytes is the hard ceiling on the text-view path
// of the file endpoint. Files larger than this still work
// through the ?download=1 branch (which streams from the
// same docker.Manager call but with no in-memory cap), but
// the inline-viewer path refuses them so the UI never has to
// try to render a multi-megabyte blob. 1 MiB matches the
// design call we made in the questionnaire — covers almost
// every config/log spot-check while keeping the request
// payload well under any reasonable browser limit.
const maxFileReadBytes int64 = 1 << 20

// AppContainerFiles handles GET
// /api/apps/{id}/containers/{name}/files?path=… and returns
// the directory listing as JSON. The path query parameter
// must be an absolute container path; we default to "/" if
// the caller omits it so the Files tab's "root" view is
// always one click away.
func (h *Handlers) AppContainerFiles(w http.ResponseWriter, r *http.Request) {
	_, name, ok := h.resolveAppContainer(w, r)
	if !ok {
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		path = "/"
	}
	if err := validateContainerPath(path); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if h.Docker == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("docker unavailable"))
		return
	}

	containerID, err := h.Docker.ContainerIDByName(r.Context(), name)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if containerID == "" {
		writeErr(w, http.StatusNotFound, errors.New("container not found"))
		return
	}

	entries, err := h.Docker.ListContainerDir(r.Context(), containerID, path)
	if err != nil {
		// ls exits non-zero for non-existent paths and
		// permission denied alike; both should look like
		// "not found" to the UI rather than a 500.
		writeErr(w, http.StatusNotFound, errors.New("path not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    path,
		"entries": entries,
	})
}

// AppContainerFile handles GET
// /api/apps/{id}/containers/{name}/file?path=…[&download=1].
//
// Default behavior (no download query) is the text-view
// path: the file is read into memory (capped at
// maxFileReadBytes) and returned as JSON with the bytes
// base64-encoded. We use base64 instead of raw JSON-string
// embedding so the response parser doesn't have to deal
// with embedded newlines, NULs, or surrogate pairs.
//
// ?download=1 switches to a raw octet-stream response
// suitable for the browser's "Save As" flow. The same
// docker.Manager call is used under the hood; we just
// stream the bytes out without the size cap so the user
// can pull a multi-megabyte log without first changing
// any settings.
func (h *Handlers) AppContainerFile(w http.ResponseWriter, r *http.Request) {
	_, name, ok := h.resolveAppContainer(w, r)
	if !ok {
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeErr(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	if err := validateContainerPath(path); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if h.Docker == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("docker unavailable"))
		return
	}

	containerID, err := h.Docker.ContainerIDByName(r.Context(), name)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if containerID == "" {
		writeErr(w, http.StatusNotFound, errors.New("container not found"))
		return
	}

	if r.URL.Query().Get("download") == "1" {
		h.streamContainerFile(w, r, containerID, path)
		return
	}

	data, size, mode, modTime, err := h.Docker.ReadContainerFile(r.Context(), containerID, path, maxFileReadBytes)
	if err != nil {
		// The docker manager already wraps the cause with
		// "file too large" / "is a directory" / "stat …
		// not found" — those are client-visible messages,
		// not internal errors, so we surface them verbatim
		// with the appropriate status. Anything else is a
		// genuine 500.
		status := http.StatusInternalServerError
		msg := err.Error()
		switch {
		case strings.Contains(msg, "file too large"):
			status = http.StatusRequestEntityTooLarge
		case strings.Contains(msg, "is a directory"):
			status = http.StatusBadRequest
		case strings.Contains(msg, "not found") || strings.Contains(msg, "No such file"):
			status = http.StatusNotFound
		}
		writeErr(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     path,
		"size":     size,
		"mode":     mode,
		"modTime":  modTime,
		"content":  base64.StdEncoding.EncodeToString(data),
		"encoding": "base64",
	})
}

// streamContainerFile serves the raw bytes of `path` from
// `containerID` as an octet-stream with a Content-Disposition
// header so the browser saves it. The 1 MiB cap from the
// text-view path is intentionally not applied here — the
// download flow's whole point is to let the user pull
// files larger than the inline viewer can handle.
func (h *Handlers) streamContainerFile(w http.ResponseWriter, r *http.Request, containerID, path string) {
	// We need a streaming source. ContainerStat is cheap and
	// gives us the size for Content-Length and a clean 404
	// if the path is gone.
	stat, err := h.Docker.ContainerStatPath(r.Context(), containerID, path)
	if err != nil {
		writeErr(w, http.StatusNotFound, errors.New("path not found"))
		return
	}
	if stat.Mode.IsDir() {
		writeErr(w, http.StatusBadRequest, errors.New("path is a directory"))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size, 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	w.WriteHeader(http.StatusOK)

	// For the download path we ignore the read cap entirely
	// (maxBytes = stat.Size + 1) so even oversized files
	// come through. The browser will buffer as needed.
	data, _, _, _, err := h.Docker.ReadContainerFile(r.Context(), containerID, path, stat.Size+1)
	if err != nil {
		// We've already sent the 200 + headers above, so
		// there's no way to turn this into a clean error
		// response. Truncate the body — the client will
		// see a short download and can retry.
		return
	}
	_, _ = w.Write(data)
}

// resolveAppContainer pulls the app id and container name
// from the URL, loads the app, and verifies the requested
// container belongs to it. Returns (0, "", false) and has
// already written the appropriate error response when it
// returns false; callers should just `return`.
//
// The container-ownership check is the security boundary
// for the file browser: it stops a request that's been
// handed an app id + some other container's name from
// poking into that other container. We don't try to be
// clever about it — a compose app's container set comes
// from ListAppContainers (filtered by the engine), and a
// docker-mode app only has one legal container: the
// current_container recorded at last deploy.
func (h *Handlers) resolveAppContainer(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return 0, "", false
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("container name is required"))
		return 0, "", false
	}
	a, err := h.DB.App.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return 0, "", false
		}
		writeInternalErr(w, err)
		return 0, "", false
	}
	if !containerBelongsToApp(r, h, a, name) {
		writeErr(w, http.StatusNotFound, errors.New("container not found"))
		return 0, "", false
	}
	return id, name, true
}

// containerBelongsToApp enforces the per-mode ownership
// rule from resolveAppContainer's doc comment. Compose
// apps use the engine-side label filter (the same one
// ListAppContainers relies on) so we don't have to
// re-implement compose's container-naming conventions;
// docker-mode apps just check the recorded
// current_container name. Anything else returns false.
func containerBelongsToApp(r *http.Request, h *Handlers, a *db.App, name string) bool {
	if a.DeployMethod == "compose" {
		if h.Docker == nil {
			return false
		}
		project := composeProjectName(a.Name)
		containers, err := h.Docker.ListAppContainers(r.Context(), project)
		if err != nil {
			return false
		}
		for _, c := range containers {
			if c.Name == name {
				return true
			}
		}
		return false
	}
	// docker-mode: the only legal container is the one we
	// recorded as current at last deploy. Anything else is
	// either an old, stopped container from a previous
	// deploy (intentionally off-limits) or a typo from the
	// caller.
	cur, err := a.QueryCurrentContainer().Only(r.Context())
	if err != nil || cur == nil {
		return false
	}
	return cur.Name == name
}

// validateContainerPath is the cheap client-facing path
// validation. It enforces the two invariants we want
// before touching docker: the path is absolute, and no
// path component is "..". Everything else (utf-8,
// embedded spaces, very long paths) is left to the
// container's filesystem to handle.
func validateContainerPath(p string) error {
	if !filepath.IsAbs(p) {
		return errors.New("path must be absolute")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return errors.New("path must not contain '..'")
		}
	}
	return nil
}
