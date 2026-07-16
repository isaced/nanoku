#!/usr/bin/env bash
# nanoku upgrade — pull the latest image and restart the running container.
# Reads /etc/nanoku/.env so the same registry / tag / data dir are reused.
#
# Usage:
#   sudo bash scripts/upgrade.sh           # upgrade to the version pinned in .env
#   sudo NANOKU_VERSION=v0.2.0 bash scripts/upgrade.sh   # one-off version pin
set -euo pipefail

ENV_FILE="${NANOKU_CONFIG_DIR:-/etc/nanoku}/.env"
if [ ! -f "$ENV_FILE" ]; then
    echo "error: $ENV_FILE not found — is nanoku installed?" >&2
    exit 1
fi

# Load existing env, then let NANOKU_VERSION / NANOKU_IMAGE overrides win.
set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a
NANOKU_VERSION="${NANOKU_VERSION:-${NANOKU_IMAGE##*:}}"
NANOKU_IMAGE="${NANOKU_IMAGE:-isaced/nanoku:${NANOKU_VERSION}}"

DOCKER_BIN="${DOCKER_BIN:-$(command -v docker || echo /usr/bin/docker)}"
COMPOSE_DIR="$(dirname "$ENV_FILE")"

echo "==> Pulling ${NANOKU_IMAGE}"
"${DOCKER_BIN}" pull "${NANOKU_IMAGE}"

echo "==> Recreating nanoku container"
cd "$COMPOSE_DIR"
"${DOCKER_BIN}" compose up -d --remove-orphans

CURRENT="$(${DOCKER_BIN} inspect --format='{{.Config.Image}}' nanoku 2>/dev/null || echo unknown)"
echo "==> nanoku is now running: ${CURRENT}"
echo "    (logs: journalctl -u nanoku -f)"
