package api

import (
	"context"
	"net/http"
	"time"

	"github.com/isaced/nanoku/internal/db"
)

// HealthResponse is the JSON shape returned by /healthz. `Status` is the
// top-level summary; the individual checks live in Checks. The handler
// returns 503 when any check fails so orchestrators (Docker HEALTHCHECK,
// Kubernetes readinessProbe, load balancers) treat it as "do not send
// traffic". The body always includes the per-check state so an operator
// can curl the endpoint and see which dependency is degraded.
type HealthResponse struct {
	Status string                  `json:"status"` // "ok" | "degraded"
	Checks map[string]HealthCheck  `json:"checks"`
}

// HealthCheck is the per-dependency result. `OK` is the only field an
// orchestrator needs; `Error` exists for human debugging.
type HealthCheck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Healthz is the orchestrator-facing liveness/readiness probe at
// GET /healthz. Returns 200 when DB and Docker (if configured) are both
// reachable; 503 otherwise. Mounted OUTSIDE the SessionAuth chain in
// main.go so probes don't need a cookie.
//
// Scope is intentionally narrow: it does not check every container's
// status (that's what /api/system/status is for) or every Caddyfile
// rewrite (that's reconciliation's job). /healthz answers exactly one
// question: "is this process capable of serving requests right now?"
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]HealthCheck{
		"db": pingDB(ctx, h.DB),
	}
	if !h.SkipCaddyReload {
		checks["docker"] = pingDocker(ctx, h)
	}

	allOK := true
	for _, c := range checks {
		if !c.OK {
			allOK = false
			break
		}
	}

	resp := HealthResponse{
		Status: "ok",
		Checks: checks,
	}
	code := http.StatusOK
	if !allOK {
		resp.Status = "degraded"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, resp)
}

// pingDB returns ok=true when the underlying *sql.DB answers a cheap
// SELECT 1 within the caller's context deadline. A nil DB or any error
// short-circuits to ok=false.
func pingDB(ctx context.Context, d *db.DB) HealthCheck {
	if d == nil {
		return HealthCheck{Error: "db not initialized"}
	}
	var one int
	if err := d.Conn().QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return HealthCheck{Error: err.Error()}
	}
	if one != 1 {
		return HealthCheck{Error: "unexpected query result"}
	}
	return HealthCheck{OK: true}
}

// pingDocker returns ok=true when the Docker manager is wired and a
// /var/run/docker.sock ping succeeds. A nil manager is reported as ok
// because SkipCaddyReload mode runs without one — the caller decides
// whether to call this check at all based on configuration.
func pingDocker(ctx context.Context, h *Handlers) HealthCheck {
	if h.Docker == nil {
		return HealthCheck{Error: "docker manager not initialized"}
	}
	if err := h.Docker.Ping(ctx); err != nil {
		return HealthCheck{Error: err.Error()}
	}
	return HealthCheck{OK: true}
}