#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${1:-http://localhost:8080}"
PASS=0
FAIL=0

ok()   { ((PASS++)); printf "\033[32m  PASS\033[0m %s\n" "$1"; }
fail() { ((FAIL++)); printf "\033[31m  FAIL\033[0m %s — %s\n" "$1" "$2"; }

assert_status() {
  local label="$1" expected="$2" actual="$3"
  if [ "$expected" -eq "$actual" ]; then
    ok "$label"
  else
    fail "$label" "expected $expected, got $actual"
  fi
}

assert_contains() {
  local label="$1" haystack="$2" needle="$3"
  if echo "$haystack" | grep -q "$needle"; then
    ok "$label"
  else
    fail "$label" "response does not contain '$needle'"
  fi
}

assert_not_contains() {
  local label="$1" haystack="$2" needle="$3"
  if echo "$haystack" | grep -q "$needle"; then
    fail "$label" "response must NOT contain '$needle'"
  else
    ok "$label"
  fi
}

echo "=== LLMSafeSpace Auth E2E Tests ==="
echo "Target: $BASE_URL"
echo ""

# --- 1. Register (Happy Path) ---
echo "--- Register ---"
REG_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"username":"e2euser","email":"e2e@example.com","password":"securepassword123"}')
REG_BODY=$(echo "$REG_RESP" | head -n -1)
REG_STATUS=$(echo "$REG_RESP" | tail -1)
assert_status "register: new user" 201 "$REG_STATUS"
assert_contains "register: returns token" "$REG_BODY" '"token"'
assert_not_contains "register: no password leak" "$REG_BODY" "password"
assert_not_contains "register: no hash leak" "$REG_BODY" "hash"
TOKEN=$(echo "$REG_BODY" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

# --- 2. Register (Unhappy: duplicate email) ---
REG2_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"username":"e2euser2","email":"e2e@example.com","password":"securepassword123"}')
REG2_BODY=$(echo "$REG2_RESP" | head -n -1)
REG2_STATUS=$(echo "$REG2_RESP" | tail -1)
assert_status "register: duplicate email → 500" 500 "$REG2_STATUS"
assert_not_contains "register: no email enumeration" "$REG2_BODY" "already registered"
assert_not_contains "register: no 'email exists' message" "$REG2_BODY" "email.*exists"

# --- 3. Register (Unhappy: short password) ---
REG3_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"username":"e2euser3","email":"e2e3@example.com","password":"short"}')
REG3_STATUS=$(echo "$REG3_RESP" | tail -1)
assert_status "register: short password → 400" 400 "$REG3_STATUS"

# --- 4. Register (Unhappy: missing fields) ---
REG4_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"username":"nobody"}')
REG4_STATUS=$(echo "$REG4_RESP" | tail -1)
assert_status "register: missing email → 400" 400 "$REG4_STATUS"

# --- 5. Register (Unhappy: oversized body) ---
OVERSIZE_BODY=$(python3 -c "print('x'*2100000)")
REG5_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"u\",\"email\":\"e@e.com\",\"password\":\"$OVERSIZE_BODY\"}" \
  --max-time 10 2>/dev/null || true)
REG5_STATUS=$(echo "$REG5_RESP" | tail -1)
if [ "$REG5_STATUS" = "400" ] || [ "$REG5_STATUS" = "413" ]; then
  ok "register: oversized body → 400/413"
else
  fail "register: oversized body" "expected 400 or 413, got $REG5_STATUS"
fi

# --- 6. Login (Happy Path) ---
echo ""
echo "--- Login ---"
LOGIN_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"e2e@example.com","password":"securepassword123"}')
LOGIN_BODY=$(echo "$LOGIN_RESP" | head -n -1)
LOGIN_STATUS=$(echo "$LOGIN_RESP" | tail -1)
assert_status "login: correct credentials" 200 "$LOGIN_STATUS"
assert_contains "login: returns token" "$LOGIN_BODY" '"token"'
assert_not_contains "login: no password leak" "$LOGIN_BODY" "password"
LOGIN_TOKEN=$(echo "$LOGIN_BODY" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

# --- 7. Login (Unhappy: wrong password) ---
LOGIN2_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"e2e@example.com","password":"wrongpassword"}')
LOGIN2_BODY=$(echo "$LOGIN2_RESP" | head -n -1)
LOGIN2_STATUS=$(echo "$LOGIN2_RESP" | tail -1)
assert_status "login: wrong password → 401" 401 "$LOGIN2_STATUS"
assert_not_contains "login: no user enumeration" "$LOGIN2_BODY" "not found"
assert_contains "login: generic error" "$LOGIN2_BODY" "invalid email or password"

# --- 8. Login (Unhappy: nonexistent user) ---
LOGIN3_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"nonexistent@example.com","password":"anything"}')
LOGIN3_BODY=$(echo "$LOGIN3_RESP" | head -n -1)
LOGIN3_STATUS=$(echo "$LOGIN3_RESP" | tail -1)
assert_status "login: nonexistent user → 401" 401 "$LOGIN3_STATUS"
assert_contains "login: same error for missing user" "$LOGIN3_BODY" "invalid email or password"

# --- 9. Login (Unhappy: missing fields) ---
LOGIN4_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"e2e@example.com"}')
LOGIN4_STATUS=$(echo "$LOGIN4_RESP" | tail -1)
assert_status "login: missing password → 400" 400 "$LOGIN4_STATUS"

# --- 10. Create API Key (Happy Path) ---
echo ""
echo "--- API Keys ---"
USE_TOKEN="${LOGIN_TOKEN:-$TOKEN}"
KEY_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/api-keys" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $USE_TOKEN" \
  -d '{"name":"e2e-test-key"}')
KEY_BODY=$(echo "$KEY_RESP" | head -n -1)
KEY_STATUS=$(echo "$KEY_RESP" | tail -1)
assert_status "create api key" 201 "$KEY_STATUS"
assert_contains "create api key: returns key" "$KEY_BODY" '"key"'
assert_contains "create api key: has lsp_ prefix" "$KEY_BODY" "lsp_"
API_KEY=$(echo "$KEY_BODY" | grep -o '"key":"[^"]*"' | cut -d'"' -f4)
KEY_ID=$(echo "$KEY_BODY" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)

# --- 11. Create API Key (Unhappy: no auth) ---
KEY2_RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/auth/api-keys" \
  -H "Content-Type: application/json" \
  -d '{"name":"unauth-key"}')
KEY2_STATUS=$(echo "$KEY2_RESP" | tail -1)
assert_status "create api key: no auth → 401" 401 "$KEY2_STATUS"

# --- 12. List API Keys (Happy Path) ---
LIST_RESP=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/api/v1/auth/api-keys" \
  -H "Authorization: Bearer $USE_TOKEN")
LIST_BODY=$(echo "$LIST_RESP" | head -n -1)
LIST_STATUS=$(echo "$LIST_RESP" | tail -1)
assert_status "list api keys" 200 "$LIST_STATUS"
assert_not_contains "list api keys: secrets stripped" "$LIST_BODY" "$API_KEY"

# --- 13. List API Keys (Unhappy: no auth) ---
LIST2_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X GET "$BASE_URL/api/v1/auth/api-keys")
assert_status "list api keys: no auth → 401" 401 "$LIST2_STATUS"

# --- 14. Use API Key for Auth ---
echo ""
echo "--- API Key Auth ---"
if [ -n "$API_KEY" ]; then
  AK_RESP=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/api/v1/auth/api-keys" \
    -H "Authorization: Bearer $API_KEY")
  AK_STATUS=$(echo "$AK_RESP" | tail -1)
  assert_status "api key auth works" 200 "$AK_STATUS"
else
  fail "api key auth" "no API key available"
fi

# --- 15. Delete API Key (Happy Path) ---
echo ""
echo "--- Delete API Key ---"
DEL_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
  "$BASE_URL/api/v1/auth/api-keys/$KEY_ID" \
  -H "Authorization: Bearer $USE_TOKEN")
assert_status "delete api key" 204 "$DEL_STATUS"

# --- 16. Delete API Key (Unhappy: no auth) ---
DEL2_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
  "$BASE_URL/api/v1/auth/api-keys/$KEY_ID")
assert_status "delete api key: no auth → 401" 401 "$DEL2_STATUS"

# --- 17. Delete API Key (Unhappy: already deleted) ---
DEL3_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
  "$BASE_URL/api/v1/auth/api-keys/$KEY_ID" \
  -H "Authorization: Bearer $USE_TOKEN")
assert_status "delete api key: already deleted → 500" 500 "$DEL3_STATUS"

# --- Summary ---
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
