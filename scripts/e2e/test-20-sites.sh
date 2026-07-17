#!/usr/bin/env bash
# test-20-sites.sh — Site CRUD + toggle
#
# Depends on the caddy container (create/update/delete site triggers regenerateAndReload).
# If caddy fails to start, hard fail so the user can see which endpoint is down.

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

# Log in to get a session
e2e_login

# ---- Create a free-upstream site (no app) -----------------------
section "create free-upstream site"

domain="e2e-$(date +%s).test"
upstream="127.0.0.1:9999"
create_body=$(jq -nc --arg d "$domain" --arg u "$upstream" '{domain:$d, upstream:$u, scheme:"http"}')
create_resp=$(api POST /api/sites "$create_body")
create_status=$(echo "$create_resp" | tail -1)
create_body_resp=$(echo "$create_resp" | sed '$d')

assert_status "$create_status" 201 "create site"
site_id=$(echo "$create_body_resp" | jq -r '.id')
assert_jq "$create_body_resp" '.domain' "$domain" "site domain"
assert_jq "$create_body_resp" '.upstream' "$upstream" "site upstream"
assert_jq "$create_body_resp" '.enabled' "true" "site default enabled"
assert_jq "$create_body_resp" '.scheme' "http" "site scheme"

# ---- List -----------------------------------------------------
section "list sites"

list_body=$(api_body GET /api/sites)
# At least contains the one we just created
assert_jq_exists "$list_body" ".[] | select(.id == $site_id)" "site appears in list"

# ---- Update -----------------------------------------------------
section "update site domain"

new_domain="e2e-$(date +%s)-u.test"
update_body=$(jq -nc --arg d "$new_domain" '{domain:$d}')
update_status=$(api_status PUT "/api/sites/$site_id" "$update_body")
assert_status "$update_status" 200 "update site"

# Verify the new domain after update
get_body=$(api_body GET "/api/sites/$site_id" 2>/dev/null || echo "{}")
# No GET handler for a single site, verify via list
list_body=$(api_body GET /api/sites)
assert_jq "$list_body" ".[] | select(.id == $site_id) | .domain" "$new_domain" "site domain updated"

# ---- toggle ----------------------------------------------------
section "toggle site"

toggle_status=$(api_status POST "/api/sites/$site_id/toggle")
assert_status "$toggle_status" 200 "toggle"
list_body=$(api_body GET /api/sites)
assert_jq "$list_body" ".[] | select(.id == $site_id) | .enabled" "false" "site disabled after toggle"

# Toggle back
toggle_status=$(api_status POST "/api/sites/$site_id/toggle")
assert_status "$toggle_status" 200 "toggle back"
list_body=$(api_body GET /api/sites)
assert_jq "$list_body" ".[] | select(.id == $site_id) | .enabled" "true" "site re-enabled"

# ---- Verify caddy received the new Caddyfile --------------------------------
section "caddyfile reflects site"

# /api/caddyfile returns text/plain (not JSON), use assert_contains instead of
# assert_jq_exists (jq parsing a non-JSON body fails and falsely reports missing).
caddyfile_body=$(api_body GET /api/caddyfile)
assert_contains "$caddyfile_body" "$new_domain" "caddyfile contains site domain"
assert_contains "$caddyfile_body" "$upstream" "caddyfile contains upstream"

# ---- Invalid domain ---------------------------------------------
section "invalid domain rejected"

# Missing domain
status=$(api_status POST /api/sites "$(jq -nc --arg u "x:y" '{upstream:$u}')")
assert_status "$status" 400 "missing domain"

# Invalid name
status=$(api_status POST /api/sites "$(jq -nc '{domain:"not a domain", upstream:"x:y"}')")
assert_status "$status" 400 "invalid domain format"

# Missing upstream and appId
status=$(api_status POST /api/sites "$(jq -nc '{domain:"foo.test"}')")
assert_status "$status" 400 "missing upstream/appId"

# ---- Delete ---------------------------------------------------
section "delete site"

del_status=$(api_status DELETE "/api/sites/$site_id")
assert_status "$del_status" 204 "delete"
list_body=$(api_body GET /api/sites)
assert_jq "$list_body" "[.[] | select(.id == $site_id)] | length" "0" "site removed from list"

# Delete non-existent id -> 404
del_status=$(api_status DELETE "/api/sites/999999")
assert_status "$del_status" 404 "delete non-existent returns 404"

case_done
e2e_summary_and_exit
