package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/deploy"
)

// RollbackRequest is the body shape for POST /api/apps/{id}/rollback.
// DeployID identifies the historical deploy to roll back to. Its recorded
// image is what the rollback re-pulls and runs.
type RollbackRequest struct {
	DeployID int `json:"deployId"`
}

// RollbackApp starts a deploy that re-runs the image snapshot from a
// previous successful deploy. Mirrors DeployApp: 202 Accepted with a new
// deployId; the actual work happens in a goroutine.
//
// Constraints:
//   - Target deploy must belong to the same app
//   - Target deploy must have status=success (rolling back to a failed
//     deploy would just re-deploy a broken image)
//   - App must be deploy_method=docker. Compose rollback needs the
//     compose-file snapshot, which we don't store yet — fail with 400
//     rather than silently doing the wrong thing.
//
// The historical Container row whose image we're rolling back to is NOT
// reused as a live container. We re-pull + re-create from scratch, so any
// env / mounts added since the original deploy are picked up. Rollback is
// really "deploy this image again", not "rewind to the exact byte state".
func (h *Handlers) RollbackApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var in RollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.DeployID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("deployId is required"))
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
	if a.DeployMethod == "compose" {
		writeErr(w, http.StatusBadRequest, errors.New("rollback for compose-mode apps is not yet supported"))
		return
	}

	target, err := h.DB.Deploy.Query().
		Where(deploy.IDEQ(in.DeployID), deploy.HasAppWith(app.IDEQ(a.ID))).
		Only(r.Context())
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("target deploy not found for this app"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	if target.Status != deploy.StatusSuccess {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("cannot roll back to a deploy with status=%s", target.Status))
		return
	}
	if target.Image == nil || *target.Image == "" {
		writeErr(w, http.StatusBadRequest, errors.New("target deploy has no image snapshot (pre-image-tracking deploy)"))
		return
	}

	if h.DeployLock == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("deploy lock not configured"))
		return
	}
	if !h.DeployLock.TryAcquire(a.ID) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": false,
			"reason":   "another deploy is already running for this app",
			"appId":    a.ID,
		})
		return
	}

	// Mark the target deploy as rolled_back so the UI can show the chain.
	if _, err := h.DB.Deploy.UpdateOneID(target.ID).
		SetStatus(deploy.StatusRolledBack).
		Save(r.Context()); err != nil {
		h.DeployLock.Release(a.ID)
		writeInternalErr(w, err)
		return
	}

	dep, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("rollback").
		SetStatus("running").
		SetStartedAt(time.Now().UTC()).
		SetImage(*target.Image).
		SetCommitSha(targetCommit(target)). // carry the original SHA for the audit trail
		Save(r.Context())
	if err != nil {
		h.DeployLock.Release(a.ID)
		writeInternalErr(w, err)
		return
	}

	// Detached context so a client disconnect during the rollback doesn't
	// kill the in-flight image pull.
	go h.executeDeploy(context.Background(), a.ID, dep.ID, *target.Image)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":       true,
		"deployId":       dep.ID,
		"appId":          a.ID,
		"rolledBackFrom": target.ID,
		"image":          *target.Image,
	})
}

// Ensure db package is referenced for future expansion (and so go vet
// catches accidental schema-removal refactors).
var _ = (*db.DB)(nil)

// targetCommit returns the commit SHA stored on the target deploy as a
// string, or empty when the deploy has none (manual deploys before this
// field existed).
func targetCommit(d *db.Deploy) string {
	if d.CommitSha == nil {
		return ""
	}
	return *d.CommitSha
}