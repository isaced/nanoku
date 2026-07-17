#!/usr/bin/env bash
# test-11-rate-limit.sh — verify /api/login rate limit of 5/min per IP
#
# Use a brand-new IP (random RFC1919 address) and send 7 wrong-password requests in a row,
# asserting that 429 starts from the 6th attempt.
# Depends on setup.sh having NANOKU_TRUST_PROXY=true.

set -u
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

case_start

# Random IP so it does not collide with any existing case
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
  # 429 starting at the 6th attempt or later counts as a pass (threshold is 5: the first 5 are allowed, the 6th is blocked)
  if [ "$got_429_at" -ge 6 ]; then
    pass "rate limit triggered at attempt $got_429_at (>= 6, as expected)"
  else
    fail "rate limit triggered too early at attempt $got_429_at (expected >= 6)"
  fi
else
  fail "rate limit never triggered after 7 attempts"
fi

# Retrying once more with the same IP should still be 429
final=$(api_as_ip "$RANDOM_IP" POST /api/login "$(jq -nc --arg u "$E2E_ADMIN_USER" --arg p "WRONG" '{username:$u,password:$p}')" | tail -1)
assert_status "$final" 429 "still 429 after limit hit"

case_done
e2e_summary_and_exit
