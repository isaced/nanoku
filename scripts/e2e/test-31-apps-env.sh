#!/usr/bin/env bash
# test-31-apps-env.sh - App env var PUT overwrite semantics + encrypted-at-rest verification
#
# Covers:
#   - PUT /api/apps/{id}/env replaces the whole set (not a merge)
#   - List returns plaintext (DB stores with enc: prefix)
#   - duplicate key -> 400
#   - empty key -> 400
#   - invalid env var format -> 400

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

# Log in
e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-envapp-$SUFFIX"

# Prepare an app
create_body=$(jq -nc --arg n "$APP" '{name:$n, image:"nginx:alpine", port:8080}')
create_resp=$(api POST /api/apps "$create_body")
app_id=$(echo "$create_resp" | sed '$d' | jq -r '.id')
if [ -z "$app_id" ] || [ "$app_id" = "null" ]; then
  fail "failed to create test app"
  e2e_summary_and_exit
fi
pass "created test app id=$app_id"

# Cleanup function: delete app when test ends
cleanup() {
    e2e_login
  api_status DELETE "/api/apps/$app_id" >/dev/null 2>&1
}
trap cleanup EXIT

# ---- Initial env should be empty -----------------------------------------
section "initial env is empty"

get_body=$(api_body GET "/api/apps/$app_id")
assert_jq "$get_body" '.envVars // [] | length' "0" "envVars empty initially"

# ---- PUT three env vars ---------------------------------------
section "PUT replaces env vars"

envs=$(jq -nc '[{key:"DB_URL", value:"postgres://x:y@db:5432/p"}, {key:"LOG_LEVEL", value:"debug"}, {key:"API_KEY", value:"sk-test-1234567890"}]')
put_status=$(api_status PUT "/api/apps/$app_id/env" "$envs")
assert_status "$put_status" 200 "PUT envs"

# Verify Get returns plaintext values
get_body=$(api_body GET "/api/apps/$app_id")
assert_jq "$get_body" '.envVars | length' "3" "3 env vars"
assert_jq "$get_body" '[.envVars[] | select(.key=="DB_URL") | .value][0]' "postgres://x:y@db:5432/p" "DB_URL plaintext"
assert_jq "$get_body" '[.envVars[] | select(.key=="API_KEY") | .value][0]' "sk-test-1234567890" "API_KEY plaintext"

# ---- DB stores encrypted values (enc: prefix) -------------------------------
section "env values are encrypted in DB"

# Query directly with sqlite3. $E2E_DB is the DB file used by nanoku for E2E.
# Encryption format: enc: + base64(nonce || ct || tag)
# Check via query whether the value field contains "enc:"
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

# ---- PUT replaces the whole set (not a merge) -------------------------------
section "PUT is full replace (not merge)"

new_envs=$(jq -nc '[{key:"ONLY_ONE", value:"now-only-this"}]')
put_status=$(api_status PUT "/api/apps/$app_id/env" "$new_envs")
assert_status "$put_status" 200 "PUT to single env"

get_body=$(api_body GET "/api/apps/$app_id")
assert_jq "$get_body" '.envVars | length' "1" "1 env after replace"
assert_jq "$get_body" '[.envVars[] | .key][0]' "ONLY_ONE" "only ONLY_ONE remains"
# The previous DB_URL should be gone
assert_jq "$get_body" '[.envVars[] | select(.key=="DB_URL")] | length' "0" "DB_URL gone"

# ---- Duplicate key -> 400 -------------------------------------------
section "duplicate key rejected"

dup=$(jq -nc '[{key:"X", value:"1"}, {key:"X", value:"2"}]')
status=$(api_status PUT "/api/apps/$app_id/env" "$dup")
assert_status "$status" 400 "duplicate key"

# ---- Empty key -> 400 --------------------------------------------
section "empty key rejected"

empty=$(jq -nc '[{key:"", value:"v"}]')
status=$(api_status PUT "/api/apps/$app_id/env" "$empty")
assert_status "$status" 400 "empty key"

# ---- Invalid env var name -> 400 ----------------------------------
section "invalid env var name rejected"

# docker env var naming rule: must start with a letter or underscore, only letters/digits/underscores allowed
invalid=$(jq -nc '[{key:"1INVALID", value:"v"}]')
status=$(api_status PUT "/api/apps/$app_id/env" "$invalid")
assert_status "$status" 400 "invalid env var name"

# ---- env_vars cleaned up when app is deleted ---------------------
section "env vars cleaned up on app delete"

cleanup
# Query DB to confirm no env vars for this app remain in the env_vars table
# (FK is ON DELETE SET NULL, so rows may remain but app_env_vars should be NULL)
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
