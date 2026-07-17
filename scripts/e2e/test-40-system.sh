#!/usr/bin/env bash
# test-40-system.sh — /api/status, /api/system/status, /api/caddyfile, /api/dashboard, /api/system/cleanup
#
# Does not depend on caddy (read path)

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

e2e_login

# ---- /api/status singleton ---------------------------------------
section "/api/status (singleton)"

status_body=$(api_body GET /api/status)
assert_jq_exists "$status_body" '.dockerConnected' "/api/status has dockerConnected"
assert_jq_exists "$status_body" '.caddyStatus' "/api/status has caddyStatus"
assert_jq_exists "$status_body" '.appCount' "/api/status has appCount"
assert_jq_exists "$status_body" '.siteCount' "/api/status has siteCount"

# ---- /api/system/status ---------------------------------------
section "/api/system/status"

sys_body=$(api_body GET /api/system/status)
assert_jq_exists "$sys_body" '.dockerAvailable' "/api/system/status has dockerAvailable"
assert_jq_exists "$sys_body" '.version' "/api/system/status has version"
assert_jq_exists "$sys_body" '.commit' "/api/system/status has commit"

# ---- /api/dashboard ------------------------------------------
section "/api/dashboard"

dash_body=$(api_body GET /api/dashboard)
assert_jq_exists "$dash_body" '.summary.totalApps' "/api/dashboard has totalApps"
assert_jq_exists "$dash_body" '.summary.totalSites' "/api/dashboard has totalSites"
assert_jq_exists "$dash_body" '.summary.containerCount' "/api/dashboard has containerCount"

# Type should be number
type_check=$(echo "$dash_body" | jq -r '.summary.totalApps | type')
assert_eq "$type_check" "number" "totalApps is number"

# ---- /api/caddyfile (does not depend on caddy container, just rendering) ----------
section "/api/caddyfile"

caddyfile_body=$(api_body GET /api/caddyfile)
# Should at least get a response, content is a string
if [ -n "$caddyfile_body" ]; then
  pass "/api/caddyfile returns non-empty"
else
  fail "/api/caddyfile empty"
fi

# ---- /api/system/cleanup -------------------------------------
section "/api/system/cleanup"

cleanup_body=$(api_body GET /api/system/cleanup)
# tasks is an object, task name is the key
assert_jq_exists "$cleanup_body" ".tasks | length" "tasks object present"
assert_jq_exists "$cleanup_body" '.tasks."purge-expired-sessions"' "purge-expired-sessions registered"
assert_jq_exists "$cleanup_body" '.tasks."prune-old-containers"' "prune-old-containers registered"
assert_jq_exists "$cleanup_body" '.tasks."prune-orphan-log-files"' "prune-orphan-log-files registered"

# ---- /api/system/reconcile POST is admin-only entry, GET first
section "/api/system/reconcile (GET report)"

recon_body=$(api_body GET /api/system/reconcile)
assert_jq_exists "$recon_body" '.scannedDocker' "reconcile report has scannedDocker"
assert_jq_exists "$recon_body" '.scannedDbRows' "reconcile report has scannedDbRows"

case_done
e2e_summary_and_exit
