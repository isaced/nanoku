package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/volume"
	"github.com/isaced/nanoku/internal/docker"
)

// VolumeDTO is the JSON shape returned for a single volume row.
type VolumeDTO struct {
	Type     string `json:"type"`
	Source   string `json:"source"` // empty = nanoku auto-names for type=volume
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

// VolumeInput is the request shape for PUT /api/apps/{id}/volumes.
type VolumeInput struct {
	Type     *string `json:"type"`
	Source   *string `json:"source"`
	Target   *string `json:"target"`
	ReadOnly *bool   `json:"readOnly"`
}

// dockerNameRe matches the subset of DNS-1123 that docker accepts for named
// volumes: lowercase letters, digits, hyphen, underscore, dot. Must start and
// end with an alphanumeric.
var dockerNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,126}[a-z0-9])?$`)

// validateVolumeMount validates one row and returns a docker.VolumeMount.
// idx is used only for error messages so the user can find the bad row.
func validateVolumeMount(in VolumeInput, idx int) (docker.VolumeMount, error) {
	if in.Type == nil {
		return docker.VolumeMount{}, fmt.Errorf("volumes[%d].type is required", idx)
	}
	typ := *in.Type
	if typ != "volume" && typ != "bind" {
		return docker.VolumeMount{}, fmt.Errorf("volumes[%d].type must be 'volume' or 'bind'", idx)
	}

	if in.Target == nil {
		return docker.VolumeMount{}, fmt.Errorf("volumes[%d].target is required", idx)
	}
	target := strings.TrimSpace(*in.Target)
	if !filepath.IsAbs(target) {
		return docker.VolumeMount{}, fmt.Errorf("volumes[%d].target %q must be an absolute path", idx, target)
	}

	var source string
	if in.Source != nil {
		source = strings.TrimSpace(*in.Source)
	}
	if typ == "bind" {
		if source == "" {
			return docker.VolumeMount{}, fmt.Errorf("volumes[%d].source is required for type=bind", idx)
		}
		if !filepath.IsAbs(source) {
			return docker.VolumeMount{}, fmt.Errorf("volumes[%d].source %q must be an absolute host path", idx, source)
		}
	} else if source != "" && !dockerNameRe.MatchString(source) {
		return docker.VolumeMount{}, fmt.Errorf("volumes[%d].source %q is not a valid docker volume name", idx, source)
	}

	readOnly := false
	if in.ReadOnly != nil {
		readOnly = *in.ReadOnly
	}

	return docker.VolumeMount{
		Type:     typ,
		Source:   source,
		Target:   target,
		ReadOnly: readOnly,
	}, nil
}

// validateVolumeSet enforces cross-row invariants (target uniqueness).
func validateVolumeSet(mounts []docker.VolumeMount) error {
	seenTarget := map[string]struct{}{}
	for i, m := range mounts {
		if _, dup := seenTarget[m.Target]; dup {
			return fmt.Errorf("volumes[%d].target %q duplicates an earlier row", i, m.Target)
		}
		seenTarget[m.Target] = struct{}{}
	}
	return nil
}

func toVolumeDTO(v *db.Volume) VolumeDTO {
	dto := VolumeDTO{
		Type:     string(v.Type),
		Target:   v.Target,
		ReadOnly: v.ReadOnly,
	}
	if v.Source != nil {
		dto.Source = *v.Source
	}
	return dto
}

func (h *Handlers) ListAppVolumes(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if _, err := h.DB.App.Get(r.Context(), id); err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, errors.New("app not found"))
			return
		}
		writeInternalErr(w, err)
		return
	}
	vols, err := h.DB.Volume.Query().
		Where(volume.HasAppWith(app.IDEQ(id))).
		Order(volume.ByID()).
		All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]VolumeDTO, 0, len(vols))
	for _, v := range vols {
		out = append(out, toVolumeDTO(v))
	}
	writeJSON(w, http.StatusOK, out)
}

// ReplaceAppVolumes replaces the full volume set for an app. Runs in a tx
// so partial failures don't leave the app in an inconsistent state. The
// resolved source for unnamed type=volume rows is written back so a later
// redeploy binds to the same physical volume.
func (h *Handlers) ReplaceAppVolumes(w http.ResponseWriter, r *http.Request) {
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
	var in []VolumeInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	mounts := make([]docker.VolumeMount, 0, len(in))
	for i, vi := range in {
		m, verr := validateVolumeMount(vi, i)
		if verr != nil {
			writeErr(w, http.StatusBadRequest, verr)
			return
		}
		if m.Type == "volume" && m.Source == "" {
			m.Source = docker.AutoAppVolumeName(a.Name, i)
		}
		mounts = append(mounts, m)
	}
	if err := validateVolumeSet(mounts); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	tx, err := h.DB.Tx(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if _, err := tx.Volume.Delete().Where(volume.HasAppWith(app.IDEQ(id))).Exec(r.Context()); err != nil {
		_ = tx.Rollback()
		writeInternalErr(w, err)
		return
	}
	for _, m := range mounts {
		create := tx.Volume.Create().
			SetType(volume.Type(m.Type)).
			SetTarget(m.Target).
			SetReadOnly(m.ReadOnly).
			SetAppID(id)
		if m.Source != "" {
			create.SetSource(m.Source)
		}
		if _, cerr := create.Save(r.Context()); cerr != nil {
			_ = tx.Rollback()
			writeDBErr(w, cerr)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(mounts),
		"hint":  "redeploy required for changes to take effect",
		"appId": id,
	})
}
