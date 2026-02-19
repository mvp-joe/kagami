#!/usr/bin/env bash
# Test: Health check endpoint
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Health Check ==="

# GET /_kagami/health should return 200
RESP=$(curl -s -o /dev/null -w "%{http_code}" "$WORKER_URL/_kagami/health")
assert_status "GET /_kagami/health returns 200" "200" "$RESP"

summary
