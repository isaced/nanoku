package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/container"
)

// ReconcileRowDBOnly describes a Container row that exists in the database
// but whose underlying Docker container does not. These appear when nanoku
// is killed mid-deploy after the DB row is committed but before the Docker
// container is fully created.
type ReconcileRowDBOnly struct {
	ContainerID int    `json:"containerId"`
	Name        string `json:"name"`
	AppID       int    `json:"appId"`
	IsCurrent   bool   `json:"isCurrent"`
}

// ReconcileReport is the JSON shape returned by GET /api/system/reconcile.
// Counts are duplicated alongside the rows so the dashboard can render
// counters without scanning the arrays.
type ReconcileReport struct {
	ScannedAt       time.Time          `json:"scannedAt"`
	ScannedDBRows   int                `json:"scannedDbRows"`
	ScannedDocker   int                `json:"scannedDocker"`
	DBOnly          []ReconcileRowDBOnly `json:"dbOnly"`
	DBOnlyCount     int                `json:"dbOnlyCount"`
	DockerOnly      []string           `json:"dockerOnly"`
	DockerOnlyCount int                `json:"dockerOnlyCount"`
	CurrentMissing  []int              `json:"currentMissing"`
}

// Reconcile walks the nanoku-owned Docker containers and compares them to
// the Container rows in the database. Returns a structured report; callers
// decide what to do with the findings.
//
// Reconcile is read-only: it never deletes containers, never edits the DB.
// For destructive cleanup use ApplyReconcile + RemoveOrphanContainer.
func (h *Handlers) Reconcile(ctx context.Context) (*ReconcileReport, error) {
	if h.DB == nil {
		return nil, fmt.Errorf("Reconcile: db not initialized")
	}
	if h.Docker == nil {
		return nil, fmt.Errorf("Reconcile: docker manager not initialized")
	}

	dockerNames, err := h.Docker.ListContainersByNamePrefix(ctx, "nanoku-")
	if err != nil {
		return nil, fmt.Errorf("list docker containers: %w", err)
	}

	conts, err := h.DB.Container.Query().WithApp().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list db containers: %w", err)
	}

	apps, err := h.DB.App.Query().WithCurrentContainer().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}

	diff := computeReconcile(reconcileInputs{
		DBContainers: conts,
		Apps:         apps,
		DockerNames:  dockerNames,
		CaddyName:    h.Docker.CaddyContainerName(),
	})

	report := &ReconcileReport{
		ScannedAt:       time.Now().UTC(),
		ScannedDBRows:   len(conts),
		ScannedDocker:   len(dockerNames),
		DBOnly:          diff.DBOnly,
		DBOnlyCount:     len(diff.DBOnly),
		DockerOnly:      diff.DockerOnly,
		DockerOnlyCount: len(diff.DockerOnly),
		CurrentMissing:  diff.CurrentMissing,
	}
	return report, nil
}

// reconcileInputs is the data feed for the pure comparison function. Kept
// here next to computeReconcile so the contract between the IO-bound
// Reconcile() and the unit-tested core stays small and obvious.
type reconcileInputs struct {
	DBContainers []*db.Container
	Apps         []*db.App
	DockerNames  []string
	CaddyName    string
}

// computeReconcile is the pure comparison logic — no DB / Docker calls —
// so it can be unit-tested without standing up a fake docker daemon. The
// test in reconcile_test.go drives this directly with hand-built inputs.
func computeReconcile(in reconcileInputs) ReconcileReport {
	dockerSet := make(map[string]struct{}, len(in.DockerNames))
	for _, n := range in.DockerNames {
		dockerSet[n] = struct{}{}
	}
	seenInDB := make(map[string]struct{}, len(in.DBContainers))

	var report ReconcileReport
	for _, c := range in.DBContainers {
		seenInDB[c.Name] = struct{}{}
		if _, ok := dockerSet[c.Name]; ok {
			continue
		}
		appID := 0
		if c.Edges.App != nil {
			appID = c.Edges.App.ID
		}
		report.DBOnly = append(report.DBOnly, ReconcileRowDBOnly{
			ContainerID: c.ID,
			Name:        c.Name,
			AppID:       appID,
		})
	}
	report.DBOnlyCount = len(report.DBOnly)

	for _, a := range in.Apps {
		if a.Edges.CurrentContainer == nil {
			continue
		}
		if _, ok := dockerSet[a.Edges.CurrentContainer.Name]; ok {
			continue
		}
		for i := range report.DBOnly {
			if report.DBOnly[i].ContainerID == a.Edges.CurrentContainer.ID {
				report.DBOnly[i].IsCurrent = true
			}
		}
		report.CurrentMissing = append(report.CurrentMissing, a.ID)
	}

	for _, n := range in.DockerNames {
		if _, ok := seenInDB[n]; ok {
			continue
		}
		if n == in.CaddyName {
			continue
		}
		if !strings.HasPrefix(n, "nanoku-") {
			continue
		}
		report.DockerOnly = append(report.DockerOnly, n)
	}
	report.DockerOnlyCount = len(report.DockerOnly)
	return report
}

// ApplyReconcile runs Reconcile and applies safe auto-fixes:
//   - For each DB Container row whose Docker container is missing AND that
//     is the app's current_container, clear the current_container edge so
//     the next deploy is the source of truth. The DB Container row is left
//     alone — it's useful for audit (you can still see "deploy #7 said
//     running at 03:14").
//   - Docker-only containers are NOT touched. Deleting a running container
//     that nanoku didn't create could be load-bearing for something else.
//
// Returns the report so callers can render a confirmation UI.
func (h *Handlers) ApplyReconcile(ctx context.Context) (*ReconcileReport, error) {
	report, err := h.Reconcile(ctx)
	if err != nil {
		return nil, err
	}
	for _, appID := range report.CurrentMissing {
		if err := h.clearAppCurrentContainer(ctx, appID); err != nil {
			return report, fmt.Errorf("clear current_container for app %d: %w", appID, err)
		}
	}
	return report, nil
}

// clearAppCurrentContainer does what the auto-clear above falls back to:
// load one app by id and clear its current_container edge.
func (h *Handlers) clearAppCurrentContainer(ctx context.Context, appID int) error {
	a, err := h.DB.App.Query().
		Where(app.IDEQ(appID)).
		WithCurrentContainer().
		Only(ctx)
	if err != nil {
		return err
	}
	if a.Edges.CurrentContainer == nil {
		return nil
	}
	_, err = h.DB.App.UpdateOneID(a.ID).ClearCurrentContainer().Save(ctx)
	return err
}

// RemoveOrphanContainer forcibly removes a docker container by name. The
// endpoint assumes the caller has confirmed this is not a tracked app
// container — the UI surfaces this as "Delete" on a row in the orphans
// list, and the row only exists in that list after a Reconcile pass found
// no matching DB row.
//
// Refuses to remove the caddy container or anything matching a tracked
// app's current container name, so a UI race (clicking delete on a
// tracked container) can't take down the reverse proxy.
//
// Returns a *orphanRefusal when the request is invalid (handler surfaces
// the safe message at 400); returns the raw DB / docker error otherwise
// (handler routes to writeInternalErr at 500). The split keeps internal
// error text from leaking through the refusal path.
func (h *Handlers) RemoveOrphanContainer(ctx context.Context, name string) error {
	if h.Docker == nil {
		return &orphanRefusal{"docker manager not initialized"}
	}
	if name == "" {
		return &orphanRefusal{"name required"}
	}
	if name == h.Docker.CaddyContainerName() {
		return &orphanRefusal{fmt.Sprintf("refusing to remove caddy container %q", name)}
	}
	if !strings.HasPrefix(name, "nanoku-") {
		return &orphanRefusal{fmt.Sprintf("refusing to remove non-nanoku container %q", name)}
	}
	tracked, err := h.DB.Container.Query().Where(container.Name(name)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("check tracked: %w", err)
	}
	if tracked {
		return &orphanRefusal{fmt.Sprintf("refusing to remove tracked container %q", name)}
	}
	return h.Docker.RemoveContainer(ctx, name)
}

// orphanRefusal is the typed error returned for the "refuse to
// remove" branches of RemoveOrphanContainer. The HTTP handler uses
// errors.As to recognize it and return 400 with the safe message;
// any other error is routed to writeInternalErr. The message itself
// is what the operator sees, so it must be hand-written — never
// include raw ent / docker error text here.
type orphanRefusal struct{ msg string }

func (e *orphanRefusal) Error() string { return e.msg }

// RunBootReconcile is the boot-time call: scan, log, and auto-clear stale
// current_container edges so a half-finished deploy doesn't leave the app
// pointing at a non-existent container. Failures are logged and swallowed —
// reconcile must never block startup.
func (h *Handlers) RunBootReconcile(ctx context.Context) {
	if h.Docker == nil || h.SkipCaddyReload {
		return
	}
	report, err := h.Reconcile(ctx)
	if err != nil {
		log.Printf("boot reconcile: %v", err)
		return
	}
	if report.DBOnlyCount == 0 && report.DockerOnlyCount == 0 {
		log.Printf("boot reconcile: clean (%d db rows, %d docker)", report.ScannedDBRows, report.ScannedDocker)
		return
	}
	log.Printf("boot reconcile: %d db-only rows, %d docker-only containers, %d current_container missing",
		report.DBOnlyCount, report.DockerOnlyCount, len(report.CurrentMissing))
	if len(report.CurrentMissing) > 0 {
		if _, err := h.ApplyReconcile(ctx); err != nil {
			log.Printf("boot reconcile: apply: %v", err)
		} else {
			log.Printf("boot reconcile: cleared %d stale current_container edges", len(report.CurrentMissing))
		}
	}
}

// --- HTTP handlers ------------------------------------------------------

// SystemReconcile serves GET /api/system/reconcile: returns the report
// without modifying anything. Frontend uses it to show the user "you have
// X orphans" without taking action.
func (h *Handlers) SystemReconcile(w http.ResponseWriter, r *http.Request) {
	report, err := h.Reconcile(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// SystemReconcileApply serves POST /api/system/reconcile: runs reconcile and
// applies safe auto-fixes (clear stale current_container edges).
func (h *Handlers) SystemReconcileApply(w http.ResponseWriter, r *http.Request) {
	report, err := h.ApplyReconcile(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// SystemRemoveOrphan serves DELETE /api/system/orphans/{name}.
func (h *Handlers) SystemRemoveOrphan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := h.RemoveOrphanContainer(r.Context(), name); err != nil {
		var refused *orphanRefusal
		if errors.As(err, &refused) {
			writeErr(w, http.StatusBadRequest, errors.New(refused.msg))
			return
		}
		writeInternalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Ensure db is referenced (used by other files for type assertions).
var _ = (*db.DB)(nil)