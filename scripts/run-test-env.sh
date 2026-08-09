#!/usr/bin/env bash
set -euo pipefail

BASE_URL="http://127.0.0.1:3456"
TESTING_TOKEN="test-token-for-seeding"
TEST_USER="testuser"
TEST_PASS="testpassword"

# ── Step 0: Pre-flight permissions ──────────────────────────────
echo "==> Setting file permissions for container access..."
chmod a+r config.test.yml
find . -name '*.go' -exec chmod a+r {} \+ 2>/dev/null || true

# ── Step 1: Start the container ─────────────────────────────────
echo "==> Starting Vikunja test instance..."
docker compose -f compose.test.yml down --volumes 2>/dev/null || true
docker compose -f compose.test.yml up -d

# ── Step 2: Wait for health ─────────────────────────────────────
echo -n "==> Waiting for Vikunja to be healthy"
MAX_WAIT=60
ELAPSED=0
while ! curl -sf -o /dev/null "$BASE_URL/api/v2/info"; do
  if [ "$ELAPSED" -ge "$MAX_WAIT" ]; then
    echo ""
    echo "ERROR: Vikunja did not become healthy within ${MAX_WAIT}s."
    echo "Check logs: docker compose -f compose.test.yml logs"
    exit 1
  fi
  echo -n "."
  sleep 2
  ELAPSED=$((ELAPSED + 2))
done
echo " ready"

# ── Step 3: Seed test user ──────────────────────────────────────
echo "==> Seeding test user..."
SEED_RESPONSE=$(curl -s -X PUT "$BASE_URL/api/v2/test/users?truncate=true" \
  -H "Authorization: $TESTING_TOKEN" \
  -H "Content-Type: application/json" \
  -d "[{
    \"id\": 1,
    \"username\": \"$TEST_USER\",
    \"password\": \"$2b$10$8.vLTS6/Ya5NCMHvP3ZiS.1shEBBsVJsTwYy8BET6B/a/zvLo/vQS\",
    \"email\": \"test@localhost.local\",
    \"issuer\": \"local\",
    \"status\": 0
  }]")

if echo "$SEED_RESPONSE" | grep -qi "error"; then
  echo "ERROR: Failed to seed test user: $SEED_RESPONSE"
  exit 1
fi
echo "   Test user created (username: $TEST_USER)"

# ── Step 4: Login ───────────────────────────────────────────────
echo "==> Logging in..."
LOGIN_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v2/login" \
  -H "Content-Type: application/json" \
  -d "{\"username\": \"$TEST_USER\", \"password\": \"$TEST_PASS\"}")

JWT=$(echo "$LOGIN_RESPONSE" | jq -r '.token // empty')
if [ -z "$JWT" ]; then
  echo "ERROR: Failed to extract token from login response: $LOGIN_RESPONSE"
  exit 1
fi
echo "   Logged in"

# ── Step 5: Print result ────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Vikunja test instance ready                                ║"
echo "║                                                            ║"
echo "║  Export this in your shell:                                ║"
echo "║    export JWT=\"$JWT\"                                       ║"
echo "║                                                            ║"
echo "║  Or use directly:                                          ║"
echo "║    curl -s -H \"Authorization: Bearer $JWT\" \\              ║"
echo "║      $BASE_URL/api/v1/your/api/endpoint | jq .              ║"
echo "║                                                            ║"
echo "║  Unauthenticated endpoint:                                 ║"
echo "║    curl -s $BASE_URL/api/v2/info | jq .                     ║"
echo "║                                                            ║"
echo "║  Stop: docker compose -f compose.test.yml down             ║"
echo "╚══════════════════════════════════════════════════════════════╝"
