#!/usr/bin/env bash
# Test: kagami project-secret command
set -euo pipefail
source "$(dirname "$0")/helpers.sh"

echo "=== Test: Project Secret Generation ==="

# 1. Generate a project secret
OUTPUT1=$("$KAGAMI" project-secret 2>&1)
if [ -n "$OUTPUT1" ]; then
  pass "project-secret generates output"
else
  fail "project-secret generates output" "non-empty" "empty"
fi

# Extract just the secret (line containing kgm_proj_)
SECRET1=$(echo "$OUTPUT1" | grep -oP 'kgm_proj_[a-f0-9]+' | head -1)

# 2. Should have kgm_proj_ prefix (per spec example format)
if [ -n "$SECRET1" ]; then
  pass "Project secret has kgm_proj_ prefix"
else
  fail "Project secret has kgm_proj_ prefix" "kgm_proj_*" "$OUTPUT1"
fi

# 3. Generate another — should be different (random)
OUTPUT2=$("$KAGAMI" project-secret 2>&1)
SECRET2=$(echo "$OUTPUT2" | grep -oP 'kgm_proj_[a-f0-9]+' | head -1)
if [ "$SECRET1" != "$SECRET2" ]; then
  pass "Each project-secret is unique"
else
  fail "Each project-secret is unique" "different" "same: $SECRET1"
fi

# 4. Secret should be reasonably long (hex-encoded SHA-256 = 64 chars + prefix)
SECRET_LEN=${#SECRET1}
if [ "$SECRET_LEN" -ge 40 ]; then
  pass "Project secret is reasonable length ($SECRET_LEN chars)"
else
  fail "Project secret is reasonable length" ">= 40 chars" "$SECRET_LEN chars"
fi

summary
