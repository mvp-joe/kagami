#!/usr/bin/env bash
# Test: Registration edge cases
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Registration Edge Cases ==="

cleanup_ids=()
cleanup() {
  for id in "${cleanup_ids[@]}"; do
    curl -s -o /dev/null -X DELETE "$WORKER_URL/_kagami/machines/$id" \
      -H "Authorization: Bearer $PROJECT_SECRET"
  done
}
trap cleanup EXIT

# 1. Register with optional fields (hostname, os)
TUNNEL_ID="edge-opt-$(date +%s)"
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"$TUNNEL_ID\",\"hostname\":\"my-server\",\"os\":\"linux\"}")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "Register with optional fields returns 201" "201" "$STATUS"
MID=$(echo "$BODY" | jq -r '.machine_id')
cleanup_ids+=("$MID")

# Verify optional fields in machine list
LIST=$(curl -s "$WORKER_URL/_kagami/machines" \
  -H "Authorization: Bearer $PROJECT_SECRET")
HOST_VAL=$(echo "$LIST" | jq -r ".machines[] | select(.id == \"$MID\") | .hostname")
OS_VAL=$(echo "$LIST" | jq -r ".machines[] | select(.id == \"$MID\") | .os")
if [ "$HOST_VAL" = "my-server" ]; then pass "hostname field stored"; else fail "hostname field stored" "my-server" "$HOST_VAL"; fi
if [ "$OS_VAL" = "linux" ]; then pass "os field stored"; else fail "os field stored" "linux" "$OS_VAL"; fi

# 2. Register without optional fields
TUNNEL_ID2="edge-min-$(date +%s)"
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"$TUNNEL_ID2\"}")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "Register without optional fields returns 201" "201" "$STATUS"
MID2=$(echo "$BODY" | jq -r '.machine_id')
cleanup_ids+=("$MID2")

# 3. Secret format check - should start with kgm_mach_
SECRET=$(echo "$BODY" | jq -r '.secret')
if echo "$SECRET" | grep -q "^kgm_mach_"; then
  pass "Machine secret starts with kgm_mach_ prefix"
else
  fail "Machine secret starts with kgm_mach_ prefix" "kgm_mach_*" "$SECRET"
fi

# 4. Machine ID format - should be UUID
if echo "$MID2" | grep -qP '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'; then
  pass "Machine ID is valid UUID format"
else
  fail "Machine ID is valid UUID format" "UUID" "$MID2"
fi

# 5. tunnel_id with special characters (hyphens, dots)
TUNNEL_ID3="my-machine.test-$(date +%s)"
RESP=$(curl -s -w "\n%{http_code}" -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"$TUNNEL_ID3\"}")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "Register tunnel_id with dots and hyphens returns 201" "201" "$STATUS"
MID3=$(echo "$BODY" | jq -r '.machine_id')
cleanup_ids+=("$MID3")

# 6. registered_at field should be present in list
LIST=$(curl -s "$WORKER_URL/_kagami/machines" \
  -H "Authorization: Bearer $PROJECT_SECRET")
REG_AT=$(echo "$LIST" | jq -r ".machines[] | select(.id == \"$MID\") | .registered_at")
if [ -n "$REG_AT" ] && [ "$REG_AT" != "null" ]; then
  pass "Machine has registered_at timestamp"
else
  fail "Machine has registered_at timestamp" "non-null" "$REG_AT"
fi

summary
