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
	// Best-effort expired-session cleanup on boot; ignore errors.
	if _, err := sessions.PurgeExpired(context.Background()); err != nil {
		log.Printf("purge expired sessions: %v", err)
	}
	// Drop login-attempt entries that have aged out of the 1-minute
	// window. Otherwise IPs that tried once and never returned stay in
	// the map forever, and an attacker can walk a wide source range to
	// grow it without bound.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			sessions.PurgeStaleAttempts()
		}
	}()

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
		DeployLogs:      api.NewDeployLogHub(),
		Sessions:        sessions,
		Secret:          sealer,
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", handlers.Me)
	mux.HandleFunc("POST /api/me/password", handlers.ChangePassword)
	mux.HandleFunc("GET /api/sites", handlers.ListSites)
	mux.HandleFunc("POST /api/sites", handlers.CreateSite)
	mux.HandleFunc("PUT /api/sites/{id}", handlers.UpdateSite)
	mux.HandleFunc("DELETE /api/sites/{id}", handlers.DeleteSite)
	mux.HandleFunc("POST /api/sites/{id}/toggle", handlers.ToggleSite)
	mux.HandleFunc("GET /api/status", handlers.Status)
	mux.HandleFunc("GET /api/caddyfile", handlers.CaddyfilePreview)

	mux.HandleFunc("GET /api/apps", handlers.ListApps)
	mux.HandleFunc("POST /api/apps", handlers.CreateApp)
	mux.HandleFunc("GET /api/apps/{id}", handlers.GetApp)
	mux.HandleFunc("PUT /api/apps/{id}", handlers.UpdateApp)
	mux.HandleFunc("DELETE /api/apps/{id}", handlers.DeleteApp)
	mux.HandleFunc("POST /api/apps/{id}/deployments", handlers.DeployApp)
	mux.HandleFunc("POST /api/apps/{id}/start", handlers.StartApp)
	mux.HandleFunc("POST /api/apps/{id}/stop", handlers.StopApp)
	mux.HandleFunc("POST /api/apps/{id}/restart", handlers.RestartApp)
	mux.HandleFunc("GET /api/apps/{id}/logs", handlers.AppLogs)
	mux.HandleFunc("GET /api/apps/{id}/logs/stream", handlers.AppLogsStream)
	mux.HandleFunc("GET /api/apps/{id}/deployments/{did}/logs/stream", handlers.DeployLogStream)
	mux.HandleFunc("GET /api/apps/{id}/env", handlers.ListAppEnvVars)
	mux.HandleFunc("PUT /api/apps/{id}/env", handlers.ReplaceAppEnvVars)
	mux.HandleFunc("GET /api/apps/{id}/volumes", handlers.ListAppVolumes)
	mux.HandleFunc("PUT /api/apps/{id}/volumes", handlers.ReplaceAppVolumes)
	mux.HandleFunc("GET /api/apps/{id}/deployments", handlers.ListAppDeploys)
	mux.HandleFunc("POST /api/apps/{id}/rotate-trigger-token", handlers.RotateTriggerToken)
	mux.HandleFunc("POST /api/apps/{id}/rollback", handlers.RollbackApp)
	mux.HandleFunc("POST /api/apps/{id}/exposed-ports/import", handlers.ImportExposedPorts)

	mux.HandleFunc("GET /api/system/status", handlers.SystemStatus)
	mux.HandleFunc("GET /api/system/logs", handlers.SystemLogs)
	mux.HandleFunc("GET /api/system/logs/stream", handlers.SystemLogsStream)
	mux.HandleFunc("GET /api/system/reconcile", handlers.SystemReconcile)
	mux.HandleFunc("POST /api/system/reconcile", handlers.SystemReconcileApply)
	mux.HandleFunc("DELETE /api/system/orphans/{name}", handlers.SystemRemoveOrphan)

	mux.HandleFunc("GET /api/dashboard", handlers.Dashboard)

	// Routing layers, outer to inner:
	//   CORS
	//   apiMux              — dispatches by URL pattern
	//     ├ POST /api/login                 (no auth — issues session cookie)
	//     ├ POST /api/logout                (no auth — clears cookie; safe to be open)
	//     ├ GET  /api/me                    (auth — heartbeat check from UI)
	//     ├ POST /api/me/password           (auth — change own password)
	//     ├ POST /api/apps/{name}/trigger   (no auth, Bearer verified in handler)
	//     ├ /api/                           (everything else: wrapped in SessionAuth)
	//     └ /                               (UI: served as-is)
	//
	// SessionAuth replaces the old BasicAuth. Login is exposed at the top
	// level so the auth check does not block the login attempt.
	authedInternal := api.SessionAuth(sessions)(mux)

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("POST /api/login", handlers.Login)
	apiMux.HandleFunc("POST /api/logout", handlers.Logout)
	apiMux.HandleFunc("POST /api/apps/{name}/trigger", handlers.Trigger)
	apiMux.Handle("/api/", authedInternal)
	apiMux.Handle("/", api.UIHandler())

	root := api.CORS(apiMux)

	// /healthz sits outside the auth + CORS chain so orchestrators (Docker
	// HEALTHCHECK, k8s readinessProbe, load balancers) can probe it without
	// a session cookie or CORS negotiation. Built as a tiny dedicated mux
	// rather than registered on root directly because root is already wrapped
	// as an http.Handler by CORS.
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("GET /healthz", handlers.Healthz)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           healthWrap(root, healthMux),
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
}

// healthWrap layers /healthz in front of the rest of the handler so the
// probe endpoint bypasses CORS + session auth + the UI handler. The order
// matters: /healthz is matched first when the URL is exactly /healthz,
// otherwise the request falls through to the primary handler.
func healthWrap(primary http.Handler, health http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			health.ServeHTTP(w, r)
			return
		}
		primary.ServeHTTP(w, r)
	})
}