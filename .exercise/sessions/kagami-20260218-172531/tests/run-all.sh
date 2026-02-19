#!/usr/bin/env bash
# Run all tests and summarize
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TOTAL_PASS=0
TOTAL_FAIL=0
FAILED_TESTS=""

for test in "$SCRIPT_DIR"/[0-9]*.sh; do
  echo ""
  echo "========================================"
  echo "Running: $(basename "$test")"
  echo "========================================"
  OUTPUT=$(bash "$test" 2>&1)
  EXIT_CODE=$?
  echo "$OUTPUT"

  # Extract counts from output
  PASS=$(echo "$OUTPUT" | grep -oP '\d+ passed' | grep -oP '^\d+' || echo "0")
  FAILS=$(echo "$OUTPUT" | grep -oP '\d+ failed' | grep -oP '^\d+' || echo "0")

  TOTAL_PASS=$((TOTAL_PASS + PASS))
  TOTAL_FAIL=$((TOTAL_FAIL + FAILS))

  if [ "$EXIT_CODE" -ne 0 ] || [ "$FAILS" -gt 0 ]; then
    FAILED_TESTS="${FAILED_TESTS}\n  - $(basename "$test")"
  fi
done

echo ""
echo "========================================"
echo "GRAND TOTAL: $TOTAL_PASS passed, $TOTAL_FAIL failed"
echo "========================================"

if [ $TOTAL_FAIL -gt 0 ]; then
  echo -e "Failed test files:$FAILED_TESTS"
  exit 1
fi
exit 0
