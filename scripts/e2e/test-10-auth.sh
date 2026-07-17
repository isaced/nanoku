#!/usr/bin/env bash
# test-10-auth.sh — 登录/登出/改密/me 完整链路
#
# 用 X-Forwarded-For 给每个 phase 分配独立 IP(setup.sh 开了 trust_proxy),
# 避免 /api/login 限流(5/min per IP)污染同 case 内的后续断言。
#
# 覆盖:
#   - 错密码 401
#   - 对密码 200 + Set-Cookie
#   - 拿 cookie 调 /api/me
#   - 改密 + 新密码能登
#   - 登出后 /api/me 401

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

# 短随机后缀,跨 test run 也不撞
SUFFIX=$$

# 错密码(用独立 IP) ----------------------------------------------------
section "login: wrong password (IP=10.0.0.$SUFFIX)"

: > "$E2E_COOKIE"
status=$(api_as_ip "10.0.0.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "WRONG-PASSWORD" '{username:$u,password:$p}')" | tail -1)
assert_status "$status" 401 "wrong password"

# 对密码(独立 IP) -----------------------------------------------------
section "login: correct password (IP=10.0.1.$SUFFIX)"

: > "$E2E_COOKIE"
login_resp=$(api_as_ip "10.0.1.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')")
login_status=$(echo "$login_resp" | tail -1)
login_body=$(echo "$login_resp" | sed '$d')

assert_status "$login_status" 200 "login with correct password"
assert_jq "$login_body" '.username' "$E2E_ADMIN_USER" "login response username"

# cookie 应该落到 jar 里
if grep -qE 'session' "$E2E_COOKIE" 2>/dev/null; then
  pass "session cookie set in jar"
else
  fail "no session cookie in jar"
  echo "  cookie jar: $E2E_COOKIE"
  cat "$E2E_COOKIE" 2>/dev/null
fi

# /api/me --------------------------------------------------------------
section "/api/me with session (IP=10.0.1.$SUFFIX)"

# /api/me 走普通 api() 走的是默认 IP。要让 cookie 真的被带上,直接用带 cookie
# 的 curl 即可(me 不限流)
me_status=$(api_status GET /api/me)
assert_status "$me_status" 200 "/api/me authenticated"
me_body=$(api_body GET /api/me)
assert_jq "$me_body" '.username' "$E2E_ADMIN_USER" "/api/me username"
assert_jq "$me_body" '.user' "$E2E_ADMIN_USER" "/api/me user"
assert_jq_exists "$me_body" '.role' "/api/me has role"

# 改密码(独立 IP) -----------------------------------------------------
section "change password (IP=10.0.2.$SUFFIX)"

# 先用新 IP 重新登一下,拿到新 cookie(因为不同 IP 对应不同 session/cookie)
: > "$E2E_COOKIE"
api_as_ip "10.0.2.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')" >/dev/null

new_password="e2e-new-pass-$SUFFIX-$(date +%s)"
change_body=$(jq -nc --arg o "$E2E_ADMIN_PASSWORD" --arg n "$new_password" '{oldPassword:$o, newPassword:$n}')

change_resp=$(api POST /api/me/password "$change_body")
change_status=$(echo "$change_resp" | tail -1)
change_body_resp=$(echo "$change_resp" | sed '$d')
# 改密返回 204 No Content。同时会 drop 该用户所有 session(测一个副作用:旧 cookie 失效)
assert_status "$change_status" 204 "change password returns 204"

# 旧 cookie 应该失效(因为服务端把该用户所有 session 都删了,包括我们刚用的)
me_after_change=$(api_status GET /api/me)
assert_status "$me_after_change" 401 "/api/me after change password (session wiped)"

# 用新密码登(独立 IP)
: > "$E2E_COOKIE"
new_login_status=$(api_as_ip "10.0.3.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$new_password" '{username:$u,password:$p}')" | tail -1)
assert_status "$new_login_status" 200 "login with new password"

# 改回原密码(独立 IP)
: > "$E2E_COOKIE"
api_as_ip "10.0.4.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$new_password" '{username:$u,password:$p}')" >/dev/null
revert_body=$(jq -nc --arg o "$new_password" --arg n "$E2E_ADMIN_PASSWORD" '{oldPassword:$o, newPassword:$n}')
revert_status=$(api_status POST /api/me/password "$revert_body")
assert_status "$revert_status" 204 "revert password"

# 登出 ---------------------------------------------------------------
section "logout (IP=10.0.5.$SUFFIX)"

: > "$E2E_COOKIE"
api_as_ip "10.0.5.$SUFFIX" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "$E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')" >/dev/null

logout_status=$(api_status POST /api/logout)
assert_status "$logout_status" 204 "logout"

# 登出后 /api/me 应 401
me_after=$(api_status GET /api/me)
assert_status "$me_after" 401 "/api/me after logout"

case_done
e2e_summary_and_exit
