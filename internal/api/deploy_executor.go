package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/docker"
)

// lineWriterToStore is the io.Writer adapter used by
// WithPullProgress: it splits the docker pull progress stream on
// '\n' and appends each line to the deploy's log file. A trailing
// partial line (no newline yet) is buffered and emitted on the
// next Write. flushPartial is idempotent on empty buffers.
type lineWriterToStore struct {
	w   *deployLogWriter
	buf bytes.Buffer
}

func (lw *lineWriterToStore) Write(p []byte) (int, error) {
	lw.buf.Write(p)
	for {
		idx := bytes.IndexByte(lw.buf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		line := lw.buf.Bytes()[:idx]
		lw.buf.Next(idx + 1)
		_ = lw.w.appendLine(string(line))
	}
	return len(p), nil
}

func (lw *lineWriterToStore) flushPartial() {
	if lw.buf.Len() == 0 {
		return
	}
	_ = lw.w.appendLine(lw.buf.String())
	lw.buf.Reset()
}

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

	// Open a writer for the deploy's log file. Every step of the deploy
	// (pull progress, compose up lines, our own status annotations) is
	// appended here. The file is the durable record — the SSE handler
	// tails it on demand, and a reboot doesn't lose history.
	//
	// On exit we close the writer; an SSE handler polling the file
	// notices via the IsDeployInFlight signal (or, more simply, by the
	// file size stopping changing) that no more lines are coming and
	// emits the terminal `end` event.
	var logWriter *deployLogWriter
	var pullSink *lineWriterToStore
	if h.DeployLogs != nil {
		w, err := h.DeployLogs.openWriter(deployID)
		if err != nil {
			// A failed log-file open is fatal for visibility but not
			// for the deploy itself: surface the error and proceed
			// without logging. The Deploy row still records
			// success/failed and the container is still created; the
			// operator just won't see the in-progress log. We'd
			// rather deploy than refuse.
			h.markDeployFailed(ctx, deployID, fmt.Errorf("open log file: %w", err))
			return
		}
		logWriter = w
		if image != "" {
			_ = logWriter.appendLine(fmt.Sprintf("-> deploy started (image=%s)", image))
		} else {
			_ = logWriter.appendLine(fmt.Sprintf("-> deploy started (compose, project=%s)", composeProjectName(a.Name)))
		}
	}
	defer func() {
		if pullSink != nil {
			pullSink.flushPartial()
		}
		if logWriter != nil {
			_ = logWriter.close()
		}
	}()

	// The previous deploy's row still points at this app via the
	// App→Container O2O reverse edge. If we don't clear the
	// relationship before creating the new row, the new row's
	// SetCurrentContainerID below will fail with a UNIQUE
	// violation (only one container row may carry a given app's
	// current_container FK at a time). Container names are no
	// longer UNIQUE — the alias is the routing identity now — so
	// the retire-by-rename dance is gone. A previous deploy's
	// container row just sits around with status=exited and a
	// stable name until the operator (or a future cleanup pass)
	// removes it.
	//
	// Done BEFORE the docker availability check so a stale
	// current_container never blocks a fresh deploy — even when
	// Docker is unreachable. (No-op on a first-time deploy.)
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
		if logWriter != nil {
			_ = logWriter.appendLine(fmt.Sprintf("-> compose up: project=%s", project))
		}
		// Structured compose progress: --progress json events are decoded
		// into (msg, key) pairs. A non-empty key means "replace the
		// same-key row in-place" so each layer's download ticks update a
		// single line instead of scrolling. We wire the callback straight
		// to the log file (carrying the `|` prefix for keyed lines) so
		// the replace semantics propagate through file replay and the
		// SSE handler emits the right `line-replace` event on read.
		progressEmit := func(msg, key string) {
			if logWriter == nil {
				return
			}
			if key != "" {
				_ = logWriter.appendKeyed(key, msg)
			} else {
				_ = logWriter.appendLine(msg)
			}
		}
		if err := h.Docker.WithRegistry(ctx, regURL, regUser, regPass, func() error {
			return h.Docker.ComposeUp(ctx, project, filePath, true, docker.WithComposeProgress(progressEmit))
		}); err != nil {
			h.markDeployFailed(ctx, deployID, err)
			return
		}
		// Attach the just-spun-up stack to nanoku's managed network and
		// give each service a stable network alias (`nanoku-<app>-<svc>`)
		// that Caddy can reverse-proxy to via Docker DNS. Without this
		// step the Caddyfile reverse_proxy entries return 502: Caddy is
		// only on `nanoku-net`, but `docker compose up` parks the stack
		// on its own `<project>_default` network. The attach is
		// idempotent (containers already on the network are skipped), so
		// re-deploys are a no-op. We do it before ComposePSServices so
		// the follow-up exposed_ports validation sees the same
		// container set the Caddyfile will.
		if err := h.Docker.ApplyAppAliases(ctx, project); err != nil {
			h.markDeployFailed(ctx, deployID, fmt.Errorf("apply app aliases: %w", err))
			return
		}
		names, _ := h.Docker.ComposePSNames(ctx, project, "")
		if len(names) > 0 {
			containerName = names[0]
		} else {
			containerName = composeProjectName(a.Name) + "-1"
		}
		// Validate declared exposed_ports against the actual running
		// stack by service name. A service listed in exposed_ports but
		// missing from `compose ps` is a misconfiguration (typo,
		// removed from compose file, depends_on failed) — Caddy would
		// route traffic to a non-existent alias if we let it through.
		// Fail loudly with the offending service name so the operator
		// can fix the YAML and re-deploy.
		if ports, perr := ParseExposedPorts(a.ExposedPorts); perr == nil && len(ports) > 0 {
			services, serr := h.Docker.ComposePSServices(ctx, project, "")
			if serr != nil {
				h.markDeployFailed(ctx, deployID, fmt.Errorf("compose ps --services: %w", serr))
				return
			}
			var missing []string
			for _, ep := range ports {
				if _, ok := services[ep.Name]; !ok {
					missing = append(missing, ep.Name)
				}
			}
			if len(missing) > 0 {
				h.markDeployFailed(ctx, deployID, fmt.Errorf("exposed_ports reference services not in stack: %s", strings.Join(missing, ", ")))
				return
			}
		}
	} else {
		if logWriter != nil {
			_ = logWriter.appendLine(fmt.Sprintf("→ pull %s", image))
		}
		// pullProgressWriter is an io.Writer that splits the docker
		// pull progress stream on '\n' and appends each line to the
		// deploy's log file. We use it (rather than the compose
		// progress callback) because docker pull progress is
		// line-oriented text, not the (msg, key) pair compose emits.
		if logWriter != nil {
			pullSink = &lineWriterToStore{w: logWriter}
		}
		if err := h.Docker.WithRegistry(ctx, regURL, regUser, regPass, func() error {
			if err := h.Docker.PullImage(ctx, image, docker.WithPullProgress(pullSink)); err != nil {
				return fmt.Errorf("pull %s: %w", image, err)
			}
			if logWriter != nil {
				_ = logWriter.appendLine(fmt.Sprintf("→ pull %s: ok", image))
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
	// row is kept (marked exited) for audit. If retire fails the deploy
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

	// Refresh stored upstreams for every site linked to this app. The
	// Caddyfile is already up to date (regenerateAndReloadCtx above
	// recomputes on the fly), but the per-site Site.Upstream column is
	// The Caddyfile was already regenerated above; nothing else to do
	// here. App-linked sites compute their upstream at render time
	// from (app, app_service), so they don't need a row update after
	// a deploy — the next regenerate will pick up the new container
	// state via Docker network DNS without any DB writes.
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