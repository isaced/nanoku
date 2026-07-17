#!/usr/bin/env bash
# test-70-proxy.sh - full chain: create app -> deploy -> bind site -> curl caddy for response
#
# This is the most "end-to-end" E2E: simulates a user accessing caddy from external HTTP, verifies:
#   - app is actually running (nginx listening on 80)
#   - site config written to Caddyfile
#   - caddy reverse proxies to app container
#   - response from external curl is nginx's default page (not caddy's own 404)
#
# Topology (test):
#   curl -> 127.0.0.1:18080 (caddy container :80 mapped to host's E2E_CADDY_HTTP_PORT)
#       -> caddy resolves Host -> finds site -> reverse proxies to nanoku-<app> -> app container:8080
#
# All depends on caddy + docker

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

# Pull a small and stable image. E2E_CADDY_HTTP_PORT is defined in lib.sh (default 18080)
# Note: this will conflict with the nanoku admin port! Change to a different port.
E2E_CADDY_HTTP_PORT=${E2E_CADDY_HTTP_PORT:-18080}
# Actually we go through caddy container's :80 (from host via 18080). But 18080 is already taken by nanoku.
# Use 18081 (to avoid conflict) -- but caddy container maps 80:80 to host's 80, won't reach 18081.
# Correct approach: have caddy map :80 to a non-default host port (need to modify docker manager's EnsureCaddyContainer).
# Workaround: curl from inside the caddy container (using docker exec).

docker_pull_if_missing "nginx:alpine"

e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-proxy-$SUFFIX"
DOMAIN="e2e-$SUFFIX.test"

# 1. create app
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

# 2. deploy
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

# 3. verify container is actually running
running=$(docker inspect "nanoku-$APP" --format '{{.State.Running}}' 2>/dev/null)
[ "$running" = "true" ] && pass "container running" || fail "container not running"

# 4. create site
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

# 5. Caddyfile contains this site
section "4. Caddyfile reflects site"
# /api/caddyfile returns text/plain (not JSON), use assert_contains.
# Previously used assert_jq_exists to run the domain as a jq expression on a non-JSON body,
# which would always fail -- a false-positive bug.
caddyfile=$(api_body GET /api/caddyfile)
assert_contains "$caddyfile" "$DOMAIN" "caddyfile contains domain"

# 6. actually curl caddy for response
# go through caddy container's :80, use Host header to simulate the domain
section "5. curl caddy with Host: $DOMAIN"
sleep 2  # give caddy reload some time

# curl inside the caddy container (because host:80 may be taken)
# caddy container name = $E2E_CADDY_CONTAINER
caddy_container="$E2E_CADDY_CONTAINER"
if docker exec "$caddy_container" test -f /usr/bin/curl 2>/dev/null; then
  caddy_curl="docker exec $caddy_container curl -sS -i -H 'Host: $DOMAIN' http://127.0.0.1/"
else
  caddy_curl="docker exec $caddy_container wget -qO- --header='Host: $DOMAIN' http://127.0.0.1/"
fi

response=$(eval "$caddy_curl" 2>&1)
status_line=$(echo "$response" | head -1 | tr -d '\r')

# expect: 200 (nginx response)
echo "  caddy response: $status_line"
if echo "$status_line" | grep -q "200"; then
  pass "caddy returned 200"
elif echo "$status_line" | grep -qE "30[127]"; then
  pass "caddy returned $status_line (redirect to https - expected in prod)"
else
  fail "caddy returned: $status_line"
  echo "  body: $(echo "$response" | head -10)"
fi

# body contains nginx marker (not caddy's 404 or similar)
if echo "$response" | grep -qiE "nginx|welcome"; then
  pass "response body contains nginx marker"
else
  fail "response body doesn't look like nginx"
  echo "  first 5 lines:"
  echo "$response" | head -5 | sed 's/^/    /'
fi

# 7. delete site
section "6. cleanup site"
del_status=$(api_status DELETE "/api/sites/$site_id")
assert_status "$del_status" 204 "delete site"
site_id=""

case_done
e2e_summary_and_exit
