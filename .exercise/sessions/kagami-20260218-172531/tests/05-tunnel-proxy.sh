#!/usr/bin/env bash
# Test: End-to-end tunnel proxying
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Tunnel Proxy (End-to-End) ==="

TUNNEL_NAME="e2e-$(date +%s)"
CONFIG="$SCRATCH/e2e-${TUNNEL_NAME}.toml"
LOCAL_PORT=19876
LOCAL_PORT2=19877

# Start a local HTTP server (with SO_REUSEADDR)
python3 -c "
import http.server, socketserver, json

class ReuseTCPServer(socketserver.TCPServer):
    allow_reuse_address = True

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('X-Custom-Header', 'hello-from-local')
        self.end_headers()
        body = json.dumps({'path': self.path, 'method': 'GET', 'host': self.headers.get('Host','')})
        self.wfile.write(body.encode())
    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length).decode() if length else ''
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        resp = json.dumps({'path': self.path, 'method': 'POST', 'body': body, 'host': self.headers.get('Host','')})
        self.wfile.write(resp.encode())
    def log_message(self, format, *args):
        pass

server = ReuseTCPServer(('127.0.0.1', $LOCAL_PORT), Handler)
server.serve_forever()
" &
LOCAL_SERVER_PID=$!

# Start a second local server for multi-tunnel test
python3 -c "
import http.server, socketserver, json

class ReuseTCPServer(socketserver.TCPServer):
    allow_reuse_address = True

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain')
        self.end_headers()
        self.wfile.write(b'hello from server 2')
    def log_message(self, format, *args):
        pass

server = ReuseTCPServer(('127.0.0.1', $LOCAL_PORT2), Handler)
server.serve_forever()
" &
LOCAL_SERVER2_PID=$!

cleanup() {
  kill $LOCAL_SERVER_PID 2>/dev/null || true
  kill $LOCAL_SERVER2_PID 2>/dev/null || true
  # Stop kagami
  if [ -n "${KAGAMI_PID:-}" ]; then
    kill $KAGAMI_PID 2>/dev/null || true
    wait $KAGAMI_PID 2>/dev/null || true
  fi
  # Clean up machine from worker
  if [ -n "${MACHINE_ID:-}" ] && [ "$MACHINE_ID" != "null" ]; then
    curl -s -o /dev/null -X DELETE "$WORKER_URL/_kagami/machines/$MACHINE_ID" \
      -H "Authorization: Bearer $PROJECT_SECRET"
  fi
  rm -f "$CONFIG"
}
trap cleanup EXIT

sleep 0.5

# Verify local server is up
LOCAL_CHECK=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$LOCAL_PORT/test" 2>/dev/null || echo "000")
if [ "$LOCAL_CHECK" != "200" ]; then
  echo "ERROR: Local test server didn't start"
  exit 1
fi

# Register via kagami init
printf 'http://localhost:8787\n%s\n%s\n' "$PROJECT_SECRET" "$TUNNEL_NAME" | \
  "$KAGAMI" init --config "$CONFIG" 2>&1

# Get machine ID for cleanup
MACHINES_RESP=$(curl -s "$WORKER_URL/_kagami/machines" \
  -H "Authorization: Bearer $PROJECT_SECRET")
MACHINE_ID=$(echo "$MACHINES_RESP" | jq -r ".machines[] | select(.tunnel_id == \"$TUNNEL_NAME\") | .id")

# Add tunnel
"$KAGAMI" tunnel add --config "$CONFIG" \
  --name api --local-addr "127.0.0.1:$LOCAL_PORT" \
  --hostname "api.${TUNNEL_NAME}.${BASE_DOMAIN}" 2>&1

# Start kagami agent in background
"$KAGAMI" run --config "$CONFIG" &
KAGAMI_PID=$!
sleep 2

# 1. Basic GET through tunnel
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: api.${TUNNEL_NAME}.${BASE_DOMAIN}:8787" \
  "http://localhost:8787/hello")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

assert_status "GET through tunnel returns 200" "200" "$STATUS"
assert_json "Response path is /hello" "$BODY" ".path" "/hello"
assert_json "Response method is GET" "$BODY" ".method" "GET"

# 2. POST through tunnel
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: api.${TUNNEL_NAME}.${BASE_DOMAIN}:8787" \
  -H "Content-Type: application/json" \
  -X POST -d '{"key":"value"}' \
  "http://localhost:8787/submit")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

assert_status "POST through tunnel returns 200" "200" "$STATUS"
assert_json "POST response path is /submit" "$BODY" ".path" "/submit"
assert_json "POST response method is POST" "$BODY" ".method" "POST"
assert_json "POST body forwarded" "$BODY" ".body" '{"key":"value"}'

# 3. Host header forwarded (spec: full original Host header forwarded to DO and agent)
HOST_IN_RESP=$(echo "$BODY" | jq -r ".host")
if echo "$HOST_IN_RESP" | grep -q "${TUNNEL_NAME}.${BASE_DOMAIN}"; then
  pass "Host header forwarded to local service"
else
  fail "Host header forwarded to local service" "contains ${TUNNEL_NAME}.${BASE_DOMAIN}" "$HOST_IN_RESP"
fi

# 4. Query string preserved
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: api.${TUNNEL_NAME}.${BASE_DOMAIN}:8787" \
  "http://localhost:8787/search?q=test&page=1")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

assert_status "GET with query string returns 200" "200" "$STATUS"
PATH_RESP=$(echo "$BODY" | jq -r ".path")
if echo "$PATH_RESP" | grep -q "q=test"; then
  pass "Query string preserved"
else
  fail "Query string preserved" "contains q=test" "$PATH_RESP"
fi

# 5. Multiple subdomain levels route to same DO (rightmost subdomain before base)
# Per spec: a.b.c.my-homelab → tunnel_id is my-homelab
# The request reaches the agent (same DO), but no configured hostname matches deep.sub.*
# so agent correctly returns 404
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: deep.sub.${TUNNEL_NAME}.${BASE_DOMAIN}:8787" \
  "http://localhost:8787/nested")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

# Should get 404 from agent (no matching tunnel hostname), NOT 502 (which would mean routing failed)
assert_status "Nested subdomain routes to correct DO (404 from agent, not 502)" "404" "$STATUS"

# 6. Add a second tunnel and test multi-tunnel
"$KAGAMI" tunnel add --config "$CONFIG" \
  --name web --local-addr "127.0.0.1:$LOCAL_PORT2" \
  --hostname "web.${TUNNEL_NAME}.${BASE_DOMAIN}" 2>&1

# Note: the second tunnel has the same tunnel_id in routing (rightmost subdomain before base_domain)
# So both "api" and "web" subdomains route to the same DO/agent. The agent uses hostname-based
# routing to pick the right local service.

summary
