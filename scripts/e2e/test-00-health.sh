#!/usr/bin/env bash
# test-00-health.sh — /healthz 不需要鉴权;未登录 /api/me 返回 401;CORS 预检
#
# 这是整个 E2E 的 baseline:不通过就说明服务根本没起来或路由坏了。

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

# ---- /healthz 不需要 cookie,任何状态都应 200 ---------------------------
section "/healthz bypass auth"

# 清空 cookie jar,确保真的没带
: > "$E2E_COOKIE"
status=$(api_unauthed_status GET /healthz)
assert_status "$status" 200 "/healthz (no cookie)"

# ---- /api/me 无 cookie 应当 401 ---------------------------------------
section "/api/me without session"

# 重新清空 cookie
: > "$E2E_COOKIE"
status=$(api_unauthed_status GET /api/me)
assert_status "$status" 401 "/api/me (no cookie)"

# 错误格式的 cookie 也应 401(被 SessionAuth 拒绝)
echo -e "127.0.0.1\tFALSE\t/\tFALSE\t0\tsession\tgarbage" >> "$E2E_COOKIE" 2>/dev/null || true
status=$(api_status GET /api/me)
assert_status "$status" 401 "/api/me (garbage cookie)"

# ---- CORS 预检 OPTIONS /api/login -------------------------------------
section "CORS preflight"

cors_resp=$(api_cors_preflight "http://example.com")
assert_contains "$cors_resp" "Access-Control-Allow-Origin" "CORS preflight headers"
assert_contains "$cors_resp" "204" "CORS preflight status"

# ---- 根路径(UI 兜底)返回 HTML 或 404 都不算服务挂 --------------------
section "root /"

# UI dist 在 source build 下是空的(只有 main.go 跑,没 npm build),所以 / 可能 404。
# 这里只确认:不挂、不 500、有响应。
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
