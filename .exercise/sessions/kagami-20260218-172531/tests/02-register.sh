#!/usr/bin/env bash
# Test: Machine registration endpoint
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Machine Registration ==="

TUNNEL_ID="test-reg-$(date +%s)"

# 1. Register a machine successfully
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"$TUNNEL_ID\"}")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

assert_status "POST /_kagami/register returns 201" "201" "$STATUS"
assert_json_exists "Response has machine_id" "$BODY" ".machine_id"
assert_json "Response has tunnel_id" "$BODY" ".tunnel_id" "$TUNNEL_ID"
assert_json_exists "Response has secret" "$BODY" ".secret"

# 2. Missing Authorization header → 401
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"should-fail\"}")
STATUS=$(echo "$RESP" | tail -1)
assert_status "Missing auth returns 401" "401" "$STATUS"

# 3. Invalid Authorization header → 401
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer wrong-secret" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"should-fail\"}")
STATUS=$(echo "$RESP" | tail -1)
assert_status "Invalid auth returns 401" "401" "$STATUS"

# 4. Missing tunnel_id → 400
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{}")
STATUS=$(echo "$RESP" | tail -1)
assert_status "Missing tunnel_id returns 400" "400" "$STATUS"

# 5. Empty tunnel_id → 400
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"\"}")
STATUS=$(echo "$RESP" | tail -1)
assert_status "Empty tunnel_id returns 400" "400" "$STATUS"

# 6. Duplicate tunnel_id → 409
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"$TUNNEL_ID\"}")
STATUS=$(echo "$RESP" | tail -1)
assert_status "Duplicate tunnel_id returns 409" "409" "$STATUS"

# Cleanup: get machine ID and delete it
MACHINE_ID=$(echo "$BODY" | jq -r '.machine_id')
if [ -n "$MACHINE_ID" ] && [ "$MACHINE_ID" != "null" ]; then
  curl -s -o /dev/null -X DELETE "$WORKER_URL/_kagami/machines/$MACHINE_ID" \
    -H "Authorization: Bearer $PROJECT_SECRET"
fi

summary
