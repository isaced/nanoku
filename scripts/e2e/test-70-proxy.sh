#!/usr/bin/env bash
# test-70-proxy.sh — 完整链路:创建 app → 部署 → 绑 site → curl caddy 拿响应
#
# 这是最"端"的 E2E:模拟用户从外部 HTTP 访问 caddy,验证:
#   - app 真在跑(nginx 监听 80)
#   - site 配置写到 Caddyfile
#   - caddy 反代到 app 容器
#   - 外部 curl 拿到的响应是 nginx 的默认页(不是 caddy 自己的 404)
#
# 拓扑(test):
#   curl → 127.0.0.1:18080 (caddy 容器 :80 映射到 host 的 E2E_CADDY_HTTP_PORT)
#       → caddy 解析 Host → 找 site → 反代 nanoku-<app> → app 容器:8080
#
# 全部依赖 caddy + docker

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

if ! require_caddy; then
  exit 0
fi

# 拉一个小而稳的镜像。E2E_CADDY_HTTP_PORT 在 lib.sh 里定义(默认 18080)
# 注意:这会跟 nanoku admin 端口冲突!改成别的端口
E2E_CADDY_HTTP_PORT=${E2E_CADDY_HTTP_PORT:-18080}
# 实际我们走 caddy 容器的 :80(从 host 通过 18080)。但 18080 已经被 nanoku 占了。
# 用 18081(避免冲突) —— 但 caddy 容器是用 80:80 映射到 host 的 80,不会到 18081。
# 正确做法:让 caddy 把 :80 映射到 host 的非默认端口(需改 docker manager 的 EnsureCaddyContainer)。
# 临时方案:从 caddy 容器内部 curl(用 docker exec)。

docker_pull_if_missing "nginx:alpine"

e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-proxy-$SUFFIX"
DOMAIN="e2e-$SUFFIX.test"

# 1. 创建 app
section "1. create app $APP"
create_body=$(jq -nc --arg n "$APP" --arg i "nginx:alpine" '{name:$n, image:$i, port:80}')
create_resp=$(api POST /api/apps "$create_body")
app_id=$(echo "$create_resp" | sed '$d' | jq -r '.id')
if [ -z "$app_id" ] || [ "$app_id" = "null" ]; then
  fail "create app failed"
  e2e_summary_and_exit
fi
pass "app id=$app_id"

cleanup() {
    e2e_login
  if [ -n "$site_id" ]; then
    api_status DELETE "/api/sites/$site_id" >/dev/null 2>&1
  fi
  docker_cleanup_app "$APP"
  api_status DELETE "/api/apps/$app_id" >/dev/null 2>&1
}
site_id=""
trap cleanup EXIT

# 2. 部署
section "2. deploy"
deploy_status=$(api_status POST "/api/apps/$app_id/deployments" "")
assert_status "$deploy_status" 202 "deploy accepted"

timeout=60
start=$(date +%s)
while true; do
  hist_body=$(api_body GET "/api/apps/$app_id/deployments")
  s=$(echo "$hist_body" | jq -r '.[0].status // "unknown"')
  if [ "$s" = "success" ]; then break; fi
  if [ "$s" = "failed" ]; then fail "deploy failed: $(echo "$hist_body" | jq -r '.[0].error // ""')"; e2e_summary_and_exit; fi
  if [ $(( $(date +%s) - start )) -ge "$timeout" ]; then fail "deploy timeout"; e2e_summary_and_exit; fi
  sleep 2
done
pass "deployed"

# 3. 验证容器真在跑
running=$(docker inspect "nanoku-$APP" --format '{{.State.Running}}' 2>/dev/null)
[ "$running" = "true" ] && pass "container running" || fail "container not running"

# 4. 创建 site
section "3. create site for $DOMAIN → $APP"
create_body=$(jq -nc --arg d "$DOMAIN" --argjson aid "$app_id" '{domain:$d, appId:$aid, scheme:"http"}')
create_resp=$(api POST /api/sites "$create_body")
status=$(echo "$create_resp" | tail -1)
body=$(echo "$create_resp" | sed '$d')
assert_status "$status" 201 "create site"
site_id=$(echo "$body" | jq -r '.id')
assert_jq "$body" '.domain' "$DOMAIN" "site domain"
assert_jq "$body" '.appId' "$app_id" "site linked to app"
pass "site id=$site_id"

# 5. Caddyfile 里有这个 site
section "4. Caddyfile reflects site"
caddyfile=$(api_body GET /api/caddyfile)
assert_jq_exists "$caddyfile" "$DOMAIN" "caddyfile contains domain"

# 6. 真 curl caddy 拿响应
# 走 caddy 容器的 :80,用 Host header 模拟域名
section "5. curl caddy with Host: $DOMAIN"
sleep 2  # 给 caddy reload 一点时间

# 在 caddy 容器内部 curl(因为 host:80 可能被占)
# caddy 容器名 = $E2E_CADDY_CONTAINER
caddy_container="$E2E_CADDY_CONTAINER"
if docker exec "$caddy_container" test -f /usr/bin/curl 2>/dev/null; then
  caddy_curl="docker exec $caddy_container curl -sS -i -H 'Host: $DOMAIN' http://127.0.0.1/"
else
  caddy_curl="docker exec $caddy_container wget -qO- --header='Host: $DOMAIN' http://127.0.0.1/"
fi

response=$(eval "$caddy_curl" 2>&1)
status_line=$(echo "$response" | head -1 | tr -d '\r')

# 期望:200(nginx 响应)
echo "  caddy response: $status_line"
if echo "$status_line" | grep -q "200"; then
  pass "caddy returned 200"
elif echo "$status_line" | grep -qE "30[127]"; then
  pass "caddy returned $status_line (redirect to https — expected in prod)"
else
  fail "caddy returned: $status_line"
  echo "  body: $(echo "$response" | head -10)"
fi

# body 包含 nginx 标识(不是 caddy 的 404 之类)
if echo "$response" | grep -qiE "nginx|welcome"; then
  pass "response body contains nginx marker"
else
  fail "response body doesn't look like nginx"
  echo "  first 5 lines:"
  echo "$response" | head -5 | sed 's/^/    /'
fi

# 7. 删 site
section "6. cleanup site"
del_status=$(api_status DELETE "/api/sites/$site_id")
assert_status "$del_status" 204 "delete site"
site_id=""

case_done
e2e_summary_and_exit
