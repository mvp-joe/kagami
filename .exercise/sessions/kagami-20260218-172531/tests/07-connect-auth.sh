#!/usr/bin/env bash
# Test: WebSocket connect endpoint auth validation
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Connect Endpoint Auth ==="

# 1. Missing headers → 400
RESP=$(curl -s -w "\n%{http_code}" "$WORKER_URL/_kagami/connect")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

assert_status "Connect without headers returns 400" "400" "$STATUS"

# 2. Missing X-Kagami-Tunnel-ID → 400
RESP=$(curl -s -w "\n%{http_code}" "$WORKER_URL/_kagami/connect" \
  -H "Upgrade: websocket" \
  -H "X-Kagami-Secret: fake-secret")
STATUS=$(echo "$RESP" | tail -1)

# Should be 400 for missing required headers
if [ "$STATUS" = "400" ] || [ "$STATUS" = "401" ]; then
  pass "Connect with missing Tunnel-ID returns 400 or 401 (got $STATUS)"
else
  fail "Connect with missing Tunnel-ID returns 400 or 401" "400 or 401" "$STATUS"
fi

# 3. Invalid secret → 401
RESP=$(curl -s -w "\n%{http_code}" "$WORKER_URL/_kagami/connect" \
  -H "Upgrade: websocket" \
  -H "Connection: Upgrade" \
  -H "X-Kagami-Tunnel-ID: fake-tunnel" \
  -H "X-Kagami-Secret: wrong-secret")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

assert_status "Connect with invalid secret returns 401" "401" "$STATUS"
assert_json "401 error field" "$BODY" ".error" "unauthorized"

summary
