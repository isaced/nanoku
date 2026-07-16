package api

// router.go is the single place that defines HTTP routes for nanoku's
// admin API + embedded UI. main.go used to carry ~30 lines of
// mux.HandleFunc calls and the CORS / auth wrapping logic; moving them
// here keeps wire-up (config loading, DB open, signal handling) out of
// the same file as the route table, so adding a new endpoint only
// touches the handler's own file + this one.

import (
	"net/http"
)

// Router returns the CORS-wrapped HTTP handler that serves the admin
// API + embedded SPA. The returned chain is, outermost-innermost:
//
//	CORS → apiMux → {
//	    POST /api/login                  (open — issues session cookie)
//	    POST /api/logout                 (open — clears cookie; safe)
//	    POST /api/apps/{name}/trigger    (open — Bearer-verified in handler)
//	    /api/...                         (everything else: SessionAuth)
//	    /                                (UI fallback)
//	}
//
// The /healthz endpoint is NOT registered here — orchestrators want
// to probe it without going through CORS or auth. Use HealthRouter
// for that and wrap it in front of the result of Router (see
// main.go's healthWrap).
//
// SessionAuth is a hard requirement: passing a nil store panics. The
// store is owned by main.go (booted from the session table) and
// passed in here for composition; this keeps the router itself
// stateless so it can be re-built in tests.
func Router(h *Handlers, sessions *SessionStore) http.Handler {
	if sessions == nil {
		panic("api.Router: sessions is required")
	}
	authed := SessionAuth(sessions)(adminMux(h))

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("POST /api/login", h.Login)
	apiMux.HandleFunc("POST /api/logout", h.Logout)
	apiMux.HandleFunc("POST /api/apps/{name}/trigger", h.Trigger)
	apiMux.Handle("/api/", authed)
	apiMux.Handle("/", UIHandler())

	return CORS(apiMux)
}

// adminMux registers every authenticated admin endpoint. Kept
// separate from the unauthenticated POST /api/login + /trigger
// surface so the auth check doesn't get in the way of the login
// flow itself. Endpoints are grouped by resource to keep the table
// scannable: /api/me*, /api/sites*, /api/apps*, /api/system*, plus
// the singleton /api/status, /api/caddyfile, /api/dashboard.
//
// New endpoints go here. The method must match the handler's
// expectations: the router uses Go 1.22+ pattern syntax, so use
// `{id}` for path parameters and `r.PathValue("id")` in the
// handler. Path parameters that are non-integer (e.g. the app name
// in /api/apps/{name}/trigger) should use a different name
// ("name" instead of "id") so the handler's pathID helper isn't
// called on it.
func adminMux(h *Handlers) *http.ServeMux {
	mux := http.NewServeMux()

	// --- /api/me (current user) --------------------------------
	mux.HandleFunc("GET /api/me", h.Me)
	mux.HandleFunc("POST /api/me/password", h.ChangePassword)

	// --- /api/sites --------------------------------------------
	mux.HandleFunc("GET /api/sites", h.ListSites)
	mux.HandleFunc("POST /api/sites", h.CreateSite)
	mux.HandleFunc("PUT /api/sites/{id}", h.UpdateSite)
	mux.HandleFunc("DELETE /api/sites/{id}", h.DeleteSite)
	mux.HandleFunc("POST /api/sites/{id}/toggle", h.ToggleSite)

	// --- /api/apps ---------------------------------------------
	mux.HandleFunc("GET /api/apps", h.ListApps)
	mux.HandleFunc("POST /api/apps", h.CreateApp)
	mux.HandleFunc("GET /api/apps/{id}", h.GetApp)
	mux.HandleFunc("PUT /api/apps/{id}", h.UpdateApp)
	mux.HandleFunc("DELETE /api/apps/{id}", h.DeleteApp)
	mux.HandleFunc("POST /api/apps/{id}/deployments", h.DeployApp)
	mux.HandleFunc("POST /api/apps/{id}/start", h.StartApp)
	mux.HandleFunc("POST /api/apps/{id}/stop", h.StopApp)
	mux.HandleFunc("POST /api/apps/{id}/restart", h.RestartApp)
	mux.HandleFunc("POST /api/apps/{id}/rotate-trigger-token", h.RotateTriggerToken)
	mux.HandleFunc("POST /api/apps/{id}/exposed-ports/import", h.ImportExposedPorts)
	mux.HandleFunc("GET /api/apps/{id}/logs", h.AppLogs)
	mux.HandleFunc("GET /api/apps/{id}/logs/stream", h.AppLogsStream)
	mux.HandleFunc("GET /api/apps/{id}/containers", h.AppContainers)
	mux.HandleFunc("GET /api/apps/{id}/env", h.ListAppEnvVars)
	mux.HandleFunc("PUT /api/apps/{id}/env", h.ReplaceAppEnvVars)
	mux.HandleFunc("GET /api/apps/{id}/volumes", h.ListAppVolumes)
	mux.HandleFunc("PUT /api/apps/{id}/volumes", h.ReplaceAppVolumes)
	mux.HandleFunc("GET /api/apps/{id}/deployments", h.ListAppDeploys)
	mux.HandleFunc("GET /api/apps/{id}/deployments/{did}/logs/stream", h.DeployLogStream)

	// --- /api/system -------------------------------------------
	mux.HandleFunc("GET /api/system/status", h.SystemStatus)
	mux.HandleFunc("GET /api/system/logs", h.SystemLogs)
	mux.HandleFunc("GET /api/system/logs/stream", h.SystemLogsStream)
	mux.HandleFunc("GET /api/system/reconcile", h.SystemReconcile)
	mux.HandleFunc("POST /api/system/reconcile", h.SystemReconcileApply)
	mux.HandleFunc("DELETE /api/system/orphans/{name}", h.SystemRemoveOrphan)

	// --- singletons --------------------------------------------
	mux.HandleFunc("GET /api/status", h.Status)
	mux.HandleFunc("GET /api/caddyfile", h.CaddyfilePreview)
	mux.HandleFunc("GET /api/dashboard", h.Dashboard)

	return mux
}

// HealthRouter returns a tiny http.Handler that serves only /healthz,
// bypassing CORS and session auth. Orchestrators (Docker
// HEALTHCHECK, Kubernetes readinessProbe, load balancers) need a
// liveness probe that doesn't negotiate a session cookie or CORS
// preflight; the wider Router returns a handler that's wrapped in
// both.
//
// Wire it in main.go with HealthWrap, which layers the health
// handler in front of the primary handler — /healthz hits the
// health handler first, everything else falls through.
func HealthRouter(h *Handlers) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.Healthz)
	return mux
}

// HealthWrap layers the health handler in front of primary so the
// /healthz endpoint bypasses CORS + session auth + the UI handler.
// The order matters: /healthz is matched first when the URL is
// exactly /healthz; any other path falls through to the primary
// (admin) handler.
func HealthWrap(primary, health http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			health.ServeHTTP(w, r)
			return
		}
		primary.ServeHTTP(w, r)
	})
}
