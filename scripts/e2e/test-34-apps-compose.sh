#!/usr/bin/env bash
# test-34-apps-compose.sh — compose mode app (inline YAML)

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-cmpapp-$SUFFIX"

# ---- missing composePath and composeContent should 400 -----------------
section "validation: compose mode needs content or path"

status=$(api_status POST /api/apps "$(jq -nc --arg n "$APP" '{name:$n, deployMethod:"compose"}')")
assert_status "$status" 400 "compose mode without content/path"

# ---- inline compose YAML --------------------------------------
section "create compose app with inline YAML"

compose_yaml='services:
  web:
    image: nginx:alpine
    ports:
      - "8080"
'
create_body=$(jq -nc --arg n "$APP" --arg y "$compose_yaml" '{name:$n, deployMethod:"compose", composeContent:$y}')
create_resp=$(api POST /api/apps "$create_body")
create_status=$(echo "$create_resp" | tail -1)
create_body_resp=$(echo "$create_resp" | sed '$d')

assert_status "$create_status" 201 "create compose app"
app_id=$(echo "$create_body_resp" | jq -r '.id')
assert_jq "$create_body_resp" '.deployMethod' "compose" "deployMethod=compose"
# composeContent must contain at least services + nginx:alpine (exact byte alignment is fragile; nanoku may trim trailing whitespace when writing the file)
assert_jq_exists "$create_body_resp" '.composeContent | select(contains("nginx:alpine") and contains("services:"))' "composeContent contains services + image"
# composeFile should be auto-generated (under $NANOKU_COMPOSE_DIR)
assert_jq_exists "$create_body_resp" '.composeFile' "composeFile auto-generated"

# verify the file was actually written to disk
if [ -n "$app_id" ] && [ "$app_id" != "null" ]; then
  cleanup() {
    : > "$E2E_COOKIE"
    e2e_login
    api_status DELETE "/api/apps/$app_id" >/dev/null 2>&1
  }
  trap cleanup EXIT

  compose_file=$(echo "$create_body_resp" | jq -r '.composeFile')
  if [ -f "$compose_file" ]; then
    pass "compose file exists on disk: $compose_file"
    # strip trailing whitespace when comparing (diff -q is sensitive to trailing newlines)
    on_disk=$(tr -d '\n' < "$compose_file")
    expected=$(printf '%s' "$compose_yaml" | tr -d '\n')
    if [ "$on_disk" = "$expected" ]; then
      pass "compose file content matches"
    else
      fail "compose file content mismatch"
      echo "  on_disk bytes: ${#on_disk}"
      echo "  expected bytes: ${#expected}"
    fi
  else
    fail "compose file not on disk: $compose_file"
  fi
fi

# ---- composePath mode ----------------------------------------
section "create compose app with path"

APP2="e2e-cmppath-$SUFFIX"
mkdir -p "$E2E_WORKDIR/compose-test"
echo 'services:
  api:
    image: alpine
    command: ["sleep", "infinity"]
' > "$E2E_WORKDIR/compose-test/docker-compose.yml"

create_body=$(jq -nc --arg n "$APP2" --arg p "$E2E_WORKDIR/compose-test/docker-compose.yml" '{name:$n, deployMethod:"compose", composePath:$p}')
create_status=$(api_status POST /api/apps "$create_body")
assert_status "$create_status" 201 "create with composePath"

app2_id=$(api_body GET "/api/apps" | jq -r ".[] | select(.name==\"$APP2\") | .id")
if [ -n "$app2_id" ] && [ "$app2_id" != "null" ]; then
  # use a nested function to avoid jq escaping issues in the trap string
  cleanup2() {
    : > "$E2E_COOKIE"
    e2e_login
    api_status DELETE "/api/apps/$app2_id" >/dev/null 2>&1
  }
  trap cleanup2 EXIT
  get_body=$(api_body GET "/api/apps/$app2_id")
  assert_jq "$get_body" '.composeFile' "$E2E_WORKDIR/compose-test/docker-compose.yml" "composeFile echoes composePath"
fi

case_done
e2e_summary_and_exit
