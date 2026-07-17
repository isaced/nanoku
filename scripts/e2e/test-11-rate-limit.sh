#!/usr/bin/env bash
# test-11-rate-limit.sh — /api/login 5/min per IP 限流验证
#
# 用一个全新的 IP(随机 RFC1919 地址),连发 7 次错密码,断言第 6 次开始 429。
# 依赖 setup.sh 开了 NANOKU_TRUST_PROXY=true。

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

# 随机 IP,不跟任何已有 case 撞
RANDOM_IP="192.168.$((RANDOM % 255)).$((RANDOM % 255))"

section "rate limit: 7 wrong logins from $RANDOM_IP"

: > "$E2E_COOKIE"
got_429_at=""
for i in 1 2 3 4 5 6 7; do
  s=$(api_as_ip "$RANDOM_IP" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "WRONG-$RANDOM" '{username:$u,password:$p}')" | tail -1)
  if [ "$s" = "429" ]; then
    got_429_at=$i
    break
  fi
done

if [ -n "$got_429_at" ]; then
  # 第 6 次或更后开始 429 都算通过(限流阈值是 5,前 5 次允许,第 6 次挡)
  if [ "$got_429_at" -ge 6 ]; then
    pass "rate limit triggered at attempt $got_429_at (>= 6, as expected)"
  else
    fail "rate limit triggered too early at attempt $got_429_at (expected >= 6)"
  fi
else
  fail "rate limit never triggered after 7 attempts"
fi

# 同一 IP 再试一次仍是 429
final=$(api_as_ip "$RANDOM_IP" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "WRONG" '{username:$u,password:$p}')" | tail -1)
assert_status "$final" 429 "still 429 after limit hit"

case_done
e2e_summary_and_exit
