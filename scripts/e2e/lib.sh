# shellcheck shell=bash
# scripts/e2e/lib.sh — 公共函数 / 状态变量,被 setup.sh / teardown.sh / test-*.sh / run-all.sh source。
#
# 设计原则:
#   - 不 set -e。assert_* 失败时累加 E2E_FAIL_COUNT,不中断,这样单 case 失败
#     不影响 run-all.sh 跑完全部再汇总。
#   - 颜色靠 NO_COLOR / 是否 TTY 自动降级,日志重定向下没有乱码。
#   - workdir 固定 /tmp/nanoku-e2e,所有进程共享(setup / test / teardown)。
#     E2E 不会并行跑(慢,人为不会同时开两个),固定路径最稳。
#     想并行用 E2E_WORKDIR 环境变量覆盖。

# 防止重复 source
if [ -n "${E2E_LIB_SOURCED:-}" ]; then
  return 0
fi
E2E_LIB_SOURCED=1

# ---- 路径 / 命名空间 ----------------------------------------------------
E2E_WORKDIR="${E2E_WORKDIR:-/tmp/nanoku-e2e}"
E2E_COOKIE="$E2E_WORKDIR/cookies.txt"
E2E_ENV="$E2E_WORKDIR/.env"
E2E_SERVER_LOG="$E2E_WORKDIR/server.log"
E2E_SERVER_PID_FILE="$E2E_WORKDIR/server.pid"
E2E_DB="$E2E_WORKDIR/nanoku.db"
E2E_CADDYFILE="$E2E_WORKDIR/Caddyfile"
E2E_DEPLOY_LOG_DIR="$E2E_WORKDIR/deploy-logs"

# ---- 自动 source setup.sh 写的 .env --------------------------------------
# setup.sh 在 $E2E_ENV 里写了 E2E_* 变量(密码、secret key、端口等)。
# test-*.sh 是单独的 bash 进程,继承不到 setup.sh 的 env,需要从这里读。
if [ -f "$E2E_ENV" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$E2E_ENV"
  set +a
fi

E2E_BASE_URL="${E2E_BASE_URL:-http://127.0.0.1:18080}"
E2E_CADDY_HTTP_PORT="${E2E_CADDY_HTTP_PORT:-18080}"   # caddy :80 映射到 host 的端口
E2E_CADDY_ADMIN_PORT="${E2E_CADDY_ADMIN_PORT:-18019}"

# setup.sh 启动时设这些,test-*.sh 从 .env 读到。
# 这里给的是 fallback(单独跑 test-*.sh 不走 setup 的场景)。
E2E_ADMIN_USER="${E2E_ADMIN_USER:-admin}"
E2E_ADMIN_PASSWORD="${E2E_ADMIN_PASSWORD:-e2e-test-pass}"
# SECRET_KEY fallback:用 E2E_WORKDIR 派生,这样跨进程一致
E2E_SECRET_KEY="${E2E_SECRET_KEY:-e2e-default-secret-key-please-override-in-setup}"

# caddy 容器/网络用 e2e 命名,避免跟本地 dev 撞
E2E_CADDY_CONTAINER="nanoku-e2e-caddy"
E2E_CADDY_NETWORK="nanoku-e2e-net"
E2E_CADDY_VOLUME="nanoku-e2e-data"
E2E_CADDY_IMAGE="${E2E_CADDY_IMAGE:-caddy:2}"

# ---- 颜色 / 输出 --------------------------------------------------------
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

# ---- 计数 ---------------------------------------------------------------
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

# ---- HTTP 包装 ----------------------------------------------------------
# 协议:返回的 stdout 是 "body\n<status_code>" 两部分。sed '$d' 取 body,tail -1 取 code。

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

# 不带 cookie 发送(测未登录),但仍写 cookie jar(让 login 这种 endpoint 的
# Set-Cookie 能落到 jar 里,后续用 api() 自动带上)
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

# 带 Authorization: Bearer
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

# 带 X-Forwarded-For 模拟不同 client IP(绕开限流)
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

# 用一个基于测试文件名的稳定 IP,绕开 /api/login 限流(per-IP 5/min)。
# 每个 test-NN-*.sh 拿到一个独立 IP,跨 case 不互相串限流预算。
# 依赖 setup.sh 开了 NANOKU_TRUST_PROXY=true。
#
# 用法:my_ip=$(e2e_test_ip)  →  api_as_ip "$my_ip" POST /api/login ...
e2e_test_ip() {
  # 优先用 $E2E_TEST_NAME(由 run-all.sh 注入),否则 fallback 到 $0
  local name="${E2E_TEST_NAME:-$(basename "$0" .sh)}"
  # hash 成 4 个 hex 段
  local hash
  hash=$(printf '%s' "$name" | shasum -a 1 | cut -c1-8)
  # 172.16/16 私有段,取后两段(避免高位超 255)
  local hi=$((16#${hash:0:4}))
  local lo=$((16#${hash:4:4}))
  # clamp 到 0-255(以防万一)
  hi=$((hi % 256))
  lo=$((lo % 256))
  printf '172.16.%d.%d\n' "$hi" "$lo"
}

# 用 e2e_test_ip 登入(自动带 cookie),后续 api() 用 cookie jar
e2e_login() {
  : > "$E2E_COOKIE"
  api_as_ip "$(e2e_test_ip)" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')" >/dev/null
}
api_bearer_body() { api_bearer "$@" | sed '$d'; }
api_bearer_status() { api_bearer "$@" | tail -1; }

# CORS 预检
api_cors_preflight() {
  local origin=${1:-http://example.com}
  curl -sS -i -X OPTIONS \
    -H "Origin: $origin" \
    -H "Access-Control-Request-Method: POST" \
    -H "Access-Control-Request-Headers: content-type" \
    "$E2E_BASE_URL/api/login"
}

# 走 caddy 反代(不经过 nanoku),用 Host header 模拟域名
curl_proxy() {
  local domain=$1
  local path=${2:-/}
  curl -sS -i -H "Host: $domain" "http://127.0.0.1:${E2E_CADDY_HTTP_PORT}${path}"
}

# ---- 断言 ---------------------------------------------------------------
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

# 依赖 nanoku 管理的 caddy 容器(由 setup.sh 通过 nanoku 自身拉起)。
# 在 OrbStack 等环境里 caddy 容器可能被 SIGKILL,这种情况 test 主动 SKIP。
require_caddy() {
  if ! require_docker; then
    return 1
  fi
  if ! docker inspect "$E2E_CADDY_CONTAINER" --format '{{.State.Running}}' 2>/dev/null | grep -q true; then
    skip "caddy container not running (this env may not support it; see README)"
    return 1
  fi
  return 0
}

# 用完一个 app 容器立刻清掉
docker_cleanup_app() {
  local name=$1
  [ -z "$name" ] && return 0
  docker rm -f "nanoku-${name}" >/dev/null 2>&1 || true
}

# 拉取一个测试用镜像(若本地有就跳过)
docker_pull_if_missing() {
  local image=$1
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "  pulling $image ..."
    docker pull --quiet "$image" >/dev/null
  fi
}

# ---- 服务器管理 ---------------------------------------------------------
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
  # 兜底:go run 把编译产物放 $GOCACHE/build,杀 go run 父进程不一定带掉 nanoku 二进制子进程。
  # 用 E2E 独有的 Caddyfile 路径做精准匹配(任何持有这个路径的进程都是 E2E 的),
  # 不会误伤 dev 用的 nanoku。
  if [ -n "$E2E_CADDYFILE" ] && [ -e "$E2E_CADDYFILE" ]; then
    pkill -f "$E2E_CADDYFILE" 2>/dev/null || true
  fi
  # 兜底:任何还占着 E2E 端口的进程
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

# 主动 reconcile 一下,确保 caddy 容器被 ensure 起来(部分 orbstack 环境下
# nanoku 自启时的 caddy ensure 会因为一些时序问题被 SIGKILL,需要显式重试)
ensure_caddy_up() {
  local timeout=${1:-30}
  local start=$(date +%s)
  # 先用 login 拿 session
  : > "$E2E_COOKIE"
  api_unauthed POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')" >/dev/null
  while true; do
    # 调一次 reconcile,会触发 caddy ensure + reload
    api_unauthed POST /api/system/reconcile "" >/dev/null 2>&1
    if docker inspect "$E2E_CADDY_CONTAINER" --format '{{.State.Running}}' 2>/dev/null | grep -q true; then
      return 0
    fi
    local now=$(date +%s)
    if [ $((now - start)) -ge "$timeout" ]; then
      echo -e "${YELLOW}caddy container not up after ${timeout}s${NC}" >&2
      docker logs "$E2E_CADDY_CONTAINER" 2>&1 | head -20 >&2 || true
      return 1
    fi
    sleep 2
  done
}

# ---- 终态汇总 -----------------------------------------------------------
e2e_summary_and_exit() {
  echo
  echo -e "${BOLD}──────────────────────────────────${NC}"
  echo -e " ${GREEN}PASS:${NC} $E2E_PASS_COUNT  ${RED}FAIL:${NC} $E2E_FAIL_COUNT  ${YELLOW}SKIP:${NC} $E2E_SKIP_COUNT"
  if [ "$E2E_FAIL_COUNT" -gt 0 ]; then
    exit 1
  fi
  exit 0
}
