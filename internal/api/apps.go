package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"entgo.io/ent/dialect/sql"

	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/container"
	"github.com/isaced/nanoku/internal/db/deploy"
	"github.com/isaced/nanoku/internal/db/envvar"
	"github.com/isaced/nanoku/internal/docker"
)

var appNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// AppDTO, AppInput, ContainerDTO, EnvVarDTO, EnvVarInput, DeployDTO,
// toContainerDTO, toDeployDTO, toAppDTO all live in app_dto.go.
//
// compose helpers (composeFilePath, writeComposeFile, composeProjectName,
// resolveComposeFile, appImage, appPort, loadMounts) live in
// compose_helpers.go.

func (h *Handlers) ListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := h.DB.App.Query().Order(app.ByName()).All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]AppDTO, 0, len(apps))
	for _, a := range apps {
		out = append(out, h.toAppDTO(r.Context(), a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) CreateApp(w http.ResponseWriter, r *http.Request) {
	var in AppInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Name == nil || !appNameRe.MatchString(*in.Name) {
		writeErr(w, http.StatusBadRequest, errors.New("name must match ^[a-z][a-z0-9-]{0,62}$"))
		return
	}
	deployMethod := "docker"
	if in.DeployMethod != nil {
		switch *in.DeployMethod {
		case "docker", "compose":
			deployMethod = *in.DeployMethod
		default:
			writeErr(w, http.StatusBadRequest, errors.New("deployMethod must be 'docker' or 'compose'"))
			return
		}
	}
	if deployMethod == "docker" {
		if in.Image == nil || strings.TrimSpace(*in.Image) == "" {
			writeErr(w, http.StatusBadRequest, errors.New("image is required for docker mode"))
			return
		}
		if in.Port == nil || *in.Port < 1 || *in.Port > 65535 {
			writeErr(w, http.StatusBadRequest, errors.New("port must be 1..65535"))
			return
		}
	} else {
		hasPath := in.ComposePath != nil && strings.TrimSpace(*in.ComposePath) != ""
		hasContent := in.ComposeContent != nil && strings.TrimSpace(*in.ComposeContent) != ""
		if !hasPath && !hasContent {
			writeErr(w, http.StatusBadRequest, errors.New("compose mode requires composePath or composeContent"))
			return
		}
	}
	create := h.DB.App.Create().
		SetName(*in.Name).
		SetDeployMethod(deployMethod)
	if deployMethod == "docker" {
		create.SetImage(strings.TrimSpace(*in.Image))
		if in.Port != nil {
			create.SetPort(*in.Port)
		}
	}
	// compose fields
	if in.ComposePath != nil && strings.TrimSpace(*in.ComposePath) != "" {
		create.SetComposePath(strings.TrimSpace(*in.ComposePath))
	}
	if in.ComposeContent != nil && strings.TrimSpace(*in.ComposeContent) != "" {
		// written to disk after Save so we have an ID
		create.SetComposeContent(strings.TrimSpace(*in.ComposeContent))
	}
	// ExposedPorts is only meaningful for compose-mode apps; docker-mode
	// apps always proxy through App.port. Validate + marshal now so a bad
	// payload returns 400 before we touch the DB. We accept the field on
	// docker-mode requests too (rather than silently drop) so a caller
	// editing an app's deploy method in the same PATCH doesn't have to
	// remember to clear the field — we ignore the value when mode=docker.
	if in.ExposedPorts != nil {
		ep, err := MarshalExposedPorts(*in.ExposedPorts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if ep != nil {
			create.SetExposedPorts(*ep)
		}
	}
	// registry
	if in.RegistryURL != nil && strings.TrimSpace(*in.RegistryURL) != "" {
		create.SetRegistryURL(strings.TrimSpace(*in.RegistryURL))
	}
	if in.RegistryUsername != nil && strings.TrimSpace(*in.RegistryUsername) != "" {
		create.SetRegistryUsername(strings.TrimSpace(*in.RegistryUsername))
	}
	if in.RegistryPassword != nil && *in.RegistryPassword != "" {
		enc, _, err := h.Secret.EncryptOptional(*in.RegistryPassword)
		if err != nil {
			writeInternalErr(w, fmt.Errorf("encrypt registry password: %w", err))
			return
		}
		if enc != nil {
			create.SetRegistryPassword(*enc)
		}
	}

	// HTTP trigger tokens are generated on demand via POST
	// /api/apps/{id}/rotate-trigger-token — never at create time. The
	// trigger tab surfaces the token once, only when the operator
	// generates (or rotates) it there.
	if in.DeleteVolumesOnRemove != nil {
		create.SetDeleteVolumesOnRemove(*in.DeleteVolumesOnRemove)
	}

	a, err := create.Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Persist the compose file to disk right away so the file path returned
	// in the DTO is always valid (not just after a deploy).
	if deployMethod == "compose" && (in.ComposePath == nil || strings.TrimSpace(*in.ComposePath) == "") && in.ComposeContent != nil && strings.TrimSpace(*in.ComposeContent) != "" {
		if _, werr := h.writeComposeFile(a.Name, strings.TrimSpace(*in.ComposeContent)); werr != nil {
			// non-fatal: deploy will retry
			_ = werr
		}
	}
	dto := h.toAppDTO(r.Context(), a)
	writeJSON(w, http.StatusCreated, dto)
}

func (h *Handlers) GetApp(w http.ResponseWriter, r *http.Request) {
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
	dto := h.toAppDTO(r.Context(), a)
	env, err := a.QueryEnvVars().All(r.Context())
	if err == nil {
		dto.EnvVars = make([]EnvVarDTO, 0, len(env))
		for _, e := range env {
			val, derr := h.Secret.DecryptString(e.Value)
			if derr != nil {
				writeInternalErr(w, fmt.Errorf("decrypt env var %s: %w", e.Key, derr))
				return
			}
			dto.EnvVars = append(dto.EnvVars, EnvVarDTO{Key: e.Key, Value: val})
		}
	}
	writeJSON(w, http.StatusOK, dto)
}

func (h *Handlers) UpdateApp(w http.ResponseWriter, r *http.Request) {
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
	var in AppInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Name != nil && !appNameRe.MatchString(*in.Name) {
		writeErr(w, http.StatusBadRequest, errors.New("name must match ^[a-z][a-z0-9-]{0,62}$"))
		return
	}
	if in.Port != nil && (*in.Port < 1 || *in.Port > 65535) {
		writeErr(w, http.StatusBadRequest, errors.New("port must be 1..65535"))
		return
	}
	upd := h.DB.App.UpdateOneID(id)
	if in.Name != nil {
		upd.SetName(*in.Name)
	}
	if in.Image != nil {
		upd.SetImage(strings.TrimSpace(*in.Image))
	}
	if in.Port != nil {
		upd.SetPort(*in.Port)
	}
	if in.DeployMethod != nil {
		switch *in.DeployMethod {
		case "docker", "compose":
			upd.SetDeployMethod(*in.DeployMethod)
		default:
			writeErr(w, http.StatusBadRequest, errors.New("deployMethod must be 'docker' or 'compose'"))
			return
		}
	}
	if in.ComposePath != nil {
		p := strings.TrimSpace(*in.ComposePath)
		if p == "" {
			upd.ClearComposePath()
		} else {
			upd.SetComposePath(p)
		}
	}
	if in.ComposeContent != nil {
		c := strings.TrimSpace(*in.ComposeContent)
		if c == "" {
			upd.ClearComposeContent()
		} else {
			upd.SetComposeContent(c)
		}
	}
	// ExposedPorts update: nil = leave alone (the only nil case),
	// empty array = clear, non-empty = replace. Only meaningful for
	// compose-mode apps; we apply it on update regardless so users can
	// switch an app from docker→compose with a single PATCH.
	if in.ExposedPorts != nil {
		ep, err := MarshalExposedPorts(*in.ExposedPorts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if ep == nil {
			upd.ClearExposedPorts()
		} else {
			upd.SetExposedPorts(*ep)
		}
	}
	if in.RegistryURL != nil {
		u := strings.TrimSpace(*in.RegistryURL)
		if u == "" {
			upd.ClearRegistryURL()
		} else {
			upd.SetRegistryURL(u)
		}
	}
	if in.RegistryUsername != nil {
		u := strings.TrimSpace(*in.RegistryUsername)
		if u == "" {
			upd.ClearRegistryUsername()
		} else {
			upd.SetRegistryUsername(u)
		}
	}
	if in.ClearRegistry != nil && *in.ClearRegistry {
		upd.ClearRegistryPassword()
		upd.ClearRegistryUsername()
		upd.ClearRegistryURL()
	} else if in.RegistryPassword != nil && *in.RegistryPassword != "" {
		enc, _, err := h.Secret.EncryptOptional(*in.RegistryPassword)
		if err != nil {
			writeInternalErr(w, fmt.Errorf("encrypt registry password: %w", err))
			return
		}
		if enc != nil {
			upd.SetRegistryPassword(*enc)
		}
	}
	if in.DeleteVolumesOnRemove != nil {
		upd.SetDeleteVolumesOnRemove(*in.DeleteVolumesOnRemove)
	}
	// HTTP trigger:
	//   EnableTrigger=nil  → leave current state alone
	//   EnableTrigger=true  → if not yet enabled, mint a new bearer token
	//   EnableTrigger=false → clear the token; trigger endpoint returns 404
	if in.EnableTrigger != nil {
		if *in.EnableTrigger {
			if a.TriggerToken == nil || *a.TriggerToken == "" {
				tok, err := randomToken()
				if err != nil {
					writeInternalErr(w, fmt.Errorf("generate trigger token: %w", err))
					return
				}
				encTok, err := h.Secret.EncryptString(tok)
				if err != nil {
					writeInternalErr(w, fmt.Errorf("encrypt trigger token: %w", err))
					return
				}
				upd.SetTriggerToken(encTok)
			}
		} else {
			upd.ClearTriggerToken()
		}
	}
	a, err = upd.Save(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Sync the on-disk compose file with the latest content.
	if a.DeployMethod == "compose" && (a.ComposePath == nil || *a.ComposePath == "") && a.ComposeContent != nil && *a.ComposeContent != "" {
		if _, werr := h.writeComposeFile(a.Name, *a.ComposeContent); werr != nil {
			_ = werr
		}
	}
	// Regenerate the Caddyfile now: changing exposed_ports /
	// deploy_method may have invalidated the upstream of a
	// previously-resolvable site (e.g. removed a service that
	// a site was pointing at). The upstream is derived at
	// render time, so the only way for that invalidation to
	// surface is via a fresh regen — without this call the
	// site would keep the old upstream in the Caddyfile
	// until the next site mutation. RegenerateAndReload is
	// best-effort; a failure here doesn't fail the PATCH
	// (the app row is already saved), but we log it so a
	// manual regen is one click away.
	if in.ExposedPorts != nil || in.DeployMethod != nil {
		if rerr := h.regenerateAndReload(r); rerr != nil {
			_ = rerr
		}
	}
	writeJSON(w, http.StatusOK, h.toAppDTO(r.Context(), a))
}

func (h *Handlers) DeleteApp(w http.ResponseWriter, r *http.Request) {
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
	if h.Docker != nil {
		if a.DeployMethod == "compose" {
			// Bring the whole stack down (compose down removes containers,
			// default network, and the nanoku- prefix).
			project, filePath := h.resolveComposeFile(a)
			_ = h.Docker.ComposeDown(r.Context(), project, filePath)
		}
		if cur, err := a.QueryCurrentContainer().Only(r.Context()); err == nil && cur != nil {
			if err := h.Docker.RemoveContainer(r.Context(), cur.Name); err != nil {
				writeInternalErr(w, fmt.Errorf("remove container: %w", err))
				return
			}
		}
		// also remove any non-current containers (historical)
		conts, _ := h.DB.Container.Query().Where(container.HasAppWith(app.IDEQ(a.ID))).All(r.Context())
		for _, c := range conts {
			_ = h.Docker.RemoveContainer(r.Context(), c.Name)
		}
		// If the user opted in, also wipe auto-named nanoku volumes for
		// this app. Bind mounts and user-named volumes are not touched.
		if a.DeleteVolumesOnRemove {
			_ = h.Docker.RemoveAppVolumes(r.Context(), a.Name)
		}
		// Clean up generated compose file on disk (best-effort).
		if a.DeployMethod == "compose" && (a.ComposePath == nil || *a.ComposePath == "") {
			_ = os.Remove(h.composeFilePath(a.Name))
		}
	}
	if err := h.DB.App.DeleteOneID(id).Exec(r.Context()); err != nil {
		writeInternalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ImportExposedPorts pre-fills the exposed_ports list for a
// compose-mode app by parsing the app's compose YAML. The handler
// returns a list of {name, port} scaffolding the operator can
// review / edit in the UI before the value lands in the database —
// we never write to App.exposed_ports here. The port for each
// service is best-effort (expose[0] > ports[0] container-half >
// 0); a service with no detectable port comes back with port=0
// and the UI renders an empty input so the operator can fill it
// in.
//
// This is compose-only: docker-mode apps have no services to
// import, and the caller would have nothing to put in exposed_ports
// anyway (docker apps use App.port). Refuse the request up front
// rather than returning an empty list, so the UI can show a clear
// error instead of silently populating zero rows.
func (h *Handlers) ImportExposedPorts(w http.ResponseWriter, r *http.Request) {
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
	if a.DeployMethod != "compose" {
		writeErr(w, http.StatusBadRequest, errors.New("import is only available for compose-mode apps"))
		return
	}

	// Pick the compose source the same way deploy does:
	// user-provided path wins, otherwise the inline content.
	// Empty content (path set but unreadable, or neither set) is
	// a 400 with an actionable hint — the operator should either
	// add compose_content or check compose_path.
	var content string
	switch {
	case a.ComposePath != nil && *a.ComposePath != "":
		writeErr(w, http.StatusBadRequest, fmt.Errorf("compose_path is set (%s); import only reads inline compose content — paste the YAML into compose_content or open the file manually", *a.ComposePath))
		return
	case a.ComposeContent != nil:
		content = *a.ComposeContent
	}
	if strings.TrimSpace(content) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("app has no compose content to import from"))
		return
	}

	ports, err := ImportExposedPortsFromCompose(content)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	hint := "review and edit before saving"
	if len(ports) == 0 {
		hint = "no services found in compose content"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"appId":        a.ID,
		"exposedPorts": ports,
		"hint":         hint,
	})
}

func (h *Handlers) ListAppEnvVars(w http.ResponseWriter, r *http.Request) {
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
	env, err := h.DB.EnvVar.Query().Where(envvar.HasAppWith(app.IDEQ(id))).All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]EnvVarDTO, 0, len(env))
	for _, e := range env {
		val, derr := h.Secret.DecryptString(e.Value)
		if derr != nil {
			writeInternalErr(w, fmt.Errorf("decrypt env var %s: %w", e.Key, derr))
			return
		}
		out = append(out, EnvVarDTO{Key: e.Key, Value: val})
	}
	writeJSON(w, http.StatusOK, out)
}

// ReplaceAppEnvVars replaces the full env var set for an app. Requires re-deploy to take effect.
func (h *Handlers) ReplaceAppEnvVars(w http.ResponseWriter, r *http.Request) {
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
	var in []EnvVarInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	seen := map[string]struct{}{}
	for i, kv := range in {
		k := strings.TrimSpace(kv.Key)
		if k == "" {
			writeErr(w, http.StatusBadRequest, errors.New("env var key cannot be empty"))
			return
		}
		if err := docker.ValidateEnvVar(k, kv.Value); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if _, dup := seen[k]; dup {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("duplicate key %q", k))
			return
		}
		seen[k] = struct{}{}
		in[i].Key = k
	}
	// wipe + recreate in a tx so partial failures don't leave junk
	tx, err := h.DB.Tx(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if _, err := tx.EnvVar.Delete().Where(envvar.HasAppWith(app.IDEQ(a.ID))).Exec(r.Context()); err != nil {
		_ = tx.Rollback()
		writeInternalErr(w, err)
		return
	}
	for _, kv := range in {
		encVal, err := h.Secret.EncryptString(kv.Value)
		if err != nil {
			_ = tx.Rollback()
			writeInternalErr(w, fmt.Errorf("encrypt env var %s: %w", kv.Key, err))
			return
		}
		if _, err := tx.EnvVar.Create().
			SetAppID(a.ID).
			SetKey(kv.Key).
			SetValue(encVal).
			Save(r.Context()); err != nil {
			_ = tx.Rollback()
			writeInternalErr(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(in),
		"hint":  "redeploy required for changes to take effect",
		"appId": a.ID,
	})
}

func (h *Handlers) ListAppDeploys(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	deps, err := h.DB.Deploy.Query().
		Where(deploy.HasAppWith(app.IDEQ(id))).
		WithContainer().
		Order(deploy.ByCreatedAt(sql.OrderDesc())).
		Limit(50).
		All(r.Context())
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	out := make([]DeployDTO, 0, len(deps))
	for _, d := range deps {
		out = append(out, toDeployDTO(d))
	}
	writeJSON(w, http.StatusOK, out)
}
