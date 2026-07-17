#!/usr/bin/env bash
# test-10-auth.sh — full login/logout/change-password/me flow
#
# Use X-Forwarded-For to assign a distinct IP to each phase (setup.sh enables trust_proxy),
# so /api/login rate limiting (5/min per IP) does not pollute subsequent assertions within the same case.
#
# Covers:
#   - wrong password -> 401
#   - correct password -> 200 + Set-Cookie
#   - call /api/me with the cookie
#   - change password + new password works
#   - after logout, /api/me -> 401

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

# Short random suffix so it does not collide across test runs
SUFFIX=$$

# Wrong password (distinct IP) -----------------------------------------------
section "login: wrong password (IP=10.0.0.$SUFFIX)"

: > "$E2E_COOKIE"
status=$(api_as_ip "10.0.0.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "WRONG-PASSWORD" '{username:$u,password:$p}')" | tail -1)
assert_status "$status" 401 "wrong password"

# Correct password (distinct IP) --------------------------------------------
section "login: correct password (IP=10.0.1.$SUFFIX)"

: > "$E2E_COOKIE"
login_resp=$(api_as_ip "10.0.1.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')")
login_status=$(echo "$login_resp" | tail -1)
login_body=$(echo "$login_resp" | sed '$d')

assert_status "$login_status" 200 "login with correct password"
assert_jq "$login_body" '.username' "$E2E_ADMIN_USER" "login response username"

# Cookie should land in the jar
if grep -qE 'session' "$E2E_COOKIE" 2>/dev/null; then
  pass "session cookie set in jar"
else
  fail "no session cookie in jar"
  echo "  cookie jar: $E2E_COOKIE"
  cat "$E2E_COOKIE" 2>/dev/null
fi

# /api/me --------------------------------------------------------------
section "/api/me with session (IP=10.0.1.$SUFFIX)"

# /api/me via the normal api() uses the default IP. To actually send the cookie,
# just use curl with the cookie (me is not rate-limited)
me_status=$(api_status GET /api/me)
assert_status "$me_status" 200 "/api/me authenticated"
me_body=$(api_body GET /api/me)
assert_jq "$me_body" '.username' "$E2E_ADMIN_USER" "/api/me username"
assert_jq "$me_body" '.user' "$E2E_ADMIN_USER" "/api/me user"
assert_jq_exists "$me_body" '.role' "/api/me has role"

# Change password (distinct IP) ---------------------------------------------
section "change password (IP=10.0.2.$SUFFIX)"

# Re-login with a new IP first to get a fresh cookie (different IP maps to a different session/cookie)
: > "$E2E_COOKIE"
api_as_ip "10.0.2.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')" >/dev/null

new_password="e2e-new-pass-$SUFFIX-$(date +%s)"
change_body=$(jq -nc --arg o "$E2E_ADMIN_PASSWORD" --arg n "$new_password" '{oldPassword:$o, newPassword:$n}')

change_resp=$(api POST /api/me/password "$change_body")
change_status=$(echo "$change_resp" | tail -1)
change_body_resp=$(echo "$change_resp" | sed '$d')
# Change password returns 204 No Content. It also drops all sessions for that user (test a side effect: old cookie invalidated)
assert_status "$change_status" 204 "change password returns 204"

# The old cookie should be invalidated (the server deleted all sessions for that user, including the one we just used)
me_after_change=$(api_status GET /api/me)
assert_status "$me_after_change" 401 "/api/me after change password (session wiped)"

# Login with the new password (distinct IP)
: > "$E2E_COOKIE"
new_login_status=$(api_as_ip "10.0.3.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$new_password" '{username:$u,password:$p}')" | tail -1)
assert_status "$new_login_status" 200 "login with new password"

# Revert to the original password (distinct IP)
: > "$E2E_COOKIE"
api_as_ip "10.0.4.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$new_password" '{username:$u,password:$p}')" >/dev/null
revert_body=$(jq -nc --arg o "$new_password" --arg n "$E2E_ADMIN_PASSWORD" '{oldPassword:$o, newPassword:$n}')
revert_status=$(api_status POST /api/me/password "$revert_body")
assert_status "$revert_status" 204 "revert password"

# Logout ---------------------------------------------------------------------
section "logout (IP=10.0.5.$SUFFIX)"

: > "$E2E_COOKIE"
api_as_ip "10.0.5.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')" >/dev/null

logout_status=$(api_status POST /api/logout)
assert_status "$logout_status" 204 "logout"

# After logout, /api/me should return 401
me_after=$(api_status GET /api/me)
assert_status "$me_after" 401 "/api/me after logout"

case_done
e2e_summary_and_exit
