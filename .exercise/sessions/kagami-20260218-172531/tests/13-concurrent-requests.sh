#!/usr/bin/env bash
# Test: Multiple sequential requests through tunnel (verifies stability)
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Multiple Sequential Requests ==="

TUNNEL_NAME="multi-$(date +%s)"
CONFIG="$SCRATCH/multi-${TUNNEL_NAME}.toml"
LOCAL_PORT=19893

# Start a counter-based local server
python3 -c "
import http.server, socketserver, json

class ReuseTCPServer(socketserver.TCPServer):
    allow_reuse_address = True

counter = [0]

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        counter[0] += 1
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        resp = json.dumps({'count': counter[0], 'path': self.path})
        self.wfile.write(resp.encode())
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

# Register and set up
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

# Send 10 sequential requests
SUCCESS_COUNT=0
for i in $(seq 1 10); do
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Host: $HOST_HEADER" \
    "http://localhost:8787/req/$i")
  if [ "$STATUS" = "200" ]; then
    SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
  fi
done

if [ $SUCCESS_COUNT -eq 10 ]; then
  pass "All 10 sequential requests returned 200"
else
  fail "All 10 sequential requests returned 200" "10" "$SUCCESS_COUNT"
fi

# Verify counter incremented (requests reached local server)
RESP=$(curl -s -H "Host: $HOST_HEADER" "http://localhost:8787/final")
COUNT=$(echo "$RESP" | jq -r '.count')
if [ "$COUNT" = "11" ]; then
  pass "Local server received all 11 requests"
else
  fail "Local server received all 11 requests" "11" "$COUNT"
fi

# Kill agent
kill $KAGAMI_PID 2>/dev/null || true
wait $KAGAMI_PID 2>/dev/null || true

summary
