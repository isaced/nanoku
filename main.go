package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/isaced/nanoku/internal/api"
	"github.com/isaced/nanoku/internal/config"
	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/docker"
	"github.com/isaced/nanoku/internal/secret"
)

var (
	version   = "dev"
	commit    = "none"
	date      = "unknown"
	buildType = "source" // "release" by Dockerfile / goreleaser ldflags
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	database, err := db.OpenDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer database.Close()

	migCtx, cancelMig := context.WithTimeout(context.Background(), 30*time.Second)
	if err := database.Migrate(migCtx); err != nil {
		cancelMig()
		log.Fatalf("db migrate: %v", err)
	}
	cancelMig()

	// The secret sealer is mandatory — we encrypt registry_passwords,
	// trigger tokens, and env var values at rest, and there's no
	// graceful "fall back to plaintext" mode we want to ship. Refuse to
	// boot without it so a missing NANOKU_SECRET_KEY fails loudly at
	// startup instead of silently storing plaintext.
	sealer, err := secret.LoadFromEnv("NANOKU_SECRET_KEY")
	if err != nil {
		log.Fatalf("secret: %v", err)
	}

	encMigCtx, cancelEncMig := context.WithTimeout(context.Background(), 30*time.Second)
	if err := (&api.Handlers{DB: database, Secret: sealer}).MigrateEncryption(encMigCtx); err != nil {
		cancelEncMig()
		log.Fatalf("encryption migration: %v", err)
	}
	cancelEncMig()
	log.Printf("encryption migration complete")

	// Refuse to boot a release binary with an empty UI embed — the
	// symptom of forgetting `npm run build` (or a broken Dockerfile
	// layer order) is otherwise a silent, blank admin page with no
	// error in the log. In dev (`buildType=source`), warn instead of
	// dying so the backend can be exercised on its own.
	if err := api.CheckUIBundled(); err != nil {
		if buildType == "source" {
			log.Printf("WARN: %v (the admin UI will return 404s)", err)
		} else {
			log.Fatalf("%v", err)
		}
	}

	var dm *docker.Manager
	if !cfg.SkipCaddyReload {
		dm, err = docker.NewManager(docker.Config{
			ContainerName: cfg.CaddyContainer,
			Image:         cfg.CaddyImage,
			VolumeName:    cfg.CaddyVolumeName,
			NetworkName:   cfg.CaddyNetworkName,
		})
		if err != nil {
			log.Printf("docker manager unavailable: %v (Caddy reload disabled)", err)
			dm = nil
		}
	}

	if dm != nil {
		setupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		if err := dm.EnsureCaddyContainer(setupCtx, cfg.CaddyfilePath); err != nil {
			log.Printf("caddy container ensure failed: %v (continuing; reload may not work)", err)
		}
		cancel()
	}

	// Seed the first admin from env vars on a fresh DB.
	// Once any user row exists, the env vars are ignored on subsequent boots.
	seedCtx, cancelSeed := context.WithTimeout(context.Background(), 10*time.Second)
	if err := api.SeedFirstAdmin(seedCtx, database, cfg.AdminUser, cfg.AdminPassword); err != nil {
		log.Fatalf("seed admin: %v", err)
	}
	cancelSeed()

	sessions := api.NewSessionStore(database)

	deployLogStore, err := api.NewDeployLogStore(cfg.DeployLogDir)
	if err != nil {
		log.Fatalf("deploy log store: %v", err)
	}
	log.Printf("deploy log dir: %s", cfg.DeployLogDir)

	// Background cleanup. The Janitor runs every built-in task once at
	// start (a long-idle install doesn't have to wait the first interval
	// to clean up) and then on its master 1-minute tick. Tasks share the
	// same per-task timeout and panic isolation, so a stuck or buggy
	// task can't crash the process. See internal/api/cleanup.go for the
	// task list and individual behavior.
	janitor := api.NewJanitor(database, sessions, deployLogStore, api.CleanupConfig{
		KeepDeploysDays: cfg.KeepDeploysDays,
	})

	var deployWG sync.WaitGroup
	handlers := &api.Handlers{
		DB:              database,
		Docker:          dm,
		CaddyfilePath:   cfg.CaddyfilePath,
		ACMEEmail:       cfg.ACMEEmail,
		SkipCaddyReload: cfg.SkipCaddyReload,
		SelfContainer:   cfg.SelfContainer,
		ComposeBaseDir:  cfg.ComposeBaseDir,
		DeployLock:      api.NewDeployLock(),
		DeployLogs:      deployLogStore,
		Sessions:        sessions,
		Secret:          sealer,
		Janitor:         janitor,
		Version:         version,
		Commit:          commit,
		Date:            date,
		BuildType:       buildType,
		TrustProxy:      cfg.TrustProxy,
		ShutdownWG:      &deployWG,
	}

	// Reconcile DB ↔ Docker state on every boot. A deploy that was killed
	// mid-flight leaves dangling Container rows or stray docker containers;
	// this catches and auto-clears the dangerous ones (stale
	// current_container edges pointing at missing containers) without
	// deleting user data.
	bootReconCtx, cancelRecon := context.WithTimeout(context.Background(), 60*time.Second)
	handlers.RunBootReconcile(bootReconCtx)
	cancelRecon()

	// Start the Janitor last so the one-shot startup pass can observe
	// any state produced above (admin seed, boot reconcile). It runs
	// synchronously up to len(tasks)*TaskTimeout; in practice that's
	// a handful of milliseconds. The master loop continues in the
	// background until Stop.
	janitor.Start(context.Background())

	// Routing layers, outer to inner (see internal/api/router.go for the
	// full route table):
	//   CORS
	//   apiMux              — dispatches by URL pattern
	//     ├ POST /api/login                 (no auth — issues session cookie)
	//     ├ POST /api/logout                (no auth — clears cookie; safe to be open)
	//     ├ POST /api/apps/{name}/trigger   (no auth, Bearer verified in handler)
	//     ├ /api/                           (everything else: wrapped in SessionAuth)
	//     └ /                               (UI: served as-is)
	//
	// /healthz sits outside this chain so orchestrators (Docker
	// HEALTHCHECK, k8s readinessProbe, load balancers) can probe it without
	// a session cookie or CORS negotiation.
	root := api.Router(handlers, sessions)
	healthMux := api.HealthRouter(handlers)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.HealthWrap(root, healthMux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("nanoku listening on %s (admin user=%s)", cfg.Listen, cfg.AdminUser)
		log.Printf("db=%s caddyfile=%s caddy-mode=%s", cfg.DBPath, cfg.CaddyfilePath, cfg.CaddyMode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}

	// HTTP listener is closed; give in-flight deploys a longer window to
	// finish (they may still be pulling images, recreating containers,
	// regenerating the Caddyfile) before forcibly failing any stragglers.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelDrain()
	handlers.DrainInflight(drainCtx, 30*time.Second)

	// The Janitor's master loop and any in-flight task get a short
	// window to finish. Cleanup tasks are short and stateless; if one
	// is mid-run when we cancel, the next Start on the next boot
	// picks up the work. Same graceful contract as DeployLock: try
	// to wait, then give up.
	janitor.Stop(2 * time.Second)
}
