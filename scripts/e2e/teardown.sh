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

echo "removing caddy container / network / volume ..."
docker rm -f "$E2E_CADDY_CONTAINER" >/dev/null 2>&1 || true
docker network rm "$E2E_CADDY_NETWORK" >/dev/null 2>&1 || true
docker volume rm "$E2E_CADDY_VOLUME" >/dev/null 2>&1 || true

echo "removing workdir $E2E_WORKDIR ..."
if [ -d "$E2E_WORKDIR" ]; then
  rm -rf "$E2E_WORKDIR"
fi

echo "done"
