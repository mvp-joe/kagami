#!/usr/bin/env bash
# Test: Proxy edge cases — 413 with connected agent, various HTTP methods, headers
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Proxy Edge Cases ==="

TUNNEL_NAME="proxy-edge-$(date +%s)"
CONFIG="$SCRATCH/proxy-edge-${TUNNEL_NAME}.toml"
LOCAL_PORT=19890

# Start a local HTTP server that echoes everything
python3 -c "
import http.server, socketserver, json

class ReuseTCPServer(socketserver.TCPServer):
    allow_reuse_address = True

class Handler(http.server.BaseHTTPRequestHandler):
    def handle_any(self):
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length).decode() if length else ''
        # Echo all request details
        headers_dict = {k: v for k, v in self.headers.items()}
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('X-Echo', 'true')
        self.end_headers()
        resp = json.dumps({
            'method': self.command,
            'path': self.path,
            'host': self.headers.get('Host', ''),
            'body': body,
            'headers': headers_dict,
            'content_type': self.headers.get('Content-Type', '')
        })
        self.wfile.write(resp.encode())
    do_GET = handle_any
    do_POST = handle_any
    do_PUT = handle_any
    do_DELETE = handle_any
    do_PATCH = handle_any
    do_HEAD = handle_any
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

# Register and start agent
printf 'http://localhost:8787\n%s\n%s\n' "$PROJECT_SECRET" "$TUNNEL_NAME" | \
  "$KAGAMI" init --config "$CONFIG" 2>&1

MACHINES_RESP=$(curl -s "$WORKER_URL/_kagami/machines" -H "Authorization: Bearer $PROJECT_SECRET")
MACHINE_ID=$(echo "$MACHINES_RESP" | jq -r ".machines[] | select(.tunnel_id == \"$TUNNEL_NAME\") | .id")

"$KAGAMI" tunnel add --config "$CONFIG" \
  --name echo --local-addr "127.0.0.1:$LOCAL_PORT" \
  --hostname "echo.${TUNNEL_NAME}.${BASE_DOMAIN}" 2>&1

"$KAGAMI" run --config "$CONFIG" &
KAGAMI_PID=$!
sleep 2

HOST_HEADER="echo.${TUNNEL_NAME}.${BASE_DOMAIN}:8787"

# 1. PUT method
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  -X PUT -H "Content-Type: application/json" -d '{"update":"data"}' \
  "http://localhost:8787/resource/1")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "PUT through tunnel returns 200" "200" "$STATUS"
assert_json "PUT method forwarded" "$BODY" ".method" "PUT"

# 2. DELETE method
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  -X DELETE \
  "http://localhost:8787/resource/1")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "DELETE through tunnel returns 200" "200" "$STATUS"
assert_json "DELETE method forwarded" "$BODY" ".method" "DELETE"

# 3. PATCH method
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  -X PATCH -H "Content-Type: application/json" -d '{"patch":"field"}' \
  "http://localhost:8787/resource/1")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "PATCH through tunnel returns 200" "200" "$STATUS"
assert_json "PATCH method forwarded" "$BODY" ".method" "PATCH"

# 4. Custom headers are forwarded
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  -H "X-Custom-Test: hello-world" \
  -H "Accept: text/html" \
  "http://localhost:8787/headers-test")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "Custom headers request returns 200" "200" "$STATUS"
CUSTOM_HDR=$(echo "$BODY" | jq -r '.headers["X-Custom-Test"] // .headers["x-custom-test"]')
if [ "$CUSTOM_HDR" = "hello-world" ]; then
  pass "Custom header X-Custom-Test forwarded"
else
  fail "Custom header X-Custom-Test forwarded" "hello-world" "$CUSTOM_HDR (body: $(echo "$BODY" | jq -c '.headers'))"
fi

# 5. Content-Type header preserved
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  -X POST -H "Content-Type: text/plain" -d 'plain text body' \
  "http://localhost:8787/content-type-test")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "Content-Type preserved returns 200" "200" "$STATUS"
CT=$(echo "$BODY" | jq -r '.content_type')
if echo "$CT" | grep -q "text/plain"; then
  pass "Content-Type text/plain forwarded"
else
  fail "Content-Type text/plain forwarded" "text/plain" "$CT"
fi

# 6. Large (but under limit) request body — 1MB via file
MEDIUM_FILE="$SCRATCH/medium-body.bin"
python3 -c "import sys; sys.stdout.buffer.write(b'A' * 1000000)" > "$MEDIUM_FILE"
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  -X POST -H "Content-Type: text/plain" --data-binary "@$MEDIUM_FILE" \
  "http://localhost:8787/large-body")
STATUS=$(echo "$RESP" | tail -1)
rm -f "$MEDIUM_FILE"
assert_status "1MB body through tunnel returns 200" "200" "$STATUS"

# 7. 413 with connected agent (oversized body > 10MB)
LARGE_FILE="$SCRATCH/large-body-proxy.bin"
python3 -c "
import sys
sys.stdout.buffer.write(b'x' * (10 * 1024 * 1024 + 1))
" > "$LARGE_FILE"

RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  -H "Content-Type: application/octet-stream" \
  -X POST --data-binary "@$LARGE_FILE" \
  "http://localhost:8787/upload" 2>/dev/null)
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
rm -f "$LARGE_FILE"

assert_status "Oversized body with connected agent returns 413" "413" "$STATUS"
assert_json "413 error is payload_too_large" "$BODY" ".error" "payload_too_large"
assert_json "413 has message" "$BODY" ".message" "Request body exceeds maximum size"

# 8. Empty path
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  "http://localhost:8787/")
STATUS=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "Root path returns 200" "200" "$STATUS"
assert_json "Root path is /" "$BODY" ".path" "/"

# 9. Path with special characters
RESP=$(curl -s -w "\n%{http_code}" \
  -H "Host: $HOST_HEADER" \
  "http://localhost:8787/path/with%20spaces/and%2Fslashes")
STATUS=$(echo "$RESP" | tail -1)
assert_status "URL-encoded path returns 200" "200" "$STATUS"

summary
