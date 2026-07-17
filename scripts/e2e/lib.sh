# shellcheck shell=bash
# scripts/e2e/lib.sh - shared functions / state variables, sourced by setup.sh / teardown.sh / test-*.sh / run-all.sh.
#
# Design principles:
#   - Do NOT set -e. assert_* failures increment E2E_FAIL_COUNT instead of aborting, so a single
#     failing case does not stop run-all.sh from running all cases and then summarizing.
#   - Colors auto-downgrade based on NO_COLOR / TTY detection, so redirected logs stay clean.
#   - workdir is fixed at /tmp/nanoku-e2e, shared by all processes (setup / test / teardown).
#     E2E does not run in parallel (it's slow, and nobody launches two at once), so a fixed path
#     is safest. Override with the E2E_WORKDIR env var to run in parallel.

# Guard against duplicate sourcing
if [ -n "${E2E_LIB_SOURCED:-}" ]; then
  return 0
fi
E2E_LIB_SOURCED=1

# ---- Paths / namespace -------------------------------------------------
E2E_WORKDIR="${E2E_WORKDIR:-/tmp/nanoku-e2e}"
E2E_COOKIE="$E2E_WORKDIR/cookies.txt"
E2E_ENV="$E2E_WORKDIR/.env"
E2E_SERVER_LOG="$E2E_WORKDIR/server.log"
E2E_SERVER_PID_FILE="$E2E_WORKDIR/server.pid"
E2E_DB="$E2E_WORKDIR/nanoku.db"
# Caddyfile lives in its own subdir because the caddy container bind-mounts
# the directory (not the file) into /etc/caddy — a directory mount
# re-resolves on every access, so atomic renames propagate to the
# container; a file mount doesn't on Linux. See internal/docker/docker.go
# EnsureCaddyContainer.
E2E_CADDYFILE="$E2E_WORKDIR/caddy-dir/Caddyfile"
E2E_DEPLOY_LOG_DIR="$E2E_WORKDIR/deploy-logs"

# ---- Auto-source the .env written by setup.sh --------------------------
# setup.sh writes E2E_* variables (passwords, secret key, ports, etc.) into $E2E_ENV.
# test-*.sh runs as a separate bash process and does not inherit setup.sh's env, so it reads them here.
if [ -f "$E2E_ENV" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$E2E_ENV"
  set +a
fi

E2E_BASE_URL="${E2E_BASE_URL:-http://127.0.0.1:18080}"
E2E_CADDY_HTTP_PORT="${E2E_CADDY_HTTP_PORT:-18080}"   # host port that caddy :80 maps to
E2E_CADDY_ADMIN_PORT="${E2E_CADDY_ADMIN_PORT:-18019}"

# These are set by setup.sh at startup; test-*.sh reads them from .env.
# Values below are fallbacks (for the case of running test-*.sh standalone, without setup).
E2E_ADMIN_USER="${E2E_ADMIN_USER:-admin}"
E2E_ADMIN_PASSWORD="${E2E_ADMIN_PASSWORD:-e2e-test-pass}"
# SECRET_KEY fallback: derived from E2E_WORKDIR so it stays consistent across processes
E2E_SECRET_KEY="${E2E_SECRET_KEY:-e2e-default-secret-key-please-override-in-setup}"

# caddy container/network use e2e names to avoid colliding with local dev
E2E_CADDY_CONTAINER="nanoku-e2e-caddy"
E2E_CADDY_NETWORK="nanoku-e2e-net"
E2E_CADDY_VOLUME="nanoku-e2e-data"
E2E_CADDY_IMAGE="${E2E_CADDY_IMAGE:-caddy:2}"

# ---- Colors / output ---------------------------------------------------
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  BLUE='\033[0;34m'
  BOLD='\033[1m'
  NC='\033[0m'
else
  RED='' GREEN='' YELLOW='' BLUE='' BOLD='' NC=''
fi

# ---- Counters ----------------------------------------------------------
E2E_PASS_COUNT=0
E2E_FAIL_COUNT=0
E2E_SKIP_COUNT=0
E2E_CASE_START=0

pass() {
  echo -e "${GREEN}✓${NC} $1"
  E2E_PASS_COUNT=$((E2E_PASS_COUNT + 1))
}
fail() {
  echo -e "${RED}✗${NC} $1"
  E2E_FAIL_COUNT=$((E2E_FAIL_COUNT + 1))
}
skip() {
  echo -e "${YELLOW}⊘${NC} $1"
  E2E_SKIP_COUNT=$((E2E_SKIP_COUNT + 1))
}
section() {
  local title=$1
  echo -e "\n${BOLD}${BLUE}── ${title} ──${NC}"
}
case_start() {
  E2E_CASE_START=$(date +%s)
}
case_done() {
  local elapsed=$(($(date +%s) - E2E_CASE_START))
  echo -e "  ${BOLD}(${elapsed}s)${NC}"
}

# ---- HTTP wrappers -----------------------------------------------------
# Convention: stdout is "body\n<status_code>" (two parts). sed '$d' extracts body, tail -1 extracts the code.

api() {
  local method=$1; shift
  local path=$1; shift
  local data=${1:-}
  if [ -n "$data" ]; then
    curl -sS -b "$E2E_COOKIE" -c "$E2E_COOKIE" \
      -X "$method" \
      -H "Content-Type: application/json" \
      -d "$data" \
      -w '\n%{http_code}' \
      "$E2E_BASE_URL$path"
  else
    curl -sS -b "$E2E_COOKIE" -c "$E2E_COOKIE" \
      -X "$method" \
      -w '\n%{http_code}' \
      "$E2E_BASE_URL$path"
  fi
}
api_body() { api "$@" | sed '$d'; }
api_status() { api "$@" | tail -1; }

# Send without a cookie (for testing unauthenticated requests), but still write to the cookie jar
# (so Set-Cookie from endpoints like login lands in the jar and is auto-attached by later api() calls)
api_unauthed() {
  local method=$1; shift
  local path=$1; shift
  local data=${1:-}
  if [ -n "$data" ]; then
    curl -sS -c "$E2E_COOKIE" \
      -X "$method" \
      -H "Content-Type: application/json" \
      -d "$data" \
      -w '\n%{http_code}' \
      "$E2E_BASE_URL$path"
  else
    curl -sS -c "$E2E_COOKIE" \
      -X "$method" \
      -w '\n%{http_code}' \
      "$E2E_BASE_URL$path"
  fi
}
api_unauthed_body() { api_unauthed "$@" | sed '$d'; }
api_unauthed_status() { api_unauthed "$@" | tail -1; }

# With Authorization: Bearer
api_bearer() {
  local token=$1; shift
  local method=$1; shift
  local path=$1; shift
  local data=${1:-}
  if [ -n "$data" ]; then
    curl -sS \
      -X "$method" \
      -H "Content-Type: application/json" \
      -H "Authorization: Bearer $token" \
      -d "$data" \
      -w '\n%{http_code}' \
      "$E2E_BASE_URL$path"
  else
    curl -sS \
      -X "$method" \
      -H "Authorization: Bearer $token" \
      -w '\n%{http_code}' \
      "$E2E_BASE_URL$path"
  fi
}

# Attach X-Forwarded-For to simulate different client IPs (bypass rate limiting)
api_as_ip() {
  local ip=$1; shift
  local method=$1; shift
  local path=$1; shift
  local data=${1:-}
  if [ -n "$data" ]; then
    curl -sS -c "$E2E_COOKIE" \
      -X "$method" \
      -H "Content-Type: application/json" \
      -H "X-Forwarded-For: $ip" \
      -d "$data" \
      -w '\n%{http_code}' \
      "$E2E_BASE_URL$path"
  else
    curl -sS -c "$E2E_COOKIE" \
      -X "$method" \
      -H "X-Forwarded-For: $ip" \
      -w '\n%{http_code}' \
      "$E2E_BASE_URL$path"
  fi
}

# Use a stable IP derived from the test file name to bypass the /api/login rate limit (per-IP 5/min).
# Each test-NN-*.sh gets its own IP, so cases do not share/consume each other's rate-limit budget.
# Requires setup.sh to have set NANOKU_TRUST_PROXY=true.
#
# Usage: my_ip=$(e2e_test_ip)  ->  api_as_ip "$my_ip" POST /api/login ...
e2e_test_ip() {
  # Prefer $E2E_TEST_NAME (injected by run-all.sh), otherwise fall back to $0
  local name="${E2E_TEST_NAME:-$(basename "$0" .sh)}"
  # Hash into 4 hex segments
  local hash
  hash=$(printf '%s' "$name" | shasum -a 1 | cut -c1-8)
  # 172.16/16 private range, take the last two octets (avoids high bytes exceeding 255)
  local hi=$((16#${hash:0:4}))
  local lo=$((16#${hash:4:4}))
  # Clamp to 0-255 (just in case)
  hi=$((hi % 256))
  lo=$((lo % 256))
  printf '172.16.%d.%d\n' "$hi" "$lo"
}

# Log in using e2e_test_ip (auto-attaches cookie); subsequent api() calls reuse the cookie jar
e2e_login() {
  : > "$E2E_COOKIE"
  api_as_ip "$(e2e_test_ip)" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')" >/dev/null
}
api_bearer_body() { api_bearer "$@" | sed '$d'; }
api_bearer_status() { api_bearer "$@" | tail -1; }

# CORS preflight
api_cors_preflight() {
  local origin=${1:-http://example.com}
  curl -sS -i -X OPTIONS \
    -H "Origin: $origin" \
    -H "Access-Control-Request-Method: POST" \
    -H "Access-Control-Request-Headers: content-type" \
    "$E2E_BASE_URL/api/login"
}

# Go through the caddy reverse proxy (bypassing nanoku), using a Host header to simulate a domain
curl_proxy() {
  local domain=$1
  local path=${2:-/}
  curl -sS -i -H "Host: $domain" "http://127.0.0.1:${E2E_CADDY_HTTP_PORT}${path}"
}

# ---- Assertions --------------------------------------------------------
assert_status() {
  local actual=$1 expected=$2 label=${3:-status}
  if [ "$actual" = "$expected" ]; then
    pass "HTTP $actual  $label"
  else
    fail "HTTP expected $expected, got $actual  $label"
    return 1
  fi
}

assert_jq() {
  local body=$1 expr=$2 expected=$3 label=${4:-$expr}
  local actual
  actual=$(printf '%s' "$body" | jq -r "$expr" 2>/dev/null) || actual="<jq-error>"
  if [ "$actual" = "$expected" ]; then
    pass "$label: = $expected"
  else
    fail "$label: expected '$expected', got '$actual'"
    echo -e "  ${BOLD}body:${NC} $(printf '%s' "$body" | head -c 400)"
    return 1
  fi
}

assert_jq_exists() {
  local body=$1 expr=$2 label=${3:-$expr}
  if printf '%s' "$body" | jq -e "$expr" >/dev/null 2>&1; then
    pass "$label: present"
  else
    fail "$label: missing"
    echo -e "  ${BOLD}body:${NC} $(printf '%s' "$body" | head -c 400)"
    return 1
  fi
}

assert_contains() {
  local haystack=$1 needle=$2 label=${3:-contains}
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
    pass "$label: contains '$needle'"
  else
    fail "$label: does not contain '$needle'"
    echo -e "  ${BOLD}got:${NC} $(printf '%s' "$haystack" | head -c 400)"
    return 1
  fi
}

assert_eq() {
  local actual=$1 expected=$2 label=${3:-eq}
  if [ "$actual" = "$expected" ]; then
    pass "$label: $actual"
  else
    fail "$label: expected '$expected', got '$actual'"
    return 1
  fi
}

# ---- Docker helpers -----------------------------------------------------
require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    skip "docker not installed"
    return 1
  fi
  if ! docker info >/dev/null 2>&1; then
    skip "docker daemon not reachable"
    return 1
  fi
  return 0
}

# Clean up an app container immediately after use
docker_cleanup_app() {
  local name=$1
  [ -z "$name" ] && return 0
  docker rm -f "nanoku-${name}" >/dev/null 2>&1 || true
}

# Pull a test image (skip if already present locally)
docker_pull_if_missing() {
  local image=$1
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "  pulling $image ..."
    docker pull --quiet "$image" >/dev/null
  fi
}

# ---- Server management -------------------------------------------------
wait_for_server() {
  local timeout=${1:-120}
  local start=$(date +%s)
  while true; do
    if curl -sS -f "$E2E_BASE_URL/healthz" >/dev/null 2>&1; then
      return 0
    fi
    local now=$(date +%s)
    if [ $((now - start)) -ge "$timeout" ]; then
      echo -e "${RED}server did not become ready within ${timeout}s${NC}"
      echo "--- last 30 lines of server log ---"
      tail -30 "$E2E_SERVER_LOG" 2>/dev/null || echo "(no log)"
      return 1
    fi
    sleep 1
  done
}

stop_server() {
  if [ -f "$E2E_SERVER_PID_FILE" ]; then
    local pid
    pid=$(cat "$E2E_SERVER_PID_FILE")
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      for _ in 1 2 3 4 5; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 1
      done
      kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$E2E_SERVER_PID_FILE"
  fi
  # Fallback: `go run` puts the build artifact in $GOCACHE/build; killing the `go run` parent does
  # not always take down the nanoku binary child process. Use the E2E-specific Caddyfile path for a
  # precise match (any process holding this path is an E2E process), so we don't kill dev nanoku.
  if [ -n "$E2E_CADDYFILE" ] && [ -e "$E2E_CADDYFILE" ]; then
    pkill -f "$E2E_CADDYFILE" 2>/dev/null || true
  fi
  # Fallback: any process still holding an E2E port
  local port_pids
  port_pids=$(lsof -ti tcp:18080 2>/dev/null || true)
  if [ -n "$port_pids" ]; then
    echo "killing leftover listeners on :18080: $port_pids"
    echo "$port_pids" | xargs -r kill 2>/dev/null || true
    sleep 1
    port_pids=$(lsof -ti tcp:18080 2>/dev/null || true)
    [ -n "$port_pids" ] && echo "$port_pids" | xargs -r kill -9 2>/dev/null || true
  fi
}

wait_for_caddy() {
  local timeout=${1:-30}
  local start=$(date +%s)
  while true; do
    if docker inspect "$E2E_CADDY_CONTAINER" --format '{{.State.Running}}' 2>/dev/null | grep -q true; then
      return 0
    fi
    local now=$(date +%s)
    if [ $((now - start)) -ge "$timeout" ]; then
      return 1
    fi
    sleep 1
  done
}

# caddy_status prints the container's State.Status (exitCode is useful when not running;
# under OrbStack a SIGKILL'd container shows exited exitCode=137). Empty string if no container.
caddy_status() {
  docker inspect "$E2E_CADDY_CONTAINER" --format '{{.State.Status}}' 2>/dev/null || true
}

# start_server launches nanoku (`go run .`) in the background from the repo root and writes the PID
# to the pid file. Shared by setup.sh's first launch and ensure_caddy_up's restart-retry path.
# Preconditions: the caller has already sourced $E2E_ENV (NANOKU_* must enter the environment), and
# has truncated/cleared $E2E_SERVER_LOG to the desired state (this function appends with >>, keeping
# pre-restart boot logs so it's easy to see later which boot attempt failed).
start_server() {
  local repo_root
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  (
    cd "$repo_root"
    # Do not use nohup: historically, nohup under orbstack occasionally triggered the caddy container being SIGKILL'd.
    go run . >> "$E2E_SERVER_LOG" 2>&1 &
    echo $! > "$E2E_SERVER_PID_FILE"
  )
}

# ensure_caddy_up waits for the caddy container created by EnsureCaddyContainer during nanoku boot
# to enter the running state.
#
# Key background: HTTP POST /api/system/reconcile (ApplyReconcile) does NOT create/start the caddy
# container -- it only clears the stale current_container edge. The only path that creates caddy is
# dm.EnsureCaddyContainer during main.go boot. So this function no longer calls
# /api/system/reconcile (an earlier version did, and unauthed -> 401, so it never worked).
#
# Known issue: on macOS + OrbStack the caddy container is occasionally SIGKILL'd ~1s after boot
# (State.Status=exited, ExitCode=137). The only reliable recovery is to restart nanoku so boot
# re-runs EnsureCaddyContainer. So within the timeout window this function will restart nanoku up
# to E2E_CADDY_RESTART_ATTEMPTS times.
ensure_caddy_up() {
  local timeout=${1:-60}
  local start=$(date +%s)
  local max_restarts=${E2E_CADDY_RESTART_ATTEMPTS:-3}
  local restarts=0
  local last_status=""
  while true; do
    if docker inspect "$E2E_CADDY_CONTAINER" --format '{{.State.Running}}' 2>/dev/null | grep -q true; then
      return 0
    fi
    local now=$(date +%s)
    if [ $((now - start)) -ge "$timeout" ]; then
      echo -e "${YELLOW}caddy container not up after ${timeout}s (last status: ${last_status:-none})${NC}" >&2
      return 1
    fi
    # Container is not running. Check its current state:
    #  - empty (no container): boot hasn't reached EnsureCaddyContainer yet, keep waiting
    #  - created/exited/paused/restarting: possibly OrbStack SIGKILL, or boot's start hasn't
    #    finished yet. Give it some time, but if it's stuck, restart nanoku.
    last_status=$(caddy_status)
    if [ -n "$last_status" ] && [ "$last_status" != "running" ]; then
      # Container exists but isn't running. Wait briefly in case it's still starting.
      sleep 2
      local s2
      s2=$(caddy_status)
      if [ "$s2" != "running" ]; then
        # Still not up. If restart budget remains, restart nanoku to re-trigger boot ensure.
        if [ "$restarts" -lt "$max_restarts" ]; then
          restarts=$((restarts + 1))
          echo -e "  ${YELLOW}caddy stuck ($s2), restarting nanoku (attempt $restarts/$max_restarts)${NC}" >&2
          # Remove the broken container so boot recreates it (EnsureCaddyContainer will attempt
          # ContainerStart on an exited container, but a SIGKILL'd container is often killed again
          # on start; recreating a clean one is more reliable)
          docker rm -f "$E2E_CADDY_CONTAINER" >/dev/null 2>&1 || true
          stop_server
          start_server
          if ! wait_for_server 120; then
            echo -e "  ${RED}nanoku did not come back up after restart${NC}" >&2
            return 1
          fi
          # boot has re-run EnsureCaddyContainer; loop continues waiting for it to be running
        fi
      fi
    fi
    sleep 1
  done
}

# ---- Final summary -----------------------------------------------------
e2e_summary_and_exit() {
  echo
  echo -e "${BOLD}──────────────────────────────────${NC}"
  echo -e " ${GREEN}PASS:${NC} $E2E_PASS_COUNT  ${RED}FAIL:${NC} $E2E_FAIL_COUNT  ${YELLOW}SKIP:${NC} $E2E_SKIP_COUNT"
  if [ "$E2E_FAIL_COUNT" -gt 0 ]; then
    exit 1
  fi
  exit 0
}
