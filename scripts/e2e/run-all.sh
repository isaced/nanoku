#!/usr/bin/env bash
# scripts/e2e/run-all.sh - Run all test-*.sh serially, aggregate results.
#
# Usage: ./run-all.sh [test-glob]
#   No args runs test-*.sh; pass a glob (e.g. 'test-1*') to run a subset.
#
# Behavior:
#   1. ./setup.sh starts nanoku (aborts immediately on failure)
#   2. trap teardown.sh
#   3. Run each test-*.sh, tallying PASS / FAIL / SKIP counts
#   4. Don't stop on a single case failure; run all then exit once
#   5. Stream each test's stdio live (so failure context is visible)

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

# Start services
if ! bash "$SCRIPT_DIR/setup.sh"; then
  echo -e "${RED}setup failed, aborting${NC}" >&2
  exit 1
fi

# Run tests
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
  # Run in a subshell so each case's E2E_* counters are isolated
  (
    # Re-source so the subshell gets E2E_BASE_URL etc.
    # shellcheck source=lib.sh
    source "$SCRIPT_DIR/lib.sh"
    # Inject the test name for e2e_test_ip (keeps IPs stable and reproducible)
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
