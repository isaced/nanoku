#!/usr/bin/env bash
# test-50-trigger.sh — POST /api/apps/{name}/trigger 鉴权 + 异步部署
#
# 覆盖:
#   - 缺 Authorization: 401
#   - 错 token: 401
#   - 对 token + 合法 tag: 202 + deployId
#   - 无 trigger 配置: 404
#   - app 不存在: 404
#   - 同一 app 二次 trigger: 命中 deploy lock → 202 + accepted:false
#   - 无效 tag: 400
# 注意:不验证 deploy 实际拉镜像成功(那是 test-60 的事)

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-trig-$SUFFIX"

# 准备:app + rotate token
create_body=$(jq -nc --arg n "$APP" '{name:$n, image:"nginx:alpine", port:8080}')
create_resp=$(api POST /api/apps "$create_body")
app_id=$(echo "$create_resp" | sed '$d' | jq -r '.id')
if [ -z "$app_id" ] || [ "$app_id" = "null" ]; then
  fail "failed to create test app"
  e2e_summary_and_exit
fi

rotate_resp=$(api POST "/api/apps/$app_id/rotate-trigger-token")
token=$(echo "$rotate_resp" | sed '$d' | jq -r '.triggerToken')
if [ -z "$token" ] || [ "$token" = "null" ]; then
  fail "no token from rotate"
  e2e_summary_and_exit
fi
pass "setup: app=$APP id=$app_id token=${token:0:10}..."

cleanup() {
    e2e_login
  api_status DELETE "/api/apps/$app_id" >/dev/null 2>&1
  docker_cleanup_app "$APP"
}
trap cleanup EXIT

# ---- 缺 Authorization: 401 --------------------------------
section "missing Authorization"

resp=$(api POST "/api/apps/$APP/trigger" '{"tag":"v1"}')
status=$(echo "$resp" | tail -1)
assert_status "$status" 401 "no auth header"

# ---- 错 token: 401 -----------------------------------------
section "wrong bearer token"

resp=$(api_bearer "wrong-token-12345" POST "/api/apps/$APP/trigger" '{"tag":"v1"}')
status=$(echo "$resp" | tail -1)
assert_status "$status" 401 "wrong token"

# ---- 对 token + 合法 tag: 202 + deployId -------------------
section "valid trigger"

resp=$(api_bearer "$token" POST "/api/apps/$APP/trigger" '{"tag":"v1","commit_message":"e2e test"}')
status=$(echo "$resp" | tail -1)
body=$(echo "$resp" | sed '$d')

assert_status "$status" 202 "valid trigger"
assert_jq "$body" '.accepted' "true" "accepted: true"
assert_jq_exists "$body" '.deployId' "deployId returned"
assert_jq "$body" '.tag' "v1" "tag echoed"

deploy_id=$(echo "$body" | jq -r '.deployId')

# ---- 同一 app 立即再 trigger: 命中 lock -------------------
section "deploy lock (immediate second trigger)"

resp=$(api_bearer "$token" POST "/api/apps/$APP/trigger" '{"tag":"v2"}')
status=$(echo "$resp" | tail -1)
body=$(echo "$resp" | sed '$d')

# 可能是 202 (with accepted:false) 或 409 (conflict)
case "$status" in
  202)
    assert_jq "$body" '.accepted' "false" "second trigger rejected via accepted:false"
    ;;
  409)
    pass "second trigger rejected with 409"
    ;;
  *)
    fail "second trigger expected 202/409, got $status"
    ;;
esac

# ---- 验证 deploy 记录出现在 history 里 -------------------
section "deploy history shows the trigger"

# deploy 可能 running / failed / success
hist_body=$(api_body GET "/api/apps/$app_id/deployments")
assert_jq_exists "$hist_body" ".[] | select(.id == $deploy_id)" "deploy in history"
first_status=$(echo "$hist_body" | jq -r ".[] | select(.id == $deploy_id) | .status")
case "$first_status" in
  running|success|failed) pass "deploy status: $first_status" ;;
  *)                       fail "unexpected deploy status: $first_status" ;;
esac

# ---- 非法 tag: 400 ----------------------------------------
section "invalid tag rejected"

# tag 含 shell metachar
resp=$(api_bearer "$token" POST "/api/apps/$APP/trigger" '{"tag":"v1;rm -rf /"}')
status=$(echo "$resp" | tail -1)
assert_status "$status" 400 "tag with shell metachar rejected"

# tag 过长
long_tag=$(printf 'a%.0s' {1..200})
resp=$(api_bearer "$token" POST "/api/apps/$APP/trigger" "$(jq -nc --arg t "$long_tag" '{tag:$t}')")
status=$(echo "$resp" | tail -1)
assert_status "$status" 400 "tag too long rejected"

# ---- app 不存在: 404 --------------------------------------
section "non-existent app: 404"

resp=$(api_bearer "$token" POST "/api/apps/does-not-exist-xyz/trigger" '{"tag":"v1"}')
status=$(echo "$resp" | tail -1)
assert_status "$status" 404 "unknown app"

# ---- 无 trigger 配置: 404 ---------------------------------
section "app without trigger token: 404"

# 创建另一个没 rotate token 的 app
APP2="e2e-notrig-$SUFFIX"
create2=$(api POST /api/apps "$(jq -nc --arg n "$APP2" '{name:$n, image:"nginx:alpine", port:8080}')")
app2_id=$(echo "$create2" | sed '$d' | jq -r '.id')

if [ -n "$app2_id" ] && [ "$app2_id" != "null" ]; then
  resp=$(api POST "/api/apps/$APP2/trigger" '{"tag":"v1"}')
  status=$(echo "$resp" | tail -1)
  assert_status "$status" 404 "app without trigger returns 404"

  # 清理 APP2
    e2e_login
  api_status DELETE "/api/apps/$app2_id" >/dev/null 2>&1
fi

case_done
e2e_summary_and_exit
