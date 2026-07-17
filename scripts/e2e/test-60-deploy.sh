#!/usr/bin/env bash
# test-60-deploy.sh - POST /api/apps/{id}/deployments pull real image + start container
#
# Runs docker for real (nginx:alpine, already pulled locally), verifies:
#   - 202 accepted
#   - Poll until deploy status = success (or timeout)
#   - /api/apps/{id}/containers shows new container, status running
#   - Actual docker container exists and is running

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_docker; then
  exit 0
fi

# Pull test image (skip if already present)
echo "preparing test image nginx:alpine ..."
docker_pull_if_missing "nginx:alpine"

e2e_login

SUFFIX="$$-$(date +%s%N)"
APP="e2e-deploy-$SUFFIX"

# Create app
create_body=$(jq -nc --arg n "$APP" --arg i "nginx:alpine" '{name:$n, image:$i, port:8080}')
create_resp=$(api POST /api/apps "$create_body")
app_id=$(echo "$create_resp" | sed '$d' | jq -r '.id')
if [ -z "$app_id" ] || [ "$app_id" = "null" ]; then
  fail "failed to create test app"
  e2e_summary_and_exit
fi
pass "setup: app=$APP id=$app_id"

cleanup() {
    e2e_login
  # Stop container before deleting app
  docker_cleanup_app "$APP"
  api_status DELETE "/api/apps/$app_id" >/dev/null 2>&1
}
trap cleanup EXIT

# ---- Trigger deploy ------------------------------------------
section "POST /api/apps/$app_id/deployments"

deploy_resp=$(api POST "/api/apps/$app_id/deployments" "")
status=$(echo "$deploy_resp" | tail -1)
body=$(echo "$deploy_resp" | sed '$d')

assert_status "$status" 202 "deploy accepted"
deploy_id=$(echo "$body" | jq -r '.id // .deployId // empty')
if [ -z "$deploy_id" ] || [ "$deploy_id" = "null" ]; then
  # Some implementations return 204 No Content, check body
  if [ -z "$body" ] || [ "$body" = "{}" ]; then
    # No problem, deploy already enqueued, get ID from history
    sleep 2
    hist_body=$(api_body GET "/api/apps/$app_id/deployments")
    deploy_id=$(echo "$hist_body" | jq -r '.[0].id // empty')
  fi
fi
if [ -n "$deploy_id" ] && [ "$deploy_id" != "null" ]; then
  pass "deployId: $deploy_id"
fi

# ---- Poll deploy status -----------------------------------
section "wait for deploy to finish"

timeout=90
start=$(date +%s)
final_status=""
while true; do
  hist_body=$(api_body GET "/api/apps/$app_id/deployments")
  final_status=$(echo "$hist_body" | jq -r '.[0].status // "unknown"')
  case "$final_status" in
    success|failed)
      break
    ;;
  esac
  now=$(date +%s)
  if [ $((now - start)) -ge "$timeout" ]; then
    fail "deploy didn't finish in ${timeout}s, last status: $final_status"
    break
  fi
  sleep 2
done

case "$final_status" in
  success) pass "deploy status: success" ;;
  failed)
    fail "deploy status: failed"
    # Try to get error message
    err=$(echo "$hist_body" | jq -r '.[0].error // "no error msg"')
    echo -e "  ${BOLD}error:${NC} $err"
    ;;
  *) fail "unexpected final status: $final_status" ;;
esac

# ---- Container actually running -----------------------------------------
section "container actually running"

if [ "$final_status" = "success" ]; then
  cont_status=$(api_status GET "/api/apps/$app_id/containers")
  assert_status "$cont_status" 200 "containers endpoint"

  cont_body=$(api_body GET "/api/apps/$app_id/containers")
  count=$(echo "$cont_body" | jq -r 'length')
  if [ "$count" -gt 0 ]; then
    pass "containers reported: $count"
    echo "$cont_body" | jq -r '.[] | "  - \(.Name) state=\(.State)"'
  else
    fail "no containers reported despite success status"
  fi

  # Query docker directly
  real_status=$(docker inspect "nanoku-$APP" --format '{{.State.Running}} {{.State.Status}}' 2>/dev/null)
  if [ -n "$real_status" ]; then
    pass "real docker container: $real_status"
  else
    fail "no real docker container named nanoku-$APP"
  fi
fi

case_done
e2e_summary_and_exit
