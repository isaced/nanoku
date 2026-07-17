#!/usr/bin/env bash
# test-32-apps-volumes.sh — App volume PUT 覆盖 + 顺序按 ID
#
# 不依赖 caddy,但需要 docker (h.Docker 在 PUT 时校验 mount 合法性)

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-volapp-$SUFFIX"

create_body=$(jq -nc --arg n "$APP" '{name:$n, image:"nginx:alpine", port:8080}')
create_resp=$(api POST /api/apps "$create_body")
app_id=$(echo "$create_resp" | sed '$d' | jq -r '.id')
if [ -z "$app_id" ] || [ "$app_id" = "null" ]; then
  fail "failed to create test app"
  e2e_summary_and_exit
fi
pass "created test app id=$app_id"

cleanup() {
    e2e_login
  api_status DELETE "/api/apps/$app_id" >/dev/null 2>&1
}
trap cleanup EXIT

# ---- 初始 volumes 为空 --------------------------------------
section "initial volumes empty"

get_body=$(api_body GET "/api/apps/$app_id/volumes")
assert_jq "$get_body" 'length' "0" "no volumes initially"

# ---- PUT 2 个 named volume ----------------------------------
section "PUT named volumes"

vols=$(jq -nc '[
  {type:"volume", source:"data-pg", target:"/var/lib/postgresql/data", readOnly:false},
  {type:"volume", source:"cache-redis", target:"/data", readOnly:false}
]')
put_status=$(api_status PUT "/api/apps/$app_id/volumes" "$vols")
assert_status "$put_status" 200 "PUT volumes"

list_body=$(api_body GET "/api/apps/$app_id/volumes")
assert_jq "$list_body" 'length' "2" "2 volumes"
# 顺序按 ID 升序
assert_jq "$list_body" ".[0].source" "data-pg" "first volume source"
assert_jq "$list_body" ".[0].target" "/var/lib/postgresql/data" "first volume target"
assert_jq "$list_body" ".[1].source" "cache-redis" "second volume source"

# ---- PUT 整组替换(不是 merge) ----------------------------
section "PUT full replace"

vols2=$(jq -nc '[
  {type:"volume", source:"only-one", target:"/x", readOnly:false}
]')
put_status=$(api_status PUT "/api/apps/$app_id/volumes" "$vols2")
assert_status "$put_status" 200 "PUT replace"

list_body=$(api_body GET "/api/apps/$app_id/volumes")
assert_jq "$list_body" 'length' "1" "1 volume after replace"
assert_jq "$list_body" ".[0].source" "only-one" "new volume"

# ---- unnamed volume 自动命名 ------------------------------
section "unnamed volume gets auto name"

vols3=$(jq -nc '[
  {type:"volume", target:"/data", readOnly:false}
]')
put_status=$(api_status PUT "/api/apps/$app_id/volumes" "$vols3")
assert_status "$put_status" 200 "PUT unnamed"

list_body=$(api_body GET "/api/apps/$app_id/volumes")
assert_jq "$list_body" ".[0].source" "nanoku-$APP-vol-0" "auto-named volume"

# ---- bind mount 也行 ----------------------------------------
section "bind mount accepted"

vols4=$(jq -nc '[
  {type:"bind", source:"/tmp", target:"/host-tmp", readOnly:true}
]')
put_status=$(api_status PUT "/api/apps/$app_id/volumes" "$vols4")
assert_status "$put_status" 200 "PUT bind"

list_body=$(api_body GET "/api/apps/$app_id/volumes")
assert_jq "$list_body" ".[0].type" "bind" "bind type"
assert_jq "$list_body" ".[0].source" "/tmp" "bind source"
assert_jq "$list_body" ".[0].readOnly" "true" "readOnly"

# ---- 非法 type 400 -----------------------------------------
section "invalid type rejected"

status=$(api_status PUT "/api/apps/$app_id/volumes" '[{"type":"nfs","target":"/x"}]')
assert_status "$status" 400 "type=nfs rejected"

# ---- 重复 target 400 ---------------------------------------
section "duplicate target rejected"

status=$(api_status PUT "/api/apps/$app_id/volumes" '[
  {"type":"volume","target":"/x"},
  {"type":"volume","target":"/x"}
]')
assert_status "$status" 400 "duplicate target"

# ---- 删 app 时 volumes 一起清 -----------------------------
section "no orphan volumes after app delete"

cleanup
if command -v sqlite3 >/dev/null 2>&1; then
  remaining=$(sqlite3 "$E2E_DB" "SELECT count(*) FROM volumes WHERE app_volumes=$app_id;" 2>/dev/null)
  if [ "$remaining" = "0" ]; then
    pass "no volumes linked to deleted app"
  else
    fail "found $remaining orphan volumes"
  fi
else
  skip "sqlite3 not installed — skip DB check"
fi

case_done
e2e_summary_and_exit
