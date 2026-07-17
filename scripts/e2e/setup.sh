#!/usr/bin/env bash
# scripts/e2e/setup.sh - Prepare a temp .env / DB dir, start nanoku in the background, wait until /healthz responds.
#
# Usage: ./setup.sh
# Exit: 0 success, non-zero failure
#
# Side effects:
#   - $E2E_WORKDIR (default /tmp/nanoku-e2e.XXXXXX) gets a bunch of temp files written to it
#   - Start `go run .` in the background listening on :18080; PID stored in $E2E_SERVER_PID_FILE
#   - Let nanoku use real docker (dm initialized) so caddy container and app deployment work
#
# This script doesn't start/stop the caddy container itself; nanoku ensures it. But the caddy
# container / network / volume all use the nanoku-e2e-* namespace, isolated from local dev.

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# ---- Preconditions -----------------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
  echo -e "${RED}go not found in PATH${NC}" >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  echo -e "${RED}curl not found in PATH${NC}" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo -e "${RED}jq not found in PATH (brew install jq)${NC}" >&2
  exit 1
fi
if ! require_docker; then
  echo -e "${RED}docker not available; cannot run E2E (test-00 and other basic cases don't need it, but test-60/61/70 do)${NC}" >&2
  echo "  -> OK on GitHub Actions ubuntu-latest runners; install docker locally if missing" >&2
  exit 1
fi

# Pre-pull the caddy:2 image; docker hub often hits EOF. Retrying here is more reliable than letting nanoku pull it internally.
# Done before mkdir so a failure won't leave a half-created workdir.
echo "pre-pulling caddy:2 (may retry on EOF) ..."
for attempt in 1 2 3 4 5; do
  if docker pull --quiet "$E2E_CADDY_IMAGE" >/dev/null 2>&1; then
    echo "  pulled on attempt $attempt"
    break
  fi
  echo "  attempt $attempt failed, retrying in $((attempt*2))s ..."
  sleep $((attempt*2))
  if [ "$attempt" = "5" ]; then
    echo -e "${YELLOW}WARN: caddy:2 pull failed after 5 attempts. E2E will continue but caddy-based cases will fail.${NC}" >&2
  fi
done

# ---- Prepare directories -----------------------------------------------------------
mkdir -p "$E2E_WORKDIR" "$E2E_DEPLOY_LOG_DIR"
# Note: don't touch the Caddyfile. docker.EnsureCaddyContainer calls ensureCaddyfileExists itself
# to write a placeholder (an empty file makes caddy fail with "EOF" on startup).

# ---- Write temp .env --------------------------------------------------------
# NANOKU_LISTEN=:18080         admin API port
# NANOKU_CADDY_HTTP_PORT=18080 caddy :80 mapped to host port (E2E curls this directly)
# Use the e2e namespace, fully isolated from local dev
cat > "$E2E_ENV" <<EOF
# For nanoku (NANOKU_*)
NANOKU_LISTEN=:18080
NANOKU_DB=${E2E_DB}
NANOKU_CADDYFILE=${E2E_CADDYFILE}
NANOKU_DEPLOY_LOG_DIR=${E2E_DEPLOY_LOG_DIR}
NANOKU_ADMIN_USER=${E2E_ADMIN_USER}
NANOKU_ADMIN_PASSWORD=${E2E_ADMIN_PASSWORD}
NANOKU_SECRET_KEY=${E2E_SECRET_KEY}
NANOKU_CADDY_CONTAINER=${E2E_CADDY_CONTAINER}
NANOKU_CADDY_NETWORK=${E2E_CADDY_NETWORK}
NANOKU_CADDY_VOLUME=${E2E_CADDY_VOLUME}
NANOKU_CADDY_IMAGE=${E2E_CADDY_IMAGE}
NANOKU_KEEP_DEPLOY_DAYS=1
# Make clientIP trust X-Forwarded-For. E2E uses this to give each case a "dedicated IP", bypassing
# the /api/login 5/min rate limit (per-IP). In production only enable behind a reverse proxy.
NANOKU_TRUST_PROXY=true

# For E2E test scripts (E2E_*); lib.sh sources this file
E2E_BASE_URL=${E2E_BASE_URL}
E2E_ADMIN_USER=${E2E_ADMIN_USER}
E2E_ADMIN_PASSWORD=${E2E_ADMIN_PASSWORD}
E2E_SECRET_KEY=${E2E_SECRET_KEY}
E2E_WORKDIR=${E2E_WORKDIR}
E2E_COOKIE=${E2E_COOKIE}
E2E_DB=${E2E_DB}
E2E_CADDYFILE=${E2E_CADDYFILE}
E2E_DEPLOY_LOG_DIR=${E2E_DEPLOY_LOG_DIR}
E2E_CADDY_CONTAINER=${E2E_CADDY_CONTAINER}
E2E_CADDY_NETWORK=${E2E_CADDY_NETWORK}
E2E_CADDY_VOLUME=${E2E_CADDY_VOLUME}
E2E_CADDY_IMAGE=${E2E_CADDY_IMAGE}
E2E_CADDY_HTTP_PORT=${E2E_CADDY_HTTP_PORT}
E2E_CADDY_ADMIN_PORT=${E2E_CADDY_ADMIN_PORT}
E2E_SERVER_LOG=${E2E_SERVER_LOG}
E2E_SERVER_PID_FILE=${E2E_SERVER_PID_FILE}
EOF

# ---- Start nanoku ----------------------------------------------------------
# Run from repo root, since go.mod, internal/api/ui.go (go:embed dist), etc. live there
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

# Publishing caddy container's :80 to host E2E_CADDY_HTTP_PORT so E2E can curl directly
# would be ugly via a temp patch (requires changing the docker manager), so after nanoku starts
# should we run `docker run` to ensure the port is published? Simpler: let caddy use host
# networking so :80 is directly reachable -- but host networking needs --network=host, which
# requires privileges under Docker.
#
# Compromise: let nanoku start caddy normally (on the nanoku-net network), then in E2E:
#   1. `docker exec` into the caddy container to curl
#   2. Or map the caddy container port out -- requires patching docker.EnsureCaddyContainer
#
# Chose route (1): proxy tests go through `docker exec caddy curl ...`, avoiding source changes.
# This way nanoku is unaware it's being tested; all external access originates inside the caddy container.

echo "starting nanoku (log: $E2E_SERVER_LOG) ..."
set -a
# shellcheck disable=SC1090
source "$E2E_ENV"
set +a

# First start: truncate the old log. start_server appends with >>, so ensure_caddy_up's
# restart retries keep each boot's log.
: > "$E2E_SERVER_LOG"
start_server

# ---- Wait for readiness -------------------------------------------------------------
echo "waiting for /healthz ..."
if ! wait_for_server 120; then
  echo -e "${RED}server failed to start${NC}" >&2
  stop_server
  exit 1
fi
echo -e "${GREEN}server up${NC}  pid=$(cat "$E2E_SERVER_PID_FILE")  url=$E2E_BASE_URL"

# ---- Clean up any leftover E2E containers (previous run didn't clean up) -------------------------
# Delete by label to avoid collateral damage
local_remaining=$(docker ps -aq --filter "label=nanoku-e2e=true" 2>/dev/null || true)
if [ -n "$local_remaining" ]; then
  echo "removing leftover e2e containers: $local_remaining"
  docker rm -f $local_remaining >/dev/null 2>&1 || true
fi
# Note: don't docker rm -f nanoku-e2e-caddy here. nanoku created it at boot time
# (main.go EnsureCaddyContainer), and HTTP /api/system/reconcile
# won't recreate caddy (ApplyReconcile only clears the current_container edge, it doesn't call
# EnsureCaddyContainer). Force-removing it = nobody can bring it back up = E2E will hang.
# What really needs cleaning is stale instances from a previous run that didn't exit cleanly: check
# state and only remove if it exists but isn't running, so nanoku boot recreates it.
caddy_state=$(docker inspect "$E2E_CADDY_CONTAINER" --format '{{.State.Status}}' 2>/dev/null || true)
if [ -n "$caddy_state" ] && [ "$caddy_state" != "running" ]; then
  echo "removing stale caddy container (state=$caddy_state)"
  docker rm -f "$E2E_CADDY_CONTAINER" >/dev/null 2>&1 || true
fi
# Same for network: only remove when nothing is using it
docker network rm "$E2E_CADDY_NETWORK" >/dev/null 2>&1 || true

echo
# Wait for the caddy container to be running. EnsureCaddyContainer at nanoku boot is the only
# path that creates/starts caddy (HTTP reconcile doesn't do this), so here we just wait
# for it to be ready and no longer call /api/system/reconcile.
# Known issue: under macOS + OrbStack the caddy container is occasionally SIGKILL'd (exit 137)
# ~1s after boot. In that case restarting nanoku re-triggers EnsureCaddyContainer, so here we
# retry restart a few times before timing out.
echo "ensuring caddy container is up (may restart nanoku) ..."
if ! ensure_caddy_up 60; then
  echo -e "${RED}caddy not up after retries; aborting E2E setup${NC}" >&2
  echo "--- last 30 lines of server log ---" >&2
  tail -30 "$E2E_SERVER_LOG" >&2 2>/dev/null || true
  echo "--- caddy container state ---" >&2
  docker inspect "$E2E_CADDY_CONTAINER" --format '{{.State.Status}} (exitCode={{.State.ExitCode}})' >&2 2>/dev/null || echo "  (no container)" >&2
  docker logs "$E2E_CADDY_CONTAINER" 2>&1 | tail -20 >&2 || true
  stop_server
  exit 1
fi
echo -e "  ${GREEN}caddy up${NC}"

echo
echo "E2E environment ready"
echo "  workdir:    $E2E_WORKDIR"
echo "  base url:   $E2E_BASE_URL"
echo "  admin:      $E2E_ADMIN_USER / $E2E_ADMIN_PASSWORD"
echo "  server log: $E2E_SERVER_LOG"
