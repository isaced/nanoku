#!/usr/bin/env bash
# scripts/e2e/teardown.sh - Stop nanoku, remove containers/networks/volumes created by E2E, clean the temp dir.
#
# Usage: ./teardown.sh
# Idempotent: safe to run repeatedly (no-op if absent).

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

echo "stopping nanoku ..."
stop_server

echo "removing e2e containers (label=nanoku-e2e=true) ..."
labeled=$(docker ps -aq --filter "label=nanoku-e2e=true" 2>/dev/null || true)
if [ -n "$labeled" ]; then
  docker rm -f $labeled >/dev/null 2>&1 || true
fi

# Also clean any nanoku-* prefixed containers (app containers deployed during tests)
# Use docker ps -a to list all relevant containers
app_containers=$(docker ps -aq --filter "label=nanoku.managed=true" --filter "label=nanoku.role=app" 2>/dev/null || true)
if [ -n "$app_containers" ]; then
  echo "removing app containers ..."
  docker rm -f $app_containers >/dev/null 2>&1 || true
fi

# Capture caddy container logs to the workdir before removing it. Goes into
# the e2e-logs artifact on CI failure (see .github/workflows/e2e.yml). Helpful
# when the proxy test fails because caddy wasn't bound to :80 — without this
# the caddy stderr/stdout (the only place caddy reports bind errors) is lost
# the instant `docker rm` runs. Best-effort: never fail teardown over it.
if [ -n "${E2E_WORKDIR:-}" ] && [ -d "$E2E_WORKDIR" ]; then
  if docker inspect "$E2E_CADDY_CONTAINER" >/dev/null 2>&1; then
    docker logs "$E2E_CADDY_CONTAINER" >"$E2E_WORKDIR/caddy.log" 2>&1 || true
  fi
fi

echo "removing caddy container / network / volume ..."
docker rm -f "$E2E_CADDY_CONTAINER" >/dev/null 2>&1 || true
docker network rm "$E2E_CADDY_NETWORK" >/dev/null 2>&1 || true
docker volume rm "$E2E_CADDY_VOLUME" >/dev/null 2>&1 || true

# Remove the workdir unless an escape hatch is set. CI sets
# E2E_SKIP_WORKDIR_RM=1 so a failed run's server.log / Caddyfile /
# nanoku.db / deploy-logs/ survive for the workflow's upload-artifact
# step. Docker cleanup above still runs regardless, so the runner stays
# clean either way. Local `make test-e2e` leaves this unset -> wiped.
if [ -z "${E2E_SKIP_WORKDIR_RM:-}" ]; then
  echo "removing workdir $E2E_WORKDIR ..."
  if [ -d "$E2E_WORKDIR" ]; then
    rm -rf "$E2E_WORKDIR"
  fi
else
  echo "E2E_SKIP_WORKDIR_RM set; keeping workdir $E2E_WORKDIR for artifact upload"
fi

echo "done"
