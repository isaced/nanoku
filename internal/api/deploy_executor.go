package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/isaced/nanoku/internal/db"
)

// executeDeploy is the shared async deploy worker used by both the manual
// UI path (DeployApp) and the HTTP-trigger path (Trigger). It runs in a
// goroutine spawned by the caller after the per-app DeployLock has been
// acquired and a Deploy row has been created with status=running.
//
// `imageOverride` is empty for manual deploys (use the app's stored image);
// non-empty for trigger deploys (the resolved <repo>:<tag>). The HTTP payload
// has already been validated before this is called; the worker is
// intentionally non-validating so a panic in the deploy pipeline can't be
// confused with a malformed request.
//
// `parentCtx` must be a detached context (not an HTTP request's), so a
// client disconnect doesn't kill an in-flight pull / caddy reload.
//
// The worker always releases the deploy lock and writes a terminal status
// (success / failed) before returning. A panic is recovered and converted to
// a failed Deploy row.
func (h *Handlers) executeDeploy(parentCtx context.Context, appID, deployID int, imageOverride string) {
	defer h.DeployLock.Release(appID)
	defer func() {
		if r := recover(); r != nil {
			h.markDeployFailed(parentCtx, deployID, fmt.Errorf("panic: %v", r))
		}
	}()

	ctx := parentCtx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
	}

	a, err := h.DB.App.Get(ctx, appID)
	if err != nil {
		h.markDeployFailed(ctx, deployID, fmt.Errorf("load app: %w", err))
		return
	}

	image := imageOverride
	if image == "" {
		image = appImage(a)
	}
	if image == "" {
		h.markDeployFailed(ctx, deployID, errors.New("app has no image configured"))
		return
	}

	if h.Docker == nil {
		h.markDeployFailed(ctx, deployID, errors.New("docker unavailable"))
		return
	}

	regURL, regUser, regPass := registryCreds(a)

	// Tear down the prior primary container (best-effort; missing = nothing to do).
	if cur, err := a.QueryCurrentContainer().Only(ctx); err == nil && cur != nil {
		_ = h.Docker.StopContainer(ctx, cur.Name)
		_ = h.Docker.RemoveContainer(ctx, cur.Name)
		_ = h.DB.Container.DeleteOneID(cur.ID).Exec(ctx)
	}

	envVars, _ := a.QueryEnvVars().All(ctx)
	envKVs := make([]string, 0, len(envVars))
	for _, e := range envVars {
		envKVs = append(envKVs, e.Key+"="+e.Value)
	}

	var (
		containerName string
		primaryImg    = image
	)
	if a.DeployMethod == "compose" {
		project, filePath := h.resolveComposeFile(a)
		// Inline content → write to disk now so compose can read it
		// (covers first deploy and re-deploys after edits).
		if (a.ComposePath == nil || *a.ComposePath == "") && a.ComposeContent != nil && *a.ComposeContent != "" {
			written, werr := h.writeComposeFile(a.Name, *a.ComposeContent)
			if werr != nil {
				h.markDeployFailed(ctx, deployID, fmt.Errorf("write compose file: %w", werr))
				return
			}
			filePath = written
		}
		// Best-effort take-down of the prior stack so re-deploys start clean.
		_ = h.Docker.ComposeDown(ctx, project, filePath)
		if err := h.Docker.WithRegistry(ctx, regURL, regUser, regPass, func() error {
			return h.Docker.ComposeUp(ctx, project, filePath, true)
		}); err != nil {
			h.markDeployFailed(ctx, deployID, err)
			return
		}
		names, _ := h.Docker.ComposePSNames(ctx, project, "")
		if len(names) > 0 {
			containerName = names[0]
		} else {
			containerName = composeProjectName(a.Name) + "-1"
		}
	} else {
		if err := h.Docker.WithRegistry(ctx, regURL, regUser, regPass, func() error {
			if err := h.Docker.PullImage(ctx, image); err != nil {
				return fmt.Errorf("pull %s: %w", image, err)
			}
			mounts, merr := loadMounts(ctx, a)
			if merr != nil {
				return fmt.Errorf("load mounts: %w", merr)
			}
			_, name, err := h.Docker.CreateAppContainer(ctx, a.Name, image, appPort(a), envKVs, 0, mounts)
			if err != nil {
				return fmt.Errorf("create container: %w", err)
			}
			containerName = name
			return nil
		}); err != nil {
			h.markDeployFailed(ctx, deployID, err)
			return
		}

		// Make sure the container actually exists before we record it; a
		// deploy that returned no container (e.g. docker run failed after
		// the call returned) would otherwise leave a dangling Container row.
		names, _ := h.Docker.ListContainersByNamePrefix(ctx, containerName)
		found := false
		for _, n := range names {
			if n == containerName {
				found = true
				break
			}
		}
		if !found {
			h.markDeployFailed(ctx, deployID, errors.New("container not found after deploy"))
			return
		}
	}

	now := time.Now().UTC()
	cont, err := h.DB.Container.Create().
		SetDockerID("").
		SetName(containerName).
		SetImage(primaryImg).
		SetStatus("running").
		SetStartedAt(now).
		SetAppID(a.ID).
		SetDeployID(deployID).
		Save(ctx)
	if err != nil {
		h.markDeployFailed(ctx, deployID, err)
		return
	}
	if err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(cont.ID).Exec(ctx); err != nil {
		h.markDeployFailed(ctx, deployID, err)
		return
	}

	// Container name (or set of names) may have changed — regenerate the
	// Caddyfile so it points at the freshly rotated upstream. A failure
	// here is terminal (not partial): the deploy is marked failed and the
	// goroutine exits, but the HTTP caller already has its 202 response.
	if err := h.regenerateAndReloadCtx(ctx); err != nil {
		h.markDeployFailed(ctx, deployID, fmt.Errorf("caddy regen: %w", err))
		return
	}

	_, _ = h.DB.Deploy.UpdateOneID(deployID).
		SetStatus("success").
		SetFinishedAt(time.Now().UTC()).
		Save(ctx)
}

// registryCreds safely dereferences an app's registry credential pointers.
func registryCreds(a *db.App) (url, user, pass string) {
	if a.RegistryURL != nil {
		url = strings.TrimSpace(*a.RegistryURL)
	}
	if a.RegistryUsername != nil {
		user = strings.TrimSpace(*a.RegistryUsername)
	}
	if a.RegistryPassword != nil {
		pass = *a.RegistryPassword
	}
	return
}