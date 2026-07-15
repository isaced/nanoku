package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/container"
	"github.com/isaced/nanoku/internal/docker"
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
	if h.ShutdownWG != nil {
		h.ShutdownWG.Add(1)
		defer h.ShutdownWG.Done()
	}
	h.inflightMu.Lock()
	if h.inflight == nil {
		h.inflight = make(map[int]int)
	}
	h.inflight[deployID] = appID
	h.inflightMu.Unlock()
	defer func() {
		h.inflightMu.Lock()
		delete(h.inflight, deployID)
		h.inflightMu.Unlock()
	}()

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

	// Resolve the image reference for docker-mode deploys. Compose-mode
	// apps pull their image(s) from the compose YAML, so they must NOT
	// be gated on app.Image — the schema explicitly marks image as
	// "ignored when deploy_method=compose" and CreateApp only requires
	// it for docker mode. Running this check unconditionally broke
	// legitimate compose apps whose image lives only in compose_content.
	image := imageOverride
	if image == "" {
		image = appImage(a)
	}
	if a.DeployMethod != "compose" && image == "" {
		h.markDeployFailed(ctx, deployID, errors.New("app has no image configured"))
		return
	}

	// Stream every step of the deploy (pull progress, compose up lines,
	// our own status annotations) into the live log hub. Subscribers in
	// the UI see a real-time feed; the hub also keeps a bounded replay
	// buffer so a late subscriber gets the start of the deploy.
	//
	// The defer order matters: we mark the stream terminal AFTER
	// updating the Deploy row to success/failed, so a subscriber polling
	// the row sees "success" only after the terminal log line has been
	// published. (markDeployFailed is the corresponding path for the
	// error case — see below.)
	logSink := &lineWriterToHub{hub: h.DeployLogs, deployID: deployID}
	if h.DeployLogs != nil {
		logSink.hub.publish(deployID, fmt.Sprintf("→ deploy started (image=%s)", image))
	}
	defer func() {
		if h.DeployLogs != nil {
			logSink.flushPartial()
			h.DeployLogs.markTerminal(deployID)
		}
	}()

	// Schema has UNIQUE(containers.name). A prior deploy on the
	// same app left a row with the same name; the new deploy's
	// Create would otherwise fail with a UNIQUE-constraint
	// violation. Retire all live rows whose name is either the
	// docker-mode canonical name (nanoku-<name>) or the
	// compose-mode fallback (nanoku-<name>-1, the first service
	// container in the project). We do this BEFORE the docker
	// availability check, so a stale row never blocks a fresh
	// deploy — even when Docker is unreachable. (The retired
	// name is recorded in the audit trail; the row stays around
	// for rollback tooling to read by deploy_id.)
	//
	// The bulk UPDATE is a no-op when no prior row matches, so
	// first-time deploys go through unchanged. We restrict the
	// name match to exact equality on the two well-known forms
	// (rather than a HasPrefix) to avoid retiring a sibling
	// app's row by accident — e.g. an app named "blog" must not
	// evict a row for an app named "blog-staging".
	canon := "nanoku-" + a.Name
	composed := canon + "-1"
	if _, err := h.DB.Container.Update().
		Where(
			container.NameIn(canon, composed),
			container.StatusNEQ(container.StatusRetired),
		).
		SetName(fmt.Sprintf("%s-retired-%d", canon, time.Now().UnixNano())).
		SetStatus(container.StatusRetired).
		Save(ctx); err != nil {
		h.markDeployFailed(ctx, deployID, fmt.Errorf("retire prior container row: %w", err))
		return
	}

	// Schema has UNIQUE(containers.app_current_container) — the
	// App→Container O2O reverse edge is stored as a column on the
	// container side. The previous deploy's row still points at
	// this app; if we don't clear the relationship before creating
	// the new row, the new row's SetCurrentContainerID below will
	// fail with a UNIQUE violation (only one container row may
	// carry a given app's current_container FK at a time).
	//
	// Done in the same window as the `name` retire so a stale row
	// never blocks a fresh deploy — even when Docker is
	// unreachable. (Both operations are no-ops on a first-time
	// deploy.)
	if err := h.DB.App.UpdateOneID(a.ID).
		ClearCurrentContainer().
		Exec(ctx); err != nil {
		h.markDeployFailed(ctx, deployID, fmt.Errorf("clear prior current_container: %w", err))
		return
	}

	if h.Docker == nil {
		h.markDeployFailed(ctx, deployID, errors.New("docker unavailable"))
		return
	}

	regURL, regUser, regPass := h.registryCreds(a)

	// Stash the old container's identity so we can retire it *after* the
	// new one is fully wired up. Tearing the old one down first would
	// mean a transient pull or create failure leaves the app completely
	// offline — a 30-minute unavailability window on a flapping registry.
	oldContainerID := 0
	oldContainerName := ""
	if cur, err := a.QueryCurrentContainer().Only(ctx); err == nil && cur != nil {
		oldContainerID = cur.ID
		oldContainerName = cur.Name
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
		if h.DeployLogs != nil {
			h.DeployLogs.publish(deployID, fmt.Sprintf("→ compose up: project=%s", project))
		}
		if err := h.Docker.WithRegistry(ctx, regURL, regUser, regPass, func() error {
			return h.Docker.ComposeUp(ctx, project, filePath, true, docker.WithComposeStream(logSink))
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
		if h.DeployLogs != nil {
			h.DeployLogs.publish(deployID, fmt.Sprintf("→ pull %s", image))
		}
		if err := h.Docker.WithRegistry(ctx, regURL, regUser, regPass, func() error {
			if err := h.Docker.PullImage(ctx, image, docker.WithPullProgress(logSink)); err != nil {
				return fmt.Errorf("pull %s: %w", image, err)
			}
			if h.DeployLogs != nil {
				h.DeployLogs.publish(deployID, fmt.Sprintf("→ pull %s: ok", image))
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
	// expectedName was already retired up front (before the docker
	// call) so the UNIQUE constraint on containers.name doesn't
	// fire here. The Container.Create below must use the same name
	// the docker engine actually created (compose path may have
	// produced a slightly different name from the docker one).
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

	// Best-effort retire of the old container *after* the new one is
	// live and routed. The Docker container is stopped + removed; the DB
	// row is kept (marked exited) so a later rollback can find the image
	// snapshot on the original Deploy record. If retire fails the deploy
	// still reports success — the stray old container is preferable to
	// having taken it down before the new one was ready.
	if oldContainerID != 0 && oldContainerName != "" && oldContainerName != containerName {
		_ = h.Docker.StopContainer(context.Background(), oldContainerName)
		_ = h.Docker.RemoveContainer(context.Background(), oldContainerName)
		_, _ = h.DB.Container.UpdateOneID(oldContainerID).
			SetStatus("exited").
			SetStoppedAt(time.Now().UTC()).
			Save(context.Background())
	}
}

// registryCreds safely dereferences an app's registry credential pointers.
// The password column is stored encrypted at rest, so it has to be
// decrypted here before being passed to `docker login`. A decryption failure
// surfaces as an empty password, which will then fail the docker login
// call — preferable to panicking, since the calling worker already reports
// errors as failed Deploy rows.
func (h *Handlers) registryCreds(a *db.App) (url, user, pass string) {
	if a.RegistryURL != nil {
		url = strings.TrimSpace(*a.RegistryURL)
	}
	if a.RegistryUsername != nil {
		user = strings.TrimSpace(*a.RegistryUsername)
	}
	if a.RegistryPassword != nil && *a.RegistryPassword != "" {
		if dec, err := h.Secret.DecryptString(*a.RegistryPassword); err == nil {
			pass = dec
		}
	}
	return
}