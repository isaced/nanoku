#!/usr/bin/env bash
# test-20-sites.sh — Site CRUD + toggle
#
# 依赖 caddy 容器(创建/更新/删除 site 会触发 regenerateAndReload)。
# 没有 caddy 自动 SKIP。

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

if ! require_caddy; then
  exit 0
fi

# 登录拿 session
e2e_login

# ---- 创建一个上游站点(free upstream,不走 app) -----------------------
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

# ---- 列出 -----------------------------------------------------
section "list sites"

list_body=$(api_body GET /api/sites)
# 至少包含我们刚建的
assert_jq_exists "$list_body" ".[] | select(.id == $site_id)" "site appears in list"

# ---- 更新 -----------------------------------------------------
section "update site domain"

new_domain="e2e-$(date +%s)-u.test"
update_body=$(jq -nc --arg d "$new_domain" '{domain:$d}')
update_status=$(api_status PUT "/api/sites/$site_id" "$update_body")
assert_status "$update_status" 200 "update site"

# 验证更新后是新的 domain
get_body=$(api_body GET "/api/sites/$site_id" 2>/dev/null || echo "{}")
# 没 GET 单个 site 的 handler,通过 list 验证
list_body=$(api_body GET /api/sites)
assert_jq "$list_body" ".[] | select(.id == $site_id) | .domain" "$new_domain" "site domain updated"

# ---- toggle ----------------------------------------------------
section "toggle site"

toggle_status=$(api_status POST "/api/sites/$site_id/toggle")
assert_status "$toggle_status" 200 "toggle"
list_body=$(api_body GET /api/sites)
assert_jq "$list_body" ".[] | select(.id == $site_id) | .enabled" "false" "site disabled after toggle"

# 再 toggle 回来
toggle_status=$(api_status POST "/api/sites/$site_id/toggle")
assert_status "$toggle_status" 200 "toggle back"
list_body=$(api_body GET /api/sites)
assert_jq "$list_body" ".[] | select(.id == $site_id) | .enabled" "true" "site re-enabled"

# ---- 验证 caddy 收到新 Caddyfile --------------------------------
section "caddyfile reflects site"

caddyfile_body=$(api_body GET /api/caddyfile)
assert_jq_exists "$caddyfile_body" "$new_domain" "caddyfile contains site domain"
assert_jq_exists "$caddyfile_body" "$upstream" "caddyfile contains upstream"

# ---- 非法 domain ---------------------------------------------
section "invalid domain rejected"

# 缺 domain
status=$(api_status POST /api/sites "$(jq -nc --arg u "x:y" '{upstream:$u}')")
assert_status "$status" 400 "missing domain"

# 非法 name
status=$(api_status POST /api/sites "$(jq -nc '{domain:"not a domain", upstream:"x:y"}')")
assert_status "$status" 400 "invalid domain format"

# 缺 upstream 和 appId
status=$(api_status POST /api/sites "$(jq -nc '{domain:"foo.test"}')")
assert_status "$status" 400 "missing upstream/appId"

# ---- 删除 ---------------------------------------------------
section "delete site"

del_status=$(api_status DELETE "/api/sites/$site_id")
assert_status "$del_status" 204 "delete"
list_body=$(api_body GET /api/sites)
assert_jq "$list_body" "[.[] | select(.id == $site_id)] | length" "0" "site removed from list"

# 删除不存在的 id → 404
del_status=$(api_status DELETE "/api/sites/999999")
assert_status "$del_status" 404 "delete non-existent returns 404"

case_done
e2e_summary_and_exit
