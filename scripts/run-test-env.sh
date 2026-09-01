#!/usr/bin/env bash
set -euo pipefail

BASE_URL="http://127.0.0.1:4176"
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
rm -rf db && mkdir -p db
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

# ── Step 3: Seed test users ─────────────────────────────────────
echo "==> Seeding test users..."
SEED_RESPONSE=$(curl -s -X PUT "$BASE_URL/api/v2/test/users?truncate=true" \
  -H "Authorization: $TESTING_TOKEN" \
  -H "Content-Type: application/json" \
  -d "[{
    \"id\": 1,
    \"username\": \"$TEST_USER\",
    \"password\": \"\$2b\$10\$8.vLTS6/Ya5NCMHvP3ZiS.1shEBBsVJsTwYy8BET6B/a/zvLo/vQS\",
    \"email\": \"test@localhost.local\",
    \"created\": \"2026-08-08T00:00:00Z\",
    \"updated\": \"2026-08-08T00:00:00Z\",
    \"issuer\": \"local\",
    \"status\": 0
  },{
    \"id\": 2,
    \"username\": \"otheruser\",
    \"password\": \"\$2b\$10\$8.vLTS6/Ya5NCMHvP3ZiS.1shEBBsVJsTwYy8BET6B/a/zvLo/vQS\",
    \"email\": \"other@localhost.local\",
    \"created\": \"2026-08-08T00:00:00Z\",
    \"updated\": \"2026-08-08T00:00:00Z\",
    \"issuer\": \"local\",
    \"status\": 0
  }]")

if echo "$SEED_RESPONSE" | grep -qi "error"; then
  echo "ERROR: Failed to seed test users: $SEED_RESPONSE"
  exit 1
fi
echo "   Test users created (managers: $TEST_USER; non-manager: otheruser)"

# ── Step 4: Login both users ──────────────────────────────────
login_user() {
  local user="$1" pass="$2"
  local resp
  resp=$(curl -s -X POST "$BASE_URL/api/v2/login" \
    -H "Content-Type: application/json" \
    -d "{\"username\": \"$user\", \"password\": \"$pass\"}")
  local tok
  tok=$(echo "$resp" | jq -r '.token // empty')
  if [ -z "$tok" ]; then
    echo "ERROR: Failed to extract token for $user: $resp"
    exit 1
  fi
  echo "$tok"
}

echo "==> Logging in..."
JWT=$(login_user "$TEST_USER" "testpassword")
JWT_OTHERUSER=$(login_user "otheruser" "testpassword")
echo "   Logged in (testuser + otheruser)"

# ── Step 5: Seed a test project (for assignment-validation ACs) ──
echo "==> Creating a test project via the API..."
PROJECT_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v2/projects" \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"title": "Test Project"}')
PROJECT_ID=$(echo "$PROJECT_RESPONSE" | jq -r .id)
if [ -z "$PROJECT_ID" ] || [ "$PROJECT_ID" = "null" ]; then
  echo "WARNING: project create response: $PROJECT_RESPONSE"
else
  echo "   Test project created (id $PROJECT_ID)"
fi
export PROJECT_ID

# ── Step 5b: Seed a custom field definition + a task ────────────
echo "==> Creating a custom field definition (whitelist-gated)..."
FIELD_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v1/plugins/custom-fields/definitions" \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"name":"Priority","type":"integer","project_ids":['"$PROJECT_ID"']}')
FIELD_ID=$(echo "$FIELD_RESPONSE" | jq -r .id)
if [ -z "$FIELD_ID" ] || [ "$FIELD_ID" = "null" ]; then
  echo "WARNING: field create response: $FIELD_RESPONSE"
else
  echo "   Custom field created (id $FIELD_ID)"
fi

echo "==> Creating a task in the test project..."
TASK_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v2/projects/$PROJECT_ID/tasks" \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"title":"seeded task"}')
TASK_ID=$(echo "$TASK_RESPONSE" | jq -r .id)
if [ -z "$TASK_ID" ] || [ "$TASK_ID" = "null" ]; then
  echo "WARNING: task create response: $TASK_RESPONSE"
else
  echo "   Task created (id $TASK_ID)"
fi
export FIELD_ID TASK_ID

# ── Step 5c: Seed the field type variety (for the S5 render test) ──
echo "==> Creating field definitions for each type..."
TYPE_DEFS='[
  {"name":"Text Field","type":"text","project_ids":['"$PROJECT_ID"']},
  {"name":"Select Field","type":"select","options":[{"value":"draft","label":"Draft"},{"value":"published","label":"Published"}],"project_ids":['"$PROJECT_ID"']},
  {"name":"Date Field","type":"date","project_ids":['"$PROJECT_ID"']},
  {"name":"Checkbox Field","type":"checkbox","project_ids":['"$PROJECT_ID"']},
  {"name":"URL Field","type":"url","project_ids":['"$PROJECT_ID"']},
  {"name":"API-Only Field","type":"text","field_config":{"is_api_only":true},"project_ids":['"$PROJECT_ID"']}
]'
while read -r def; do
  def_name=$(echo "$def" | jq -r .name)
  def_resp=$(curl -s -X POST "$BASE_URL/api/v1/plugins/custom-fields/definitions" \
    -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
    -d "$def")
  if [ -z "$(echo "$def_resp" | jq -r '.id // empty')" ]; then
    echo "   WARN: failed to create $def_name: $def_resp"
  else
    echo "   created: $def_name (id $(echo "$def_resp" | jq -r .id))"
  fi
done < <(echo "$TYPE_DEFS" | jq -c '.[]')

# ── Step 6: Print result ────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Vikunja test instance ready                                 ║"
echo "║                                                              ║"
echo "║  Whitelisted manager (is_manager should be true):            ║"
echo "║    export JWT=\"$JWT\"                                       ║"
echo "║                                                              ║"
echo "║  Non-whitelisted user (is_manager should be false):          ║"
echo "║    export JWT_OTHERUSER=\"$JWT_OTHERUSER\"                   ║"
echo "║    export PROJECT_ID=\"$PROJECT_ID\"                         ║"
echo "║                                                              ║"
echo "║  Seeded plugin fixtures (for AC verification):               ║"
echo "║    export FIELD_ID=\"$FIELD_ID\"                             ║"
echo "║    export TASK_ID=\"$TASK_ID\"                               ║"
echo "║                                                              ║"
echo "║  Verify the whitelist:                                       ║"
echo "║    curl -s -H \"Authorization: Bearer \$JWT\" \\               ║"
echo "║      $BASE_URL/api/v1/plugins/custom-fields/definitions | jq .  ║"
echo "║    curl -s -H \"Authorization: Bearer \$JWT_OTHERUSER\" \\     ║"
echo "║      $BASE_URL/api/v1/plugins/custom-fields/definitions | jq .  ║"
echo "║                                                              ║"
echo "║  Stop: docker compose -f compose.test.yml down               ║"
echo "╚══════════════════════════════════════════════════════════════╝"
