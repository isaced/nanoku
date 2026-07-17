#!/usr/bin/env bash
# test-00-health.sh - /healthz requires no auth; unauthenticated /api/me returns 401; CORS preflight
#
# This is the baseline for the entire E2E suite: if it fails, the service is not up or routing is broken.

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

# ---- /healthz needs no cookie, should return 200 in any state ----------------
section "/healthz bypass auth"

# Clear the cookie jar to make sure none is sent
: > "$E2E_COOKIE"
status=$(api_unauthed_status GET /healthz)
assert_status "$status" 200 "/healthz (no cookie)"

# ---- /api/me without cookie should return 401 ------------------------------
section "/api/me without session"

# Clear the cookie jar again
: > "$E2E_COOKIE"
status=$(api_unauthed_status GET /api/me)
assert_status "$status" 401 "/api/me (no cookie)"

# Malformed cookie should also return 401 (rejected by SessionAuth)
echo -e "127.0.0.1\tFALSE\t/\tFALSE\t0\tsession\tgarbage" >> "$E2E_COOKIE" 2>/dev/null || true
status=$(api_status GET /api/me)
assert_status "$status" 401 "/api/me (garbage cookie)"

# ---- CORS preflight OPTIONS /api/login --------------------------------------
section "CORS preflight"

cors_resp=$(api_cors_preflight "http://example.com")
assert_contains "$cors_resp" "Access-Control-Allow-Origin" "CORS preflight headers"
assert_contains "$cors_resp" "204" "CORS preflight status"

# ---- Root path (UI fallback) returning HTML or 404 does not mean the service is down ----
section "root /"

# UI dist is empty in source build (only main.go runs, no npm build), so / may 404.
# Here we only confirm: no crash, no 500, has a response.
root_status=$(api_unauthed_status GET /)
case "$root_status" in
  2*|3*|404)
    pass "GET / returns $root_status (200/3xx/404 all OK)"
    ;;
  *)
    fail "GET / returns $root_status, expected 2xx/3xx/404"
    ;;
esac

case_done
e2e_summary_and_exit
