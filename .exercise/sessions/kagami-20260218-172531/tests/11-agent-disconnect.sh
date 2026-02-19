#!/usr/bin/env bash
# Test: Agent disconnect → 502 for subsequent requests
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Agent Disconnect Behavior ==="

TUNNEL_NAME="disc-$(date +%s)"
CONFIG="$SCRATCH/disc-${TUNNEL_NAME}.toml"
LOCAL_PORT=19891

# Start local server
python3 -c "
import http.server, socketserver, json

class ReuseTCPServer(socketserver.TCPServer):
    allow_reuse_address = True

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain')
        self.end_headers()
        self.wfile.write(b'alive')
    def log_message(self, format, *args):
        pass

server = ReuseTCPServer(('127.0.0.1', $LOCAL_PORT), Handler)
server.serve_forever()
" &
LOCAL_SERVER_PID=$!

cleanup() {
  kill $LOCAL_SERVER_PID 2>/dev/null || true
  if [ -n "${MACHINE_ID:-}" ] && [ "$MACHINE_ID" != "null" ]; then
    curl -s -o /dev/null -X DELETE "$WORKER_URL/_kagami/machines/$MACHINE_ID" \
      -H "Authorization: Bearer $PROJECT_SECRET"
  fi
  rm -f "$CONFIG"
}
trap cleanup EXIT

sleep 0.5

# Register and start agent
printf 'http://localhost:8787\n%s\n%s\n' "$PROJECT_SECRET" "$TUNNEL_NAME" | \
  "$KAGAMI" init --config "$CONFIG" 2>&1

MACHINES_RESP=$(curl -s "$WORKER_URL/_kagami/machines" -H "Authorization: Bearer $PROJECT_SECRET")
MACHINE_ID=$(echo "$MACHINES_RESP" | jq -r ".machines[] | select(.tunnel_id == \"$TUNNEL_NAME\") | .id")

"$KAGAMI" tunnel add --config "$CONFIG" \
  --name svc --local-addr "127.0.0.1:$LOCAL_PORT" \
  --hostname "svc.${TUNNEL_NAME}.${BASE_DOMAIN}" 2>&1

"$KAGAMI" run --config "$CONFIG" &
KAGAMI_PID=$!
sleep 2

HOST_HEADER="svc.${TUNNEL_NAME}.${BASE_DOMAIN}:8787"

# 1. Verify tunnel works while agent is connected
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  "http://localhost:8787/check")
STATUS=$(echo "$RESP" | tail -1)
assert_status "Request succeeds while agent connected" "200" "$STATUS"

# 2. Kill the agent
kill $KAGAMI_PID 2>/dev/null || true
wait $KAGAMI_PID 2>/dev/null || true
sleep 1

# 3. Request after agent disconnect → 502
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  "http://localhost:8787/check")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

assert_status "Request after agent disconnect returns 502" "502" "$STATUS"
assert_json "502 error is tunnel_offline" "$BODY" ".error" "tunnel_offline"

# 4. Reconnect agent — should work again
"$KAGAMI" run --config "$CONFIG" &
KAGAMI_PID=$!
sleep 2

RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  "http://localhost:8787/check")
STATUS=$(echo "$RESP" | tail -1)
assert_status "Request succeeds after agent reconnects" "200" "$STATUS"

# Clean up
kill $KAGAMI_PID 2>/dev/null || true
wait $KAGAMI_PID 2>/dev/null || true

summary
