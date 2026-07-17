#!/usr/bin/env bash
# test-33-apps-trigger.sh — Trigger token rotate + DB encryption + List/Get do not return it
#
# Does not need caddy / docker actually running (only tests token rotation and persistence)

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-trigapp-$SUFFIX"

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

# ---- initially no token -------------------------------------------
section "initially no trigger token"

get_body=$(api_body GET "/api/apps/$app_id")
assert_jq "$get_body" '.triggerConfigured' "false" "triggerConfigured false at create"
assert_jq "$get_body" '.triggerToken' "null" "triggerToken null at create"

# ---- first rotate: returns token -------------------------------
section "first rotate: returns token + persisted"

rotate_resp=$(api POST "/api/apps/$app_id/rotate-trigger-token")
rotate_status=$(echo "$rotate_resp" | tail -1)
rotate_body=$(echo "$rotate_resp" | sed '$d')

assert_status "$rotate_status" 200 "rotate"
token1=$(echo "$rotate_body" | jq -r '.triggerToken')
if [ -z "$token1" ] || [ "$token1" = "null" ]; then
  fail "rotate did not return a token"
  e2e_summary_and_exit
fi
# token length reasonable (should be 40+ chars)
if [ "${#token1}" -lt 20 ]; then
  fail "token looks too short: $token1"
else
  pass "token returned (length=${#token1})"
fi

# verify Get now shows triggerConfigured=true
get_body=$(api_body GET "/api/apps/$app_id")
assert_jq "$get_body" '.triggerConfigured' "true" "triggerConfigured true after rotate"
# Get still does not return token
assert_jq "$get_body" '.triggerToken' "null" "Get still omits triggerToken"

# List also still omits it
list_body=$(api_body GET /api/apps)
assert_jq "$list_body" ".[] | select(.id == $app_id) | .triggerToken" "null" "List omits triggerToken"

# ---- encrypted in DB -----------------------------------------
section "trigger token encrypted in DB"

if command -v sqlite3 >/dev/null 2>&1; then
  db_value=$(sqlite3 "$E2E_DB" "SELECT trigger_token FROM apps WHERE id=$app_id;" 2>/dev/null)
  if [ -z "$db_value" ]; then
    fail "could not read trigger_token from DB"
  else
    case "$db_value" in
      enc:*) pass "DB trigger_token is encrypted" ;;
      *)     fail "DB trigger_token not encrypted: $db_value" ;;
    esac
  fi
else
  skip "sqlite3 not installed"
fi

# ---- second rotate: token changes -------------------------------
section "second rotate: token changes"

# after getting token1, rotate
rotate_resp=$(api POST "/api/apps/$app_id/rotate-trigger-token")
token2=$(echo "$rotate_resp" | sed '$d' | jq -r '.triggerToken')
if [ -z "$token2" ] || [ "$token2" = "null" ]; then
  fail "second rotate did not return a token"
elif [ "$token1" = "$token2" ]; then
  fail "second rotate returned same token as first"
else
  pass "second rotate produced a different token"
fi

case_done
e2e_summary_and_exit
