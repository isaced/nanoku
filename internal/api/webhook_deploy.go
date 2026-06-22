package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/isaced/nanoku/internal/db"
)

// executeWebhookDeploy runs in a goroutine spawned by Webhook after the
// deploy lock has been acquired and the Deploy row has been created with
// status=running. It always releases the lock and updates the Deploy row.
//
// parentCtx is a detached context (not the HTTP request's), so a client
// disconnect doesn't kill the in-flight pull / caddy reload.
func (h *Handlers) executeWebhookDeploy(parentCtx context.Context, appID, deployID int, tag, commitMsg string) {
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

	if h.Docker == nil {
		h.markDeployFailed(ctx, deployID, errors.New("docker unavailable"))
		return
	}

	if a.ImageRepo == nil || *a.ImageRepo == "" {
		h.markDeployFailed(ctx, deployID, errors.New("app has no image_repo configured"))
		return
	}

	image := *a.ImageRepo + ":" + tag

	regURL, regUser, regPass := registryCreds(a)
	pull := func() error {
		if err := h.Docker.PullImage(ctx, image); err != nil {
			return fmt.Errorf("pull %s: %w", image, err)
		}
		return nil
	}
	if err := h.Docker.WithRegistry(ctx, regURL, regUser, regPass, pull); err != nil {
		h.markDeployFailed(ctx, deployID, err)
		return
	}

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
	_, containerName, err := h.Docker.CreateAppContainer(ctx, a.Name, image, appPort(a), envKVs, 0)
	if err != nil {
		h.markDeployFailed(ctx, deployID, fmt.Errorf("create container: %w", err))
		return
	}

	now := time.Now().UTC()
	cont, err := h.DB.Container.Create().
		SetDockerID("").
		SetName(containerName).
		SetImage(image).
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

	if err := h.regenerateAndReloadCtx(ctx); err != nil {
		h.markDeployFailed(ctx, deployID, fmt.Errorf("caddy regen: %w", err))
		return
	}

	_, _ = h.DB.Deploy.UpdateOneID(deployID).
		SetStatus("success").
		SetFinishedAt(time.Now().UTC()).
		Save(ctx)
}

func registryCreds(a *db.App) (url, user, pass string) {
	if a.RegistryURL != nil {
		url = *a.RegistryURL
	}
	if a.RegistryUsername != nil {
		user = *a.RegistryUsername
	}
	if a.RegistryPassword != nil {
		pass = *a.RegistryPassword
	}
	return
}
