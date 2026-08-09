# Test Instance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a Docker-based Vikunja 2.4 test instance with a one-command bootstrap script for local plugin development.

**Architecture:** A single docker compose service mounts the plugin source directory and a pinned config file. A shell bootstrap script handles pre-flight permissions, waits for the container to become healthy, seeds a non-admin test user via the testingtoken seed endpoint, logs in, and prints a ready-to-use JWT.

**Tech Stack:** Docker Compose, shell (bash), curl, jq

## Global Constraints

- Vikunja image: `vikunja/vikunja:2.4`
- Port bound to `127.0.0.1:3456` only — no external exposure
- No `user:` directive in compose — default UID 1000
- SQLite only — no external database service
- Test user is a regular (non-admin) user — plugin management is gated by config whitelist, not Vikunja's licensed admin feature
- All state is ephemeral — `docker compose down` destroys everything
- Config must be minimal — no forward-looking sections or placeholders
- Testing token value: `test-token-for-seeding`
- JWT secret: `test-secret-do-not-use-in-production`

---

## File Structure

```
vikunja-custom-fields-plugin/
├── compose.test.yml          # NEW — Docker Compose service definition
├── config.test.yml           # NEW — Vikunja configuration mounted into container
├── scripts/
│   └── run-test-env.sh       # NEW — Bootstrap script (pre-flight + seed + login)
└── CLAUDE.local.md           # MODIFY — Add "Test Instance" section
```

---

### Task 1: Create config.test.yml

**Files:**
- Create: `config.test.yml`

**Interfaces:**
- Produces: Vikunja config file consumed by the docker compose mount
- Key values consumed by Task 3 (`run-test-env.sh`): `service.testingtoken` = `"test-token-for-seeding"`, `service.secret` = `"test-secret-do-not-use-in-production"`

- [ ] **Step 1: Write the config file**

```yaml
service:
  secret: "test-secret-do-not-use-in-production"
  testingtoken: "test-token-for-seeding"
  enableregistration: true

database:
  type: sqlite
  path: /db/vikunja.db

plugins:
  enabled: true
  dir: /app/vikunja/plugins
  loader: yaegi

log:
  level: DEBUG
  http: stdout

mailer:
  enabled: false

ratelimit:
  enabled: false
```

- [ ] **Step 2: Verify the file is valid YAML**

Run: `python3 -c "import yaml; yaml.safe_load(open('config.test.yml')); print('valid')"` (or `cat config.test.yml | python3 -c "import sys,yaml; yaml.safe_load(sys.stdin); print('valid')"`)

- [ ] **Step 3: Commit**

```bash
git add config.test.yml
git commit -m "feat: add test instance Vikunja config"
```

---

### Task 2: Create compose.test.yml

**Files:**
- Create: `compose.test.yml`

**Interfaces:**
- Produces: Docker Compose service that starts Vikunja 2.4 with the plugin source mounted live
- Mounts: `./` → `/app/vikunja/plugins/custom-fields` (plugin source), `./config.test.yml` → `/etc/vikunja/config.yml:ro` (config)

- [ ] **Step 1: Write the compose file**

```yaml
services:
  vikunja:
    image: vikunja/vikunja:2.4
    ports:
      - "127.0.0.1:3456:3456"
    volumes:
      - ./:/app/vikunja/plugins/custom-fields
      - ./config.test.yml:/etc/vikunja/config.yml:ro
    restart: "no"
```

- [ ] **Step 2: Verify compose syntax**

Run: `docker compose -f compose.test.yml config --quiet`

Expected: exit code 0, no output (compose file parses correctly). Note: this does not start the container, only validates the file.

- [ ] **Step 3: Commit**

```bash
git add compose.test.yml
git commit -m "feat: add test instance Docker Compose file"
```

---

### Task 3: Create scripts/run-test-env.sh

**Files:**
- Create: `scripts/run-test-env.sh`

**Interfaces:**
- Consumes: `compose.test.yml`, `config.test.yml` (reads testingtoken value)
- Produces: Starts container, seeds test user, prints JWT to stdout
- Test user credentials: username `testuser`, password `testpassword`, bcrypt hash `$2b$10$8.vLTS6/Ya5NCMHvP3ZiS.1shEBBsVJsTwYy8BET6B/a/zvLo/vQS`

- [ ] **Step 1: Create the scripts directory**

```bash
mkdir -p scripts
```

- [ ] **Step 2: Write the bootstrap script**

```bash
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
    \"password\": \"\$2b\$10\$8.vLTS6/Ya5NCMHvP3ZiS.1shEBBsVJsTwYy8BET6B/a/zvLo/vQS\",
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
```

- [ ] **Step 3: Make the script executable**

```bash
chmod +x scripts/run-test-env.sh
```

- [ ] **Step 4: Verify syntax**

Run: `bash -n scripts/run-test-env.sh`

Expected: exit code 0 (no syntax errors).

- [ ] **Step 5: Commit**

```bash
git add scripts/run-test-env.sh
git commit -m "feat: add test instance bootstrap script"
```

---

### Task 4: Update CLAUDE.local.md

**Files:**
- Modify: `CLAUDE.local.md`

**Interfaces:**
- Consumes: compose.test.yml, config.test.yml, scripts/run-test-env.sh (documents their use)
- Produces: Updated CLAUDE.local.md with "Test Instance" section appended

- [ ] **Step 1: Read the current CLAUDE.local.md to verify exact content**

Read the file at `CLAUDE.local.md`. It currently ends with the Planning section:

```
### Planning

When writing plans, you **MUST** include a "Required Skills" list and "Recommended Skills" list for **EVERY** task.
```

- [ ] **Step 2: Append the Test Instance section**

Append the following after the Planning section (after the last line):

```markdown

## Test Instance

A Docker-based Vikunja 2.4 instance for local plugin development.

### Quick Start

```
./scripts/run-test-env.sh
```

This starts the container, waits for it to be healthy, creates a test
user, logs in, and prints a ready-to-use JWT.

### Stop

```
docker compose -f compose.test.yml down
```

### Manual Testing

Once you have a JWT, use it with authenticated endpoints:

```
curl -s -H "Authorization: Bearer $JWT" \
  http://127.0.0.1:3456/api/v1/your/api/endpoint | jq .
```

(Use without the Authorization header for unauthenticated endpoints
like /api/v2/info.)

### How It Works

- compose.test.yml starts Vikunja 2.4 with the plugin source mounted
  live at /app/vikunja/plugins/custom-fields — edit main.go, restart
  the container, changes take effect.
- config.test.yml enables the yaegi plugin loader, sets a stable
  JWT secret, and enables the testingtoken seed endpoint.
- run-test-env.sh seeds a regular (non-admin) test user via the
  testingtoken endpoint and logs in to get a JWT.
- The DB is ephemeral — docker compose down destroys all data.

### The Testing Token Seed Endpoint

When config.test.yml sets service.testingtoken (value:
"test-token-for-seeding"), Vikunja exposes database seed endpoints.
These bypass normal auth — authorization is the raw token string,
not a JWT.

Endpoints (both v1 and v2 exist; prefer v2):

```
# Seed a table (truncates by default, ?truncate=false to append)
PUT /api/v2/test/{table}

# Truncate all tables
DELETE /api/v2/test/all
```

Authorization header format:

```
Authorization: <raw-testing-token>    (NOT "Bearer ...")
```

Request body for PUT /api/v2/test/{table}:
A JSON array of objects, each object is a row with column names
as keys. Example — seeding a user:

```
curl -s -X PUT 'http://127.0.0.1:3456/api/v2/test/users?truncate=true' \
  -H 'Authorization: test-token-for-seeding' \
  -H 'Content-Type: application/json' \
  -d '[{
    "id": 1,
    "username": "testuser",
    "password": "$2a$14$...",    # bcrypt hash
    "email": "test@localhost.local",
    "issuer": "local",
    "status": 0
  }]'
```

Available tables include: users, projects, tasks, labels, teams,
and all other Vikunja database tables. See the Vikunja source at
pkg/db/fixtures/ for column names and example data.

The seeding user table ("users") has a dependency on "notifications" —
the endpoint handles this automatically (clears notifications when
users are truncated).

### Modifying the Test Harness

As the plugin evolves, you may need to:
- Add config sections to config.test.yml as the plugin's configuration
  needs grow.
- Add seeding steps to run-test-env.sh — for example, creating test
  projects or pre-populating data via additional PUT calls to
  /api/v2/test/{table}.
- Change the Vikunja image tag in compose.test.yml to test against
  a different version.

### Troubleshooting

- Container fails to read mounted files: chmod a+r on config.test.yml
  and the plugin .go source files, then restart.
- Plugin not loading: check logs with `docker compose -f compose.test.yml logs`
  and look for "Loaded plugin" or error messages.
- Port already in use: ensure nothing else is running on 3456.
```

- [ ] **Step 3: Verify the file is well-formed**

Skim the file to confirm the new section is appended correctly and doesn't duplicate or overwrite existing content.

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.local.md
git commit -m "docs: add test instance instructions to CLAUDE.local.md"
```

---

### Task 5: End-to-end verification

**Files:**
- None (verification only)

**Interfaces:**
- Consumes: compose.test.yml, config.test.yml, scripts/run-test-env.sh

- [ ] **Step 1: Pull the Vikunja image**

```bash
docker pull vikunja/vikunja:2.4
```

- [ ] **Step 2: Run the bootstrap script**

```bash
./scripts/run-test-env.sh
```

Expected: script completes successfully, prints a JWT, exit code 0.

- [ ] **Step 3: Verify the JWT works against an authenticated endpoint**

Extract the JWT from the script output and test it:

```bash
JWT="<token-from-output>"
curl -sf -H "Authorization: Bearer $JWT" http://127.0.0.1:3456/api/v2/info > /dev/null && echo "Authenticated request OK" || echo "FAIL"
```

Expected: "Authenticated request OK"

- [ ] **Step 4: Verify the JWT also works unauthenticated**

```bash
curl -sf http://127.0.0.1:3456/api/v2/info > /dev/null && echo "Public request OK" || echo "FAIL"
```

Expected: "Public request OK"

- [ ] **Step 5: Verify idempotency — run the script a second time**

```bash
./scripts/run-test-env.sh
```

Expected: completes successfully again (user is replaced, token is fresh).

- [ ] **Step 6: Stop and clean up**

```bash
docker compose -f compose.test.yml down
```

- [ ] **Step 7: Verify cleanup**

```bash
docker compose -f compose.test.yml ps
```

Expected: no running services.

- [ ] **Step 8: Commit (if any changes were made during verification)**

```bash
git status
# Only commit if verification revealed issues that were fixed
```
