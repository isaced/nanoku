package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/isaced/nanoku/internal/api"
	"github.com/isaced/nanoku/internal/config"
	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/docker"
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

	handlers := &api.Handlers{
		DB:              database,
		Docker:          dm,
		CaddyfilePath:   cfg.CaddyfilePath,
		ACMEEmail:       cfg.ACMEEmail,
		SkipCaddyReload: cfg.SkipCaddyReload,
		SelfContainer:   cfg.SelfContainer,
		ComposeBaseDir:  cfg.ComposeBaseDir,
		DeployLock:      api.NewDeployLock(),
		Sessions:        sessions,
	}

	mux := http.NewServeMux()
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
	mux.HandleFunc("GET /api/apps/{id}/env", handlers.ListAppEnvVars)
	mux.HandleFunc("PUT /api/apps/{id}/env", handlers.ReplaceAppEnvVars)
	mux.HandleFunc("GET /api/apps/{id}/deployments", handlers.ListAppDeploys)
	mux.HandleFunc("POST /api/apps/{id}/rotate-trigger-token", handlers.RotateTriggerToken)

	mux.HandleFunc("GET /api/system/status", handlers.SystemStatus)
	mux.HandleFunc("GET /api/system/logs", handlers.SystemLogs)

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

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           root,
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
}