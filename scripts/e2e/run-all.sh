#!/usr/bin/env bash
# scripts/e2e/run-all.sh — 串行跑所有 test-*.sh,汇总结果。
#
# 用法: ./run-all.sh [test-glob]
#   不带参数跑 test-*.sh;带参数 (如 'test-1*') 跑子集。
#
# 行为:
#   1. ./setup.sh 启 nanoku(失败立即退出)
#   2. trap teardown.sh
#   3. 跑每个 test-*.sh,统计 PASS / FAIL / SKIP 计数
#   4. 不因单 case 失败就停,跑完全部再统一退
#   5. 把所有 test 的 stdio 实时显示(失败时上下文看得见)

set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

GLOB_PATTERN="${1:-test-*.sh}"

cleanup() {
  local exit_code=$?
  bash "$SCRIPT_DIR/teardown.sh" >/dev/null 2>&1 || true
  exit $exit_code
}
trap cleanup EXIT INT TERM

# 启服务
if ! bash "$SCRIPT_DIR/setup.sh"; then
  echo -e "${RED}setup failed, aborting${NC}" >&2
  exit 1
fi

# 跑测试
shopt -s nullglob
tests=("$SCRIPT_DIR"/$GLOB_PATTERN)
shopt -u nullglob

if [ ${#tests[@]} -eq 0 ]; then
  echo -e "${YELLOW}no tests match '$GLOB_PATTERN'${NC}" >&2
  exit 1
fi

TOTAL_PASS=0
TOTAL_FAIL=0
TOTAL_SKIP=0
FAILED_CASES=()

echo
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  Running ${#tests[@]} E2E test files${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"

for t in "${tests[@]}"; do
  name=$(basename "$t")
  echo
  echo -e "${BOLD}▶ ${name}${NC}"
  start=$(date +%s)
  # 子 shell 跑,每个 case 自己的 E2E_* 计数隔离
  (
    # 重新 source 让子 shell 拿到 E2E_BASE_URL 等
    # shellcheck source=lib.sh
    source "$SCRIPT_DIR/lib.sh"
    # 注入测试名给 e2e_test_ip 用(让 IP 稳定可复现)
    export E2E_TEST_NAME="$name"
    bash "$t"
  )
  rc=$?
  elapsed=$(($(date +%s) - start))

  if [ $rc -eq 0 ]; then
    echo -e "  ${GREEN}✓ ${name} PASSED${NC} (${elapsed}s)"
    TOTAL_PASS=$((TOTAL_PASS + 1))
  else
    echo -e "  ${RED}✗ ${name} FAILED${NC} (${elapsed}s, exit=$rc)"
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
    FAILED_CASES+=("$name")
  fi
done

echo
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  E2E Summary${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
echo -e "  Test files: ${#tests[@]}"
echo -e "  ${GREEN}Passed:     $TOTAL_PASS${NC}"
echo -e "  ${RED}Failed:     $TOTAL_FAIL${NC}"
if [ ${#FAILED_CASES[@]} -gt 0 ]; then
  echo -e "  ${RED}Cases:      ${FAILED_CASES[*]}${NC}"
fi

exit $TOTAL_FAIL
