#!/usr/bin/env bash
# Test: Machine management (list and delete)
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Machine Management ==="

TUNNEL_ID="test-mgmt-$(date +%s)"

# Register a machine first
REG_RESP=$(curl -s -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"$TUNNEL_ID\",\"hostname\":\"test-host\",\"os\":\"linux\"}")
MACHINE_ID=$(echo "$REG_RESP" | jq -r '.machine_id')

# 1. List machines - requires auth
RESP=$(curl -s -w "\n%{http_code}" "$WORKER_URL/_kagami/machines")
STATUS=$(echo "$RESP" | tail -1)
assert_status "GET /_kagami/machines without auth returns 401" "401" "$STATUS"

# 2. List machines with auth
RESP=$(curl -s -w "\n%{http_code}" "$WORKER_URL/_kagami/machines" \
  -H "Authorization: Bearer $PROJECT_SECRET")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

assert_status "GET /_kagami/machines returns 200" "200" "$STATUS"

# Check the machine we registered is in the list
FOUND=$(echo "$BODY" | jq -r ".machines[] | select(.id == \"$MACHINE_ID\") | .tunnel_id")
if [ "$FOUND" = "$TUNNEL_ID" ]; then
  pass "Registered machine appears in list"
else
  fail "Registered machine appears in list" "$TUNNEL_ID" "$FOUND (body: $BODY)"
fi

# Check optional fields are stored
FOUND_HOST=$(echo "$BODY" | jq -r ".machines[] | select(.id == \"$MACHINE_ID\") | .hostname")
assert_json_exists "Machine has hostname" "$REG_RESP" ".machine_id"
FOUND_OS=$(echo "$BODY" | jq -r ".machines[] | select(.id == \"$MACHINE_ID\") | .os")
if [ "$FOUND_HOST" = "test-host" ]; then
  pass "Hostname stored correctly"
else
  fail "Hostname stored correctly" "test-host" "$FOUND_HOST"
fi
if [ "$FOUND_OS" = "linux" ]; then
  pass "OS stored correctly"
else
  fail "OS stored correctly" "linux" "$FOUND_OS"
fi

# 3. Delete machine - requires auth
RESP=$(curl -s -w "\n%{http_code}" -X DELETE "$WORKER_URL/_kagami/machines/$MACHINE_ID")
STATUS=$(echo "$RESP" | tail -1)
assert_status "DELETE without auth returns 401" "401" "$STATUS"

# 4. Delete non-existent machine → 404
RESP=$(curl -s -w "\n%{http_code}" -X DELETE "$WORKER_URL/_kagami/machines/non-existent-id" \
  -H "Authorization: Bearer $PROJECT_SECRET")
STATUS=$(echo "$RESP" | tail -1)
assert_status "DELETE non-existent machine returns 404" "404" "$STATUS"

# 5. Delete machine successfully → 204
RESP=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "$WORKER_URL/_kagami/machines/$MACHINE_ID" \
  -H "Authorization: Bearer $PROJECT_SECRET")
assert_status "DELETE existing machine returns 204" "204" "$RESP"

# 6. Verify machine is gone from list
RESP=$(curl -s "$WORKER_URL/_kagami/machines" \
  -H "Authorization: Bearer $PROJECT_SECRET")
FOUND=$(echo "$RESP" | jq -r ".machines[] | select(.id == \"$MACHINE_ID\") | .id")
if [ -z "$FOUND" ]; then
  pass "Deleted machine no longer in list"
else
  fail "Deleted machine no longer in list" "empty" "$FOUND"
fi

# 7. Can re-register after delete
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"$TUNNEL_ID\"}")
STATUS=$(echo "$RESP" | tail -1)
assert_status "Re-register after delete returns 201" "201" "$STATUS"

# Cleanup
NEW_MACHINE_ID=$(echo "$RESP" | sed '$d' | jq -r '.machine_id')
curl -s -o /dev/null -X DELETE "$WORKER_URL/_kagami/machines/$NEW_MACHINE_ID" \
  -H "Authorization: Bearer $PROJECT_SECRET"

summary
