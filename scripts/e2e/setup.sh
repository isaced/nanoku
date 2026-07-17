#!/usr/bin/env bash
# scripts/e2e/setup.sh — 准备临时 .env / DB 目录,后台启 nanoku,等到 /healthz 通。
#
# 用法: ./setup.sh
# 退出: 0 成功,非 0 失败
#
# 副作用:
#   - $E2E_WORKDIR (默认 /tmp/nanoku-e2e.XXXXXX) 写一堆临时文件
#   - 后台启 `go run .` 监听 :18080,PID 写在 $E2E_SERVER_PID_FILE
#   - 让 nanoku 走真 docker(dm 初始化),这样 caddy 容器和 app 部署能跑
#
# 这里不主动调 caddy 容器启停,nanoku 自己 ensure。但 caddy 容器 / 网络 / volume
# 全部用 nanoku-e2e-* 命名空间,跟本地 dev 隔离。

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# ---- 前置检查 -----------------------------------------------------------
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
  echo -e "${RED}docker not available; cannot run E2E (test-00 等基础 case 不需要,但 test-60/61/70 需要)${NC}" >&2
  echo "  → 在 GitHub Actions ubuntu-latest runner 上跑是 OK 的,本地缺 docker 时请装" >&2
  exit 1
fi

# 提前拉 caddy:2 镜像,docker hub 经常 EOF,setup 里 retry 几次比 nanoku 内部拉稳。
# 这步放在 mkdir 之前,失败不会留半个 workdir。
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

# ---- 准备目录 -----------------------------------------------------------
mkdir -p "$E2E_WORKDIR" "$E2E_DEPLOY_LOG_DIR"
# 注意:不 touch Caddyfile。docker.EnsureCaddyContainer 自己会调 ensureCaddyfileExists
# 写一个占位(空文件会让 caddy 报 "EOF" 启动失败)。

# ---- 写临时 .env --------------------------------------------------------
# NANOKU_LISTEN=:18080         admin API 端口
# NANOKU_CADDY_HTTP_PORT=18080 caddy :80 映射到 host 的端口(我们 E2E 里直接 curl 这个)
# 用 e2e 命名空间,跟本地 dev 完全隔离
cat > "$E2E_ENV" <<EOF
# 给 nanoku 用的(NANOKU_*)
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
# 让 clientIP 信任 X-Forwarded-For。E2E 用它给每个 case 分配"独立 IP",绕开
# /api/login 的 5/min 限流(per-IP)。生产里只在反向代理后面才该开。
NANOKU_TRUST_PROXY=true

# 给 E2E 测试脚本用的(E2E_*),lib.sh 会 source 这个文件
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

# ---- 启 nanoku ----------------------------------------------------------
# 在仓库根目录跑,因为有 go.mod、internal/api/ui.go (go:embed dist) 等
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

# 把 caddy 容器的 :80 端口 publish 到 host 的 E2E_CADDY_HTTP_PORT,这样 E2E 可以直接 curl
# 这部分通过临时 patch 不优雅(要改 docker manager),所以我们直接在 nanoku 启动后
# 用 docker run 命令确保端口发布?更简单:让 caddy 容器用 host 网络,这样直接 :80 就能访问
# 但 host 网络需要 --network=host,在 Docker 里需要特权。
#
# 折中:让 nanoku 正常起 caddy(走 nanoku-net 网络),然后我们 E2E 里:
#   1. 用 `docker exec` 进 caddy 容器去 curl
#   2. 或者把 caddy 容器端口映射出来 —— 需要 patch docker.EnsureCaddyContainer
#
# 选了 (1) 路线:proxy 测试走 `docker exec caddy curl ...`,避免改源码。
# 这样 nanoku 完全不感知我们在测它,所有外部访问都从 caddy 容器内发起。

echo "starting nanoku (log: $E2E_SERVER_LOG) ..."
set -a
# shellcheck disable=SC1090
source "$E2E_ENV"
set +a

# nohup 让它脱离 shell;重定向 stdio 避免阻塞
# (注意:orbstack 环境下,nohup 启动的 nanoku 偶发会触发 caddy 容器创建后
# ~1.2s 被 SIGKILL(暂时查不到原因,跟 nohup/parent exit 有关)。先不用 nohup。)
go run . > "$E2E_SERVER_LOG" 2>&1 &
SERVER_BG_PID=$!
echo "$SERVER_BG_PID" > "$E2E_SERVER_PID_FILE"

# ---- 等就绪 -------------------------------------------------------------
echo "waiting for /healthz ..."
if ! wait_for_server 120; then
  echo -e "${RED}server failed to start${NC}" >&2
  stop_server
  exit 1
fi
echo -e "${GREEN}server up${NC}  pid=$(cat "$E2E_SERVER_PID_FILE")  url=$E2E_BASE_URL"

# ---- 清理任何残留的旧 E2E 容器(上次没清干净) -------------------------
# 拿 label 删,避免误伤
local_remaining=$(docker ps -aq --filter "label=nanoku-e2e=true" 2>/dev/null || true)
if [ -n "$local_remaining" ]; then
  echo "removing leftover e2e containers: $local_remaining"
  docker rm -f $local_remaining >/dev/null 2>&1 || true
fi
# caddy 容器也清掉,让 nanoku 重 ensure
docker rm -f "$E2E_CADDY_CONTAINER" >/dev/null 2>&1 || true
docker network rm "$E2E_CADDY_NETWORK" >/dev/null 2>&1 || true

# 触发一次 ensure(直接重启 server 即可,或者调 reconcile 接口)。
# 简单做法:让 test-00 调一次 reconcile 把它 ensure 起来;或者我们在 setup 末尾重启一次 server。
# 但重启会让 caddy 拉镜像,慢。算了,test-60/70 调一次 /api/system/reconcile POST 即可触发 ensure。

echo
# 主动 reconcile 触发 caddy 容器起来。OrbStack 等环境下 nanoku 启动时 caddy
# ensure 会被 SIGKILL(查不到明确原因),用 reconcile 重试直到起来。
echo "ensuring caddy container is up (may retry) ..."
if ensure_caddy_up 30; then
  echo -e "  ${GREEN}caddy up${NC}"
else
  echo -e "  ${YELLOW}WARN: caddy not up after retries. test-60/61/70 (docker + proxy) will fail.${NC}"
fi

echo
echo "E2E environment ready"
echo "  workdir:    $E2E_WORKDIR"
echo "  base url:   $E2E_BASE_URL"
echo "  admin:      $E2E_ADMIN_USER / $E2E_ADMIN_PASSWORD"
echo "  server log: $E2E_SERVER_LOG"
