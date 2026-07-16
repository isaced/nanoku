#!/usr/bin/env bash
# nanoku uninstall — stop the service, remove the systemd unit, drop config.
# Persistent data (/var/lib/nanoku) and the image are KEPT by default so a
# re-install can pick up where you left off. Pass --purge to wipe everything.
#
# Usage:
#   sudo bash scripts/uninstall.sh        # keep data + image
#   sudo bash scripts/uninstall.sh --purge  # wipe data + image too
set -euo pipefail

PURGE=0
for arg in "$@"; do
    case "$arg" in
        --purge) PURGE=1 ;;
        -h|--help)
            sed -n '2,12p' "$0"
            exit 0
            ;;
        *) echo "unknown arg: $arg" >&2; exit 2 ;;
    esac
done

ENV_FILE="${NANOKU_CONFIG_DIR:-/etc/nanoku}/.env"
COMPOSE_DIR="$(dirname "$ENV_FILE")"
DATA_DIR="${NANOKU_DATA_DIR:-/var/lib/nanoku}"

if [ "$(id -u)" -ne 0 ]; then
    echo "error: please run as root (sudo bash uninstall.sh)" >&2
    exit 1
fi

# 1) stop the service
if systemctl list-unit-files nanoku.service >/dev/null 2>&1; then
    echo "==> Stopping nanoku service"
    systemctl stop nanoku.service || true
    systemctl disable nanoku.service || true
    rm -f /etc/systemd/system/nanoku.service
    systemctl daemon-reload
fi

# 2) drop the container + managed caddy + the nanoku-net network
if command -v docker >/dev/null 2>&1; then
    if [ -f "$COMPOSE_DIR/docker-compose.yml" ]; then
        echo "==> Removing nanoku container"
        cd "$COMPOSE_DIR" && docker compose down --remove-orphans || true
    fi
    # The Caddy container, app containers, and the nanoku-net network are
    # created at runtime by nanoku itself, not by this installer.
    for c in $(docker ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^(nanoku|nanoku-caddy)$' || true); do
        docker rm -f "$c" >/dev/null 2>&1 || true
    done
    docker network rm nanoku-net >/dev/null 2>&1 || true
fi

# 3) drop config (always)
echo "==> Removing config at ${COMPOSE_DIR}"
rm -rf "$COMPOSE_DIR"

# 4) optionally drop data + image
if [ "$PURGE" -eq 1 ]; then
    echo "==> Purging data at ${DATA_DIR}"
    rm -rf "$DATA_DIR"
    if command -v docker >/dev/null 2>&1; then
        echo "==> Removing nanoku image"
        docker image rm -f "$(docker image ls --format '{{.Repository}}:{{.Tag}}' | grep -E '^(isaced|ghcr.io/isaced)/nanoku' | head -1)" >/dev/null 2>&1 || true
    fi
    echo "    purging also removed: persistent SQLite DB, encrypted secrets, deploy logs."
else
    echo "==> Keeping data at ${DATA_DIR} (pass --purge to wipe)"
fi

echo
echo "nanoku uninstalled."
if [ "$PURGE" -eq 0 ]; then
    echo "Re-install with: curl -fsSL https://raw.githubusercontent.com/isaced/nanoku/main/scripts/install.sh | sudo bash"
fi
