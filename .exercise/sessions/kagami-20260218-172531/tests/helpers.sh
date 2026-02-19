#!/usr/bin/env bash
# Shared test helpers

WORKER_URL="http://localhost:8787"
PROJECT_SECRET="kgm_proj_e03ff406312bf59f8939d1c04382fa98f5bb6036ec28fe74542c095906771064"
BASE_DOMAIN="tunnel.local"
KAGAMI="$(cd "$(dirname "$0")/.." && pwd)/kagami"
SCRATCH="$(cd "$(dirname "$0")/.." && pwd)/scratch"

PASS_COUNT=0
FAIL_COUNT=0
FAILURES=""

pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  echo "  PASS: $1"
}

fail() {
  FAIL_COUNT=$((FAIL_COUNT + 1))
  FAILURES="${FAILURES}\n  FAIL: $1"
  echo "  FAIL: $1"
  if [ -n "$2" ]; then
    echo "    Expected: $2"
  fi
  if [ -n "$3" ]; then
    echo "    Actual:   $3"
  fi
}

summary() {
  echo ""
  echo "=== Results: $PASS_COUNT passed, $FAIL_COUNT failed ==="
  if [ $FAIL_COUNT -gt 0 ]; then
    echo -e "Failures:$FAILURES"
    return 1
  fi
  return 0
}

# Assert HTTP status code
assert_status() {
  local desc="$1" expected="$2" actual="$3"
  if [ "$actual" = "$expected" ]; then
    pass "$desc"
  else
    fail "$desc" "HTTP $expected" "HTTP $actual"
  fi
}

# Assert JSON field value
assert_json() {
  local desc="$1" body="$2" jq_expr="$3" expected="$4"
  local actual
  actual=$(echo "$body" | jq -r "$jq_expr" 2>/dev/null)
  if [ "$actual" = "$expected" ]; then
    pass "$desc"
  else
    fail "$desc" "$expected" "$actual (body: $body)"
  fi
}

# Assert JSON field exists (non-null, non-empty)
assert_json_exists() {
  local desc="$1" body="$2" jq_expr="$3"
  local actual
  actual=$(echo "$body" | jq -r "$jq_expr" 2>/dev/null)
  if [ -n "$actual" ] && [ "$actual" != "null" ]; then
    pass "$desc"
  else
    fail "$desc" "non-empty value" "$actual (body: $body)"
  fi
}
