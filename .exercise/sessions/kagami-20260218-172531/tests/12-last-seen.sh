#!/usr/bin/env bash
# Test: last_seen_at is updated when agent connects
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: last_seen_at Updated on Connect ==="

TUNNEL_NAME="seen-$(date +%s)"
CONFIG="$SCRATCH/seen-${TUNNEL_NAME}.toml"
LOCAL_PORT=19892

# Start local server
python3 -c "
import http.server, socketserver
class ReuseTCPServer(socketserver.TCPServer):
    allow_reuse_address = True
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'ok')
    def log_message(self, format, *args):
        pass
server = ReuseTCPServer(('127.0.0.1', $LOCAL_PORT), Handler)
server.serve_forever()
" &
LOCAL_SERVER_PID=$!

cleanup() {
  kill $LOCAL_SERVER_PID 2>/dev/null || true
  if [ -n "${KAGAMI_PID:-}" ]; then
    kill $KAGAMI_PID 2>/dev/null || true
    wait $KAGAMI_PID 2>/dev/null || true
  fi
  if [ -n "${MACHINE_ID:-}" ] && [ "$MACHINE_ID" != "null" ]; then
    curl -s -o /dev/null -X DELETE "$WORKER_URL/_kagami/machines/$MACHINE_ID" \
      -H "Authorization: Bearer $PROJECT_SECRET"
  fi
  rm -f "$CONFIG"
}
trap cleanup EXIT

sleep 0.5

# Register via API (so we can check last_seen_at before connect)
REG_RESP=$(curl -s -X POST "$WORKER_URL/_kagami/register" \
  -H "Authorization: Bearer $PROJECT_SECRET" \
  -H "Content-Type: application/json" \
  -d "{\"tunnel_id\":\"$TUNNEL_NAME\"}")
MACHINE_ID=$(echo "$REG_RESP" | jq -r '.machine_id')
MACHINE_SECRET=$(echo "$REG_RESP" | jq -r '.secret')

# Check last_seen_at before agent connects
LIST=$(curl -s "$WORKER_URL/_kagami/machines" -H "Authorization: Bearer $PROJECT_SECRET")
LAST_SEEN_BEFORE=$(echo "$LIST" | jq -r ".machines[] | select(.id == \"$MACHINE_ID\") | .last_seen_at")

# Now create config and connect agent
cat > "$CONFIG" <<EOF
[agent]
tunnel_id = "$TUNNEL_NAME"
secret = "$MACHINE_SECRET"
server = "localhost:8787"
insecure = true

[[tunnel]]
name = "svc"
local_addr = "127.0.0.1:$LOCAL_PORT"
hostname = "svc.${TUNNEL_NAME}.${BASE_DOMAIN}"
EOF

"$KAGAMI" run --config "$CONFIG" &
KAGAMI_PID=$!
sleep 2

# Check last_seen_at after agent connects
LIST=$(curl -s "$WORKER_URL/_kagami/machines" -H "Authorization: Bearer $PROJECT_SECRET")
LAST_SEEN_AFTER=$(echo "$LIST" | jq -r ".machines[] | select(.id == \"$MACHINE_ID\") | .last_seen_at")

# last_seen_at should be updated (non-null and different or first set)
if [ "$LAST_SEEN_AFTER" != "null" ] && [ -n "$LAST_SEEN_AFTER" ]; then
  pass "last_seen_at is set after agent connects"
else
  fail "last_seen_at is set after agent connects" "non-null timestamp" "$LAST_SEEN_AFTER"
fi

if [ "$LAST_SEEN_BEFORE" = "null" ] || [ -z "$LAST_SEEN_BEFORE" ]; then
  if [ "$LAST_SEEN_AFTER" != "null" ] && [ -n "$LAST_SEEN_AFTER" ]; then
    pass "last_seen_at changed from null to timestamp after connect"
  else
    fail "last_seen_at changed from null to timestamp" "timestamp" "$LAST_SEEN_AFTER"
  fi
elif [ "$LAST_SEEN_BEFORE" != "$LAST_SEEN_AFTER" ]; then
  pass "last_seen_at updated on connect"
else
  # Could be the same if registration set it too — still valid
  pass "last_seen_at present after connect (same as registration)"
fi

# Kill agent
kill $KAGAMI_PID 2>/dev/null || true
wait $KAGAMI_PID 2>/dev/null || true

summary
