#!/usr/bin/env bash
# test-30-apps-crud.sh - App CRUD (docker mode + some compose-mode branches)
#
# Does not require the caddy container to be up (nanoku CreateApp does not trigger reload),
# but docker must be up (because toAppDTO tries to refresh current_container state via h.Docker).
# Gate with `require_docker`; SKIP when docker is absent.

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
APP1="e2e-app1-$SUFFIX"
APP2="e2e-app2-$SUFFIX"

# ---- Missing image / port should be 400 ------------------------------------
section "validation: docker mode requires image + port"

# Missing image
status=$(api_status POST /api/apps "$(jq -nc --arg n "$APP1" '{name:$n, port:8080}')")
assert_status "$status" 400 "missing image"

# Missing port
status=$(api_status POST /api/apps "$(jq -nc --arg n "$APP1" '{name:$n, image:"nginx:alpine"}')")
assert_status "$status" 400 "missing port"

# Port out of range
status=$(api_status POST /api/apps "$(jq -nc --arg n "$APP1" '{name:$n, image:"nginx:alpine", port:99999}')")
assert_status "$status" 400 "port out of range"

# Invalid name
status=$(api_status POST /api/apps "$(jq -nc '{name:"Invalid-Name", image:"nginx:alpine", port:8080}')")
assert_status "$status" 400 "invalid name"

# ---- Create a docker-mode app ---------------------------------
section "create docker-mode app"

create_body=$(jq -nc --arg n "$APP1" --arg i "nginx:alpine" '{name:$n, image:$i, port:8080}')
create_resp=$(api POST /api/apps "$create_body")
create_status=$(echo "$create_resp" | tail -1)
create_body_resp=$(echo "$create_resp" | sed '$d')

assert_status "$create_status" 201 "create app"
app1_id=$(echo "$create_body_resp" | jq -r '.id')
assert_jq "$create_body_resp" '.name' "$APP1" "app name"
assert_jq "$create_body_resp" '.image' "nginx:alpine" "app image"
assert_jq "$create_body_resp" '.port' "8080" "app port"
assert_jq "$create_body_resp" '.deployMethod' "docker" "default deployMethod"
assert_jq "$create_body_resp" '.triggerConfigured' "false" "no trigger token at create"
# Create does not return a token
assert_jq "$create_body_resp" '.triggerToken' "null" "no trigger token at create"

# ---- List / Get do not expose token -----------------------------------
section "List/Get do not expose triggerToken"

list_body=$(api_body GET /api/apps)
assert_jq_exists "$list_body" ".[] | select(.id == $app1_id) | .name" "in list"
# triggerToken is not exposed in List/Get (sensitive field)
assert_jq "$list_body" ".[] | select(.id == $app1_id) | .triggerToken" "null" "List omits triggerToken"

get_body=$(api_body GET "/api/apps/$app1_id")
assert_jq "$get_body" '.triggerToken' "null" "Get omits triggerToken"

# ---- UpdateApp partial-field semantics ------------------------------------
section "update app image and port"

update_resp=$(api PATCH "/api/apps/$app1_id" "$(jq -nc --arg i "nginx:1.27-alpine" --arg p "9090" '{image:$i, port:($p | tonumber)}')" 2>/dev/null)
# Actually PUT, not PATCH
update_status=$(api_status PUT "/api/apps/$app1_id" "$(jq -nc --arg i "nginx:1.27-alpine" '{image:$i, port:9090}')")
assert_status "$update_status" 200 "update app"

get_body=$(api_body GET "/api/apps/$app1_id")
assert_jq "$get_body" '.image' "nginx:1.27-alpine" "image updated"
assert_jq "$get_body" '.port' "9090" "port updated"

# ---- registry credentials (set + ClearRegistry behavior) -----------
section "registry credentials (set + clear)"

# Set
update_status=$(api_status PUT "/api/apps/$app1_id" "$(jq -nc --arg u "https://ghcr.io" --arg n "alice" --arg p "secret123" '{registryUrl:$u, registryUsername:$n, registryPassword:$p}')")
assert_status "$update_status" 200 "set registry creds"
get_body=$(api_body GET "/api/apps/$app1_id")
assert_jq "$get_body" '.registryConfigured' "true" "registryConfigured true"
assert_jq "$get_body" '.registryUrl' "https://ghcr.io" "registryUrl set"
assert_jq "$get_body" '.registryUsername' "alice" "registryUsername set"
# password is not returned
assert_jq "$get_body" '.registryPassword' "null" "registryPassword never returned"

# Clear
update_status=$(api_status PUT "/api/apps/$app1_id" '{"clearRegistry": true}')
assert_status "$update_status" 200 "clear registry"
get_body=$(api_body GET "/api/apps/$app1_id")
assert_jq "$get_body" '.registryConfigured' "false" "registryConfigured false after clear"

# ---- Duplicate app name should be 409 -----------------------------------------
section "duplicate name rejected"

# Change APP2 name to APP1
status=$(api_status POST /api/apps "$(jq -nc --arg n "$APP1" --arg i "alpine" '{name:$n, image:$i, port:8080}')")
# Should be 4xx (409 Conflict or 400)
case "$status" in
  4*) pass "duplicate name returns 4xx ($status)" ;;
  *)   fail "duplicate name expected 4xx, got $status" ;;
esac

# ---- Creating APP1 again should be 4xx ----------------------------
section "create same name twice rejected"

status=$(api_status POST /api/apps "$(jq -nc --arg n "$APP1" --arg i "alpine" '{name:$n, image:$i, port:9090}')")
case "$status" in
  4*) pass "second create returns 4xx ($status)" ;;
  *)   fail "second create expected 4xx, got $status" ;;
esac

# ---- DeleteApp ----------------------------------------------
section "delete app"

del_status=$(api_status DELETE "/api/apps/$app1_id")
assert_status "$del_status" 204 "delete app"

# GET after delete should be 404
get_status=$(api_status GET "/api/apps/$app1_id")
assert_status "$get_status" 404 "get deleted app"

# Deleting a non-existent id should be 404
del_status=$(api_status DELETE "/api/apps/999999")
assert_status "$del_status" 404 "delete non-existent"

case_done
e2e_summary_and_exit
