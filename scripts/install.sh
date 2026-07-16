#!/usr/bin/env bash
# nanoku installer — pulls and runs the nanoku container, installs a systemd
# unit so it survives reboots. Docker is the only host-side dependency.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/isaced/nanoku/main/scripts/install.sh | sudo bash
#   curl -fsSL ... | sudo NANOKU_VERSION=v0.1.0 bash
#   curl -fsSL ... | sudo NANOKU_ADMIN_PASSWORD=hunter2 bash
#
# Environment variables (all optional):
#   NANOKU_VERSION         - image tag, e.g. v0.1.0 (default: latest)
#   NANOKU_IMAGE           - full image ref (default: isaced/nanoku:<VERSION>)
#   NANOKU_ADMIN_USER      - seed admin username (default: admin)
#   NANOKU_ADMIN_PASSWORD  - seed admin password (default: auto-generated, printed at the end)
#   NANOKU_SECRET_KEY      - AES-256-GCM passphrase (default: auto-generated, printed at the end)
#   NANOKU_LISTEN          - nanoku listen address (default: :8080)
#   NANOKU_DATA_DIR        - persistent data dir (default: /var/lib/nanoku)
#   NANOKU_CONFIG_DIR      - config + compose + .env dir (default: /etc/nanoku)
#   NANOKU_IMAGE_REGISTRY  - "dockerhub" or "ghcr" (default: dockerhub)

set -o pipefail

# ---------- pretty output ----------
if [ -t 1 ]; then
    BOLD="\033[1m"; DIM="\033[2m"; GREEN="\033[32m"; YELLOW="\033[33m"; RED="\033[31m"; RESET="\033[0m"
else
    BOLD=""; DIM=""; GREEN=""; YELLOW=""; RED=""; RESET=""
fi
log()  { echo -e "${DIM}[$(date +%H:%M:%S)]${RESET} $*"; }
ok()   { echo -e "${GREEN}✓${RESET} $*"; }
warn() { echo -e "${YELLOW}!${RESET} $*"; }
die()  { echo -e "${RED}✗${RESET} $*" >&2; exit 1; }
section() { echo -e "\n${BOLD}==>${RESET} ${BOLD}$*${RESET}"; }

# ---------- defaults ----------
NANOKU_VERSION="${NANOKU_VERSION:-latest}"
case "${NANOKU_IMAGE_REGISTRY:-dockerhub}" in
    ghcr)      NANOKU_IMAGE="${NANOKU_IMAGE:-ghcr.io/isaced/nanoku:${NANOKU_VERSION}}" ;;
    dockerhub|*) NANOKU_IMAGE="${NANOKU_IMAGE:-isaced/nanoku:${NANOKU_VERSION}}" ;;
esac
NANOKU_LISTEN="${NANOKU_LISTEN:-:8080}"
NANOKU_DATA_DIR="${NANOKU_DATA_DIR:-/var/lib/nanoku}"
NANOKU_CONFIG_DIR="${NANOKU_CONFIG_DIR:-/etc/nanoku}"
NANOKU_ADMIN_USER="${NANOKU_ADMIN_USER:-admin}"

REPO_OWNER="isaced"
REPO_NAME="nanoku"
GITHUB_API="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"

# ---------- preflight ----------
section "Preflight checks"

[ "$(id -u)" -eq 0 ] || die "Please run as root (sudo bash install.sh)"

case "$(uname -s)" in
    Linux) ;;
    Darwin) die "macOS is not supported. Run nanoku natively on Linux or use docker compose." ;;
    *)      die "Unsupported OS: $(uname -s)" ;;
esac

if [ -f /.dockerenv ] || grep -qE '/(docker|lxc)/' /proc/1/cgroup 2>/dev/null; then
    die "Running inside a container is not supported. Run this on a Linux host (or VM)."
fi

if ! command -v systemctl >/dev/null 2>&1; then
    die "systemd is required (this installer only supports systemd-based Linux distros)."
fi

# detect listen port from NANOKU_LISTEN (e.g. ":8080" or "0.0.0.0:8080")
LISTEN_PORT="${NANOKU_LISTEN##*:}"
[ -n "$LISTEN_PORT" ] || die "Could not parse NANOKU_LISTEN: ${NANOKU_LISTEN}"

# 80/443 are claimed by the Caddy container that nanoku manages on first start.
for port in "$LISTEN_PORT" 80 443; do
    if ss -tuln 2>/dev/null | awk '{print $5}' | grep -E "[:.]${port}\$" >/dev/null; then
        die "Port ${port} is already in use. Stop the conflicting service or set NANOKU_LISTEN to a free port."
    fi
done
ok "Ports free: ${LISTEN_PORT}, 80, 443"

# ---------- docker ----------
section "Checking Docker"

if ! command -v docker >/dev/null 2>&1; then
    warn "Docker not found. Installing via get.docker.com..."
    curl -fsSL https://get.docker.com | sh >/dev/null
fi

if ! command -v docker >/dev/null 2>&1; then
    die "Docker installation failed. Install Docker manually: https://docs.docker.com/engine/install/"
fi

DOCKER_BIN="$(command -v docker)"

if ! docker version >/dev/null 2>&1; then
    if systemctl is-active --quiet docker 2>/dev/null; then
        die "Docker is installed but not responding. Check: systemctl status docker"
    fi
    log "Starting docker service..."
    systemctl enable --now docker >/dev/null
    sleep 2
    docker version >/dev/null 2>&1 || die "Docker daemon is not responding."
fi

DOCKER_VERSION="$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo unknown)"
ok "Docker ${DOCKER_VERSION} ready"

# ensure docker compose plugin (v2 syntax used in compose.yml)
if ! docker compose version >/dev/null 2>&1; then
    die "Docker Compose plugin (v2) is required. Install: https://docs.docker.com/compose/install/"
fi
ok "Docker Compose plugin ready"

# ---------- version detection ----------
section "Resolving image"

# If user pinned a version, just use it. Otherwise, peek at GitHub for the
# latest tag so the install log prints a real version number (and so a failed
# version pin shows a useful error instead of a bare "manifest unknown").
if [ "$NANOKU_VERSION" = "latest" ]; then
    if command -v curl >/dev/null 2>&1; then
        if RELEASE_JSON="$(curl -fsSL --connect-timeout 5 "$GITHUB_API" 2>/dev/null)"; then
            TAG="$(echo "$RELEASE_JSON" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
            case "$TAG" in
                v*) NANOKU_VERSION="$TAG" ;;
            esac
        fi
    fi
fi

# Re-resolve image with the now-known version (if it was discovered)
if [ "$NANOKU_VERSION" != "latest" ] && [ "${NANOKU_IMAGE_REGISTRY:-dockerhub}" != "ghcr" ]; then
    case "${NANOKU_IMAGE:-}" in
        *":${NANOKU_VERSION}") ;;
        *) NANOKU_IMAGE="${NANOKU_IMAGE%:*}:${NANOKU_VERSION}" ;;
    esac
fi
ok "Image: ${NANOKU_IMAGE}"

# Verify the image is actually pullable before writing any files. This catches
# typos, private GHCR without `docker login ghcr.io`, and missing platforms.
if ! docker pull --quiet "$NANOKU_IMAGE" >/dev/null 2>&1; then
    if [ "${NANOKU_IMAGE_REGISTRY:-dockerhub}" = "ghcr" ]; then
        die "Could not pull ${NANOKU_IMAGE}. GHCR images are private by default — run \`docker login ghcr.io\` first, or set NANOKU_IMAGE_REGISTRY=dockerhub."
    fi
    die "Could not pull ${NANOKU_IMAGE}. Check your network, the version tag, or your Docker Hub rate limit."
fi
ok "Image pulled successfully"

# ---------- secrets ----------
section "Generating secrets"

generate_secret() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -base64 32 | tr -d '=+/' | head -c 48
    else
        tr -dc 'A-Za-z0-9' </dev/urandom | head -c 48
    fi
}

# Reuse existing secrets on re-install, generate fresh on first install.
ENV_FILE="${NANOKU_CONFIG_DIR}/.env"
NANOKU_ADMIN_PASSWORD="${NANOKU_ADMIN_PASSWORD:-}"
NANOKU_SECRET_KEY="${NANOKU_SECRET_KEY:-}"

if [ -f "$ENV_FILE" ]; then
    log "Existing .env found at ${ENV_FILE} — reusing credentials."
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a
    NANOKU_ADMIN_PASSWORD="${NANOKU_ADMIN_PASSWORD:-$(generate_secret)}"
    NANOKU_SECRET_KEY="${NANOKU_SECRET_KEY:-$(generate_secret)}"
else
    if [ -z "$NANOKU_ADMIN_PASSWORD" ]; then
        NANOKU_ADMIN_PASSWORD="$(generate_secret)"
        GENERATED_ADMIN_PASSWORD=1
    fi
    if [ -z "$NANOKU_SECRET_KEY" ]; then
        NANOKU_SECRET_KEY="$(generate_secret)"
        GENERATED_SECRET_KEY=1
    fi
fi
ok "Secrets ready (admin password length: ${#NANOKU_ADMIN_PASSWORD}, secret key length: ${#NANOKU_SECRET_KEY})"

# ---------- files ----------
section "Writing configuration"

mkdir -p "$NANOKU_DATA_DIR" "$NANOKU_CONFIG_DIR"
chmod 700 "$NANOKU_DATA_DIR" "$NANOKU_CONFIG_DIR"

cat > "$NANOKU_CONFIG_DIR/.env" <<EOF
# Generated by nanoku install.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ)
# After changing anything here: sudo systemctl restart nanoku
NANOKU_LISTEN=${NANOKU_LISTEN}
NANOKU_ADMIN_USER=${NANOKU_ADMIN_USER}
NANOKU_ADMIN_PASSWORD=${NANOKU_ADMIN_PASSWORD}
NANOKU_SECRET_KEY=${NANOKU_SECRET_KEY}
NANOKU_IMAGE=${NANOKU_IMAGE}
NANOKU_DATA_DIR=${NANOKU_DATA_DIR}
EOF
chmod 600 "$NANOKU_CONFIG_DIR/.env"
ok "Wrote ${ENV_FILE}"

cat > "$NANOKU_CONFIG_DIR/docker-compose.yml" <<EOF
# Managed by nanoku install.sh. Do not edit by hand — re-run install.sh instead.
services:
  nanoku:
    image: \${NANOKU_IMAGE}
    container_name: nanoku
    restart: unless-stopped
    env_file:
      - .env
    environment:
      NANOKU_LISTEN: \${NANOKU_LISTEN}
      NANOKU_ADMIN_USER: \${NANOKU_ADMIN_USER}
      NANOKU_ADMIN_PASSWORD: \${NANOKU_ADMIN_PASSWORD}
      NANOKU_SECRET_KEY: \${NANOKU_SECRET_KEY}
    ports:
      - "\${NANOKU_LISTEN}:\${NANOKU_LISTEN}"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - \${NANOKU_DATA_DIR}:/data
    healthcheck:
      test: ["CMD", "/usr/local/bin/nanoku", "--health-check"]
      interval: 10s
      timeout: 3s
      retries: 5
      start_period: 10s
    # nanoku's built-in Caddy container (\`nanoku-caddy\`) is created on first start.
    # It uses the docker network \`nanoku-net\`, which nanoku creates itself.
EOF
ok "Wrote ${NANOKU_CONFIG_DIR}/docker-compose.yml"

# Install the upgrade helper alongside the system service.
install -m 0755 /dev/null "${NANOKU_CONFIG_DIR}/upgrade.sh" 2>/dev/null || true
cat > "${NANOKU_CONFIG_DIR}/upgrade.sh" <<'EOF'
#!/usr/bin/env bash
# Pull the latest image and restart the nanoku container in place.
set -euo pipefail
ENV_FILE="/etc/nanoku/.env"
[ -f "$ENV_FILE" ] || { echo "No .env at $ENV_FILE" >&2; exit 1; }
set -a; . "$ENV_FILE"; set +a
echo "==> Pulling ${NANOKU_IMAGE}"
docker pull "${NANOKU_IMAGE}"
cd /etc/nanoku
docker compose up -d
echo "==> nanoku upgraded to $(docker inspect --format='{{.Config.Image}}' nanoku)"
EOF
chmod +x "${NANOKU_CONFIG_DIR}/upgrade.sh"
ok "Wrote ${NANOKU_CONFIG_DIR}/upgrade.sh"

# systemd unit — `docker compose up` keeps the stack alive across reboots and
# restarts on failure. We deliberately don't use `restart: always` semantics
# inside the unit itself; the compose file's `restart: unless-stopped` is the
# inner layer, systemd is the outer.
cat > /etc/systemd/system/nanoku.service <<EOF
[Unit]
Description=nanoku — self-hosted deployment hub
After=network-online.target docker.service
Requires=docker.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=${NANOKU_CONFIG_DIR}
EnvironmentFile=${NANOKU_CONFIG_DIR}/.env
ExecStart=${DOCKER_BIN} compose up -d --remove-orphans
ExecStop=${DOCKER_BIN} compose down
ExecReload=${DOCKER_BIN} compose pull && ${DOCKER_BIN} compose up -d
TimeoutStartSec=120

[Install]
WantedBy=multi-user.target
EOF
ok "Wrote /etc/systemd/system/nanoku.service"

# ---------- start ----------
section "Starting nanoku"

systemctl daemon-reload
systemctl enable nanoku.service >/dev/null
if ! systemctl restart nanoku.service 2>&1 | tee /tmp/nanoku-install.log; then
    warn "systemd start reported errors — check: journalctl -u nanoku -n 50"
fi

# ---------- wait for ready ----------
section "Waiting for nanoku to be ready"

ATTEMPT=0
MAX_ATTEMPT=30
HEALTH_URL="http://127.0.0.1:${LISTEN_PORT}/healthz"

while [ $ATTEMPT -lt $MAX_ATTEMPT ]; do
    if curl -fsS --max-time 2 "$HEALTH_URL" >/dev/null 2>&1; then
        ok "nanoku is responding at ${HEALTH_URL}"
        break
    fi
    ATTEMPT=$((ATTEMPT + 1))
    if [ $ATTEMPT -eq $MAX_ATTEMPT ]; then
        warn "nanoku did not become healthy within $((MAX_ATTEMPT * 2))s. Tail the log: journalctl -u nanoku -f"
    else
        printf "."
        sleep 2
    fi
done
echo

# ---------- done ----------
section "Installation complete"

# Best-effort public IP — useful for SSH-on-VPS installs.
PUBLIC_IP=""
for url in https://ifconfig.io https://icanhazip.com https://ipecho.net/plain; do
    if PUBLIC_IP="$(curl -4s --connect-timeout 3 "$url" 2>/dev/null)" && [ -n "$PUBLIC_IP" ]; then
        break
    fi
done

echo
echo -e "${BOLD}nanoku is running.${RESET}"
echo
echo -e "  Admin UI : ${GREEN}http://${PUBLIC_IP:-127.0.0.1}:${LISTEN_PORT}${RESET}"
echo -e "  Local UI : ${GREEN}http://127.0.0.1:${LISTEN_PORT}${RESET}"
echo -e "  Service  : ${DIM}systemctl status nanoku${RESET}"
echo -e "  Logs     : ${DIM}journalctl -u nanoku -f${RESET}"
echo -e "  Upgrade  : ${DIM}sudo ${NANOKU_CONFIG_DIR}/upgrade.sh${RESET}"
echo -e "  Uninstall: ${DIM}sudo bash uninstall.sh${RESET}"
echo
echo -e "${BOLD}First login${RESET}"
echo -e "  User     : ${NANOKU_ADMIN_USER}"

if [ "${GENERATED_ADMIN_PASSWORD:-}" = "1" ]; then
    echo -e "  Password : ${YELLOW}${NANOKU_ADMIN_PASSWORD}${RESET}  ${DIM}(generated — change it after first login)${RESET}"
    echo
    echo -e "${YELLOW}!${RESET} Save this password now — it will only be printed once."
else
    echo -e "  Password : ${DIM}the value of NANOKU_ADMIN_PASSWORD you provided (stored in ${ENV_FILE})${RESET}"
fi

if [ "${GENERATED_SECRET_KEY:-}" = "1" ]; then
    echo
    echo -e "${YELLOW}!${RESET} NANOKU_SECRET_KEY was generated and is stored in ${ENV_FILE}."
    echo -e "  Lost key = permanently lost encrypted secrets (registry passwords, trigger tokens, env vars)."
    echo -e "  Back up ${ENV_FILE} to a safe place."
fi

echo
echo -e "${BOLD}Next steps${RESET}"
echo -e "  1. Open the admin UI and change the admin password (${DIM}Settings${RESET})."
echo -e "  2. Set ${DIM}NANOKU_CADDY_AUTO_HTTPS=true${RESET} in ${ENV_FILE} and run ${DIM}sudo systemctl restart nanoku${RESET} to enable Let's Encrypt."
echo -e "  3. Add an app, enable the HTTP trigger, and wire it to your CI."
echo
