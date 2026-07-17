#!/usr/bin/env bash
# test-31-apps-env.sh — App 环境变量 PUT 覆盖语义 + 加密存盘验证
#
# 覆盖:
#   - PUT /api/apps/{id}/env 整组替换(不是 merge)
#   - List 出来是明文(DB 里是 enc: 前缀)
#   - 重复 key 400
#   - 空 key 400
#   - 非法 env var 格式 400

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

# 登录
e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-envapp-$SUFFIX"

# 准备一个 app
create_body=$(jq -nc --arg n "$APP" '{name:$n, image:"nginx:alpine", port:8080}')
create_resp=$(api POST /api/apps "$create_body")
app_id=$(echo "$create_resp" | sed '$d' | jq -r '.id')
if [ -z "$app_id" ] || [ "$app_id" = "null" ]; then
  fail "failed to create test app"
  e2e_summary_and_exit
fi
pass "created test app id=$app_id"

# 清理函数:test 结束删 app
cleanup() {
    e2e_login
  api_status DELETE "/api/apps/$app_id" >/dev/null 2>&1
}
trap cleanup EXIT

# ---- 初始 env 应为空 -----------------------------------------
section "initial env is empty"

get_body=$(api_body GET "/api/apps/$app_id")
assert_jq "$get_body" '.envVars // [] | length' "0" "envVars empty initially"

# ---- PUT 三个 env vars ---------------------------------------
section "PUT replaces env vars"

envs=$(jq -nc '[{key:"DB_URL", value:"postgres://x:y@db:5432/p"}, {key:"LOG_LEVEL", value:"debug"}, {key:"API_KEY", value:"sk-test-1234567890"}]')
put_status=$(api_status PUT "/api/apps/$app_id/env" "$envs")
assert_status "$put_status" 200 "PUT envs"

# 验证 Get 出来 value 是明文
get_body=$(api_body GET "/api/apps/$app_id")
assert_jq "$get_body" '.envVars | length' "3" "3 env vars"
assert_jq "$get_body" '[.envVars[] | select(.key=="DB_URL") | .value][0]' "postgres://x:y@db:5432/p" "DB_URL plaintext"
assert_jq "$get_body" '[.envVars[] | select(.key=="API_KEY") | .value][0]' "sk-test-1234567890" "API_KEY plaintext"

# ---- DB 里是加密的 (enc: 前缀) -------------------------------
section "env values are encrypted in DB"

# 用 sqlite3 直接查。$E2E_DB 是 E2E 用 nanoku 的 DB 文件
# 加密格式:enc: + base64(nonce || ct || tag)
# 通过 query 看 value 字段是否包含 "enc:"
if ! command -v sqlite3 >/dev/null 2>&1; then
  skip "sqlite3 not installed (brew install sqlite) — skip DB-level check"
else
  db_value=$(sqlite3 "$E2E_DB" "SELECT value FROM env_vars WHERE key='API_KEY' AND app_env_vars=$app_id LIMIT 1;" 2>/dev/null)
  if [ -z "$db_value" ]; then
    fail "could not read env_vars from DB"
  else
    case "$db_value" in
      enc:*) pass "DB value is encrypted (starts with enc:)" ;;
      *)     fail "DB value not encrypted: $db_value" ;;
    esac
  fi
fi

# ---- PUT 整组替换(不是 merge) -------------------------------
section "PUT is full replace (not merge)"

new_envs=$(jq -nc '[{key:"ONLY_ONE", value:"now-only-this"}]')
put_status=$(api_status PUT "/api/apps/$app_id/env" "$new_envs")
assert_status "$put_status" 200 "PUT to single env"

get_body=$(api_body GET "/api/apps/$app_id")
assert_jq "$get_body" '.envVars | length' "1" "1 env after replace"
assert_jq "$get_body" '[.envVars[] | .key][0]' "ONLY_ONE" "only ONLY_ONE remains"
# 原来的 DB_URL 应该没了
assert_jq "$get_body" '[.envVars[] | select(.key=="DB_URL")] | length' "0" "DB_URL gone"

# ---- 重复 key 400 -------------------------------------------
section "duplicate key rejected"

dup=$(jq -nc '[{key:"X", value:"1"}, {key:"X", value:"2"}]')
status=$(api_status PUT "/api/apps/$app_id/env" "$dup")
assert_status "$status" 400 "duplicate key"

# ---- 空 key 400 --------------------------------------------
section "empty key rejected"

empty=$(jq -nc '[{key:"", value:"v"}]')
status=$(api_status PUT "/api/apps/$app_id/env" "$empty")
assert_status "$status" 400 "empty key"

# ---- 非法 env var name 400 ----------------------------------
section "invalid env var name rejected"

# docker env var 名规则:以字母或下划线开头,只能包含字母数字下划线
invalid=$(jq -nc '[{key:"1INVALID", value:"v"}]')
status=$(api_status PUT "/api/apps/$app_id/env" "$invalid")
assert_status "$status" 400 "invalid env var name"

# ---- DELETE app 时 env_vars 一起清 ---------------------
section "env vars cleaned up on app delete"

cleanup
# 查 DB 确认 env_vars 表里没这个 app 的 env 了
# (FK 是 ON DELETE SET NULL,所以行可能还在但 app_env_vars 应当是 NULL)
if command -v sqlite3 >/dev/null 2>&1; then
  linked=$(sqlite3 "$E2E_DB" "SELECT count(*) FROM env_vars WHERE app_env_vars=$app_id;" 2>/dev/null)
  if [ "$linked" = "0" ]; then
    pass "no env_vars linked to deleted app (ON DELETE SET NULL)"
  else
    fail "found $linked env_vars still linked to deleted app"
  fi
else
  skip "sqlite3 not installed — skip DB-level check"
fi

case_done
e2e_summary_and_exit
