#!/usr/bin/env bash
# Test: kagami CLI (init, tunnel add/list/remove)
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Kagami CLI ==="

CONFIG="$SCRATCH/cli-test-$(date +%s).toml"
TUNNEL_NAME="cli-test-$(date +%s)"

# 1. kagami init - register with worker
printf 'http://localhost:8787\n%s\n%s\n' "$PROJECT_SECRET" "$TUNNEL_NAME" | \
  "$KAGAMI" init --config "$CONFIG" 2>&1
if [ -f "$CONFIG" ]; then
  pass "kagami init creates config file"
else
  fail "kagami init creates config file" "file exists" "file missing"
fi

# Verify config contents
if [ -f "$CONFIG" ]; then
  CONF_TUNNEL_ID=$(grep -oP 'tunnel_id\s*=\s*"\K[^"]+' "$CONFIG" 2>/dev/null || echo "")
  if [ "$CONF_TUNNEL_ID" = "$TUNNEL_NAME" ]; then
    pass "Config has correct tunnel_id"
  else
    fail "Config has correct tunnel_id" "$TUNNEL_NAME" "$CONF_TUNNEL_ID"
  fi

  CONF_SERVER=$(grep -oP 'server\s*=\s*"\K[^"]+' "$CONFIG" 2>/dev/null || echo "")
  if [ -n "$CONF_SERVER" ]; then
    pass "Config has server URL"
  else
    fail "Config has server URL" "non-empty" "$CONF_SERVER"
  fi

  CONF_SECRET=$(grep -oP 'secret\s*=\s*"\K[^"]+' "$CONFIG" 2>/dev/null || echo "")
  if [ -n "$CONF_SECRET" ]; then
    pass "Config has machine secret"
  else
    fail "Config has machine secret" "non-empty" "$CONF_SECRET"
  fi

  CONF_INSECURE=$(grep -oP 'insecure\s*=\s*\K\w+' "$CONFIG" 2>/dev/null || echo "")
  if [ "$CONF_INSECURE" = "true" ]; then
    pass "Config has insecure = true for localhost"
  else
    fail "Config has insecure = true for localhost" "true" "$CONF_INSECURE"
  fi
fi

# 2. kagami tunnel add
"$KAGAMI" tunnel add --config "$CONFIG" \
  --name api --local-addr localhost:9000 \
  --hostname "api.${TUNNEL_NAME}.${BASE_DOMAIN}" 2>&1
TUNNEL_LIST=$("$KAGAMI" tunnel list --config "$CONFIG" 2>&1)
if echo "$TUNNEL_LIST" | grep -q "api"; then
  pass "tunnel add + list shows the tunnel"
else
  fail "tunnel add + list shows the tunnel" "contains 'api'" "$TUNNEL_LIST"
fi

# 3. kagami tunnel add another
"$KAGAMI" tunnel add --config "$CONFIG" \
  --name web --local-addr localhost:9001 \
  --hostname "web.${TUNNEL_NAME}.${BASE_DOMAIN}" 2>&1
TUNNEL_LIST=$("$KAGAMI" tunnel list --config "$CONFIG" 2>&1)
if echo "$TUNNEL_LIST" | grep -q "web"; then
  pass "Second tunnel appears in list"
else
  fail "Second tunnel appears in list" "contains 'web'" "$TUNNEL_LIST"
fi

# 4. kagami tunnel remove
"$KAGAMI" tunnel remove web --config "$CONFIG" 2>&1
TUNNEL_LIST=$("$KAGAMI" tunnel list --config "$CONFIG" 2>&1)
if echo "$TUNNEL_LIST" | grep -q "web"; then
  fail "Removed tunnel no longer in list" "not contains 'web'" "$TUNNEL_LIST"
else
  pass "Removed tunnel no longer in list"
fi

# Cleanup: delete the machine from worker
MACHINES_RESP=$(curl -s "$WORKER_URL/_kagami/machines" \
  -H "Authorization: Bearer $PROJECT_SECRET")
MACHINE_ID=$(echo "$MACHINES_RESP" | jq -r ".machines[] | select(.tunnel_id == \"$TUNNEL_NAME\") | .id")
if [ -n "$MACHINE_ID" ] && [ "$MACHINE_ID" != "null" ]; then
  curl -s -o /dev/null -X DELETE "$WORKER_URL/_kagami/machines/$MACHINE_ID" \
    -H "Authorization: Bearer $PROJECT_SECRET"
fi
rm -f "$CONFIG"

summary
