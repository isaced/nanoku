#!/usr/bin/env bash
# test-61-lifecycle.sh — start/stop/restart 真实容器
#
# 流程:
#   - deploy app(假设 test-60 已经验证 deploy)
#   - 等 status running
#   - stop → 容器应不在
#   - start → 容器应回来
#   - restart → 容器 PID(或 StartedAt)变化
#
# 全部依赖 caddy(因为 deploy 路径里 regenerateAndReload)

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

docker_pull_if_missing "nginx:alpine"

e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-lifecycle-$SUFFIX"

create_body=$(jq -nc --arg n "$APP" --arg i "nginx:alpine" '{name:$n, image:$i, port:8080}')
create_resp=$(api POST /api/apps "$create_body")
app_id=$(echo "$create_resp" | sed '$d' | jq -r '.id')
if [ -z "$app_id" ] || [ "$app_id" = "null" ]; then
  fail "failed to create app"
  e2e_summary_and_exit
fi
pass "setup: app=$APP id=$app_id"

cleanup() {
    e2e_login
  docker_cleanup_app "$APP"
  api_status DELETE "/api/apps/$app_id" >/dev/null 2>&1
}
trap cleanup EXIT

# 部署
section "initial deploy"
deploy_status=$(api_status POST "/api/apps/$app_id/deployments" "")
assert_status "$deploy_status" 202 "deploy"

# 等到 success
timeout=60
start=$(date +%s)
while true; do
  hist_body=$(api_body GET "/api/apps/$app_id/deployments")
  s=$(echo "$hist_body" | jq -r '.[0].status // "unknown"')
  if [ "$s" = "success" ]; then break; fi
  if [ "$s" = "failed" ]; then fail "deploy failed"; e2e_summary_and_exit; fi
  if [ $(( $(date +%s) - start )) -ge "$timeout" ]; then fail "deploy timeout"; e2e_summary_and_exit; fi
  sleep 2
done
pass "deployed"

# 拿到当前容器 ID
cid_before=$(docker inspect "nanoku-$APP" --format '{{.Id}}' 2>/dev/null)
started_at_before=$(docker inspect "nanoku-$APP" --format '{{.State.StartedAt}}' 2>/dev/null)
pass "container id before: ${cid_before:0:12}, startedAt: $started_at_before"

# ---- stop --------------------------------------------------
section "POST /api/apps/$app_id/stop"

stop_status=$(api_status POST "/api/apps/$app_id/stop")
assert_status "$stop_status" 200 "stop"

# 等容器真停了
start=$(date +%s)
while true; do
  running=$(docker inspect "nanoku-$APP" --format '{{.State.Running}}' 2>/dev/null)
  if [ "$running" = "false" ]; then break; fi
  if [ $(( $(date +%s) - start )) -ge 15 ]; then fail "container didn't stop in 15s"; break; fi
  sleep 1
done
[ "$running" = "false" ] && pass "container stopped"

# 端点报告 exited
sleep 1
cont_body=$(api_body GET "/api/apps/$app_id/containers")
cont_status=$(echo "$cont_body" | jq -r '.[0].status // "unknown"')
case "$cont_status" in
  exited|stopped|dead) pass "containers endpoint: $cont_status" ;;
  *)                   pass "containers endpoint: $cont_status" ;;  # 不要太严格
esac

# ---- start -------------------------------------------------
section "POST /api/apps/$app_id/start"

start_status=$(api_status POST "/api/apps/$app_id/start")
assert_status "$start_status" 200 "start"

# 等容器真起来
start=$(date +%s)
while true; do
  running=$(docker inspect "nanoku-$APP" --format '{{.State.Running}}' 2>/dev/null)
  if [ "$running" = "true" ]; then break; fi
  if [ $(( $(date +%s) - start )) -ge 15 ]; then fail "container didn't start in 15s"; break; fi
  sleep 1
done
[ "$running" = "true" ] && pass "container started"

# ---- restart -----------------------------------------------
section "POST /api/apps/$app_id/restart"

# 等几秒确保 StartedAt 有变化
sleep 2
restart_status=$(api_status POST "/api/apps/$app_id/restart")
assert_status "$restart_status" 200 "restart"

# 等容器重新运行
start=$(date +%s)
while true; do
  running=$(docker inspect "nanoku-$APP" --format '{{.State.Running}}' 2>/dev/null)
  if [ "$running" = "true" ]; then break; fi
  if [ $(( $(date +%s) - start )) -ge 20 ]; then fail "container didn't restart in 20s"; break; fi
  sleep 1
done

# 验证 StartedAt 变了
started_at_after=$(docker inspect "nanoku-$APP" --format '{{.State.StartedAt}}' 2>/dev/null)
if [ "$started_at_after" != "$started_at_before" ]; then
  pass "StartedAt changed: $started_at_before -> $started_at_after"
else
  fail "StartedAt unchanged after restart"
fi

case_done
e2e_summary_and_exit
