# Test Instance — Design Spec

**Date:** 2026-08-08

## Summary

A Docker-based test instance of Vikunja 2.4 for local plugin development. A single `./scripts/run-test-env.sh` command starts the container, seeds a test user, and prints a ready-to-use JWT. All state is ephemeral — `docker compose down` destroys everything.

## Deliverables

Three new files in the plugin repo root, plus updates to an existing file:

| File | Role |
|------|------|
| `compose.test.yml` | Docker Compose — Vikunja 2.4 with plugin source mounted live |
| `config.test.yml` | Vikunja config — plugins, testingtoken, auth, logging |
| `scripts/run-test-env.sh` | Bootstrap script — pre-flight permissions, wait for health, seed user, login, print JWT |
| `CLAUDE.local.md` | Updated with "Test Instance" section |

No other files change. These are net-new additions that don't touch existing story docs or source code.

## compose.test.yml

Single-service Compose file:

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

**Design decisions:**

- **No `user:` directive.** The default UID 1000 works — all mounts are read-only or read-by-yaegi, and the SQLite DB lives inside the container at the image's default `/db/vikunja.db`.
- **Plugin mount at repo root (`./`).** The agent's working copy is live in the container — edit `main.go`, restart the container, changes take effect.
- **`restart: "no"`.** Container stays stopped on error so agents can inspect logs. No infinite restart loop masking startup failures.
- **No named volumes.** All data is ephemeral — `docker compose down` destroys the DB. Consistent starting state every time.
- **No DB service.** SQLite via the image's default `VIKUNJA_DATABASE_PATH=/db/vikunja.db`. One container, no orchestration.
- **Port bound to `127.0.0.1`.** Test instance is local-only.

## config.test.yml

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

**Design decisions:**

- **`service.secret` pinned.** Stable JWT signing key — tokens survive container restarts.
- **`service.testingtoken` pinned.** Enables `/api/v2/test/{table}` seed endpoints. The bootstrap script uses this to create users in one API call without going through registration flow.
- **`plugins.dir` at `/app/vikunja/plugins`.** Container path where compose mounts the plugin source.
- **SQLite only.** No PostgreSQL/MySQL service needed. Simplest possible setup.
- **`log.level: DEBUG`.** Plugin load/failure messages visible in container logs.
- **Mailer and ratelimit disabled.** Not needed for local testing.
- **Minimal config.** No forward-looking sections or placeholders. Config grows as the plugin's needs grow.

## scripts/run-test-env.sh

Single entry point for the test instance. Pure shell script with zero dependencies beyond `curl` and `jq`.

### Step 0 — Pre-flight permissions

```bash
chmod a+r config.test.yml
find . -name '*.go' -exec chmod a+r {} \+
```

Only `config.test.yml` and `*.go` source files are made world-readable — nothing else. This ensures the container's UID 1000 can read the mounted files regardless of host umask.

### Step 1 — Wait for health

Polls `GET /api/v2/info` until HTTP 200, with a timeout. Reports progress.

### Step 2 — Seed test user

```bash
curl -s -X PUT 'http://127.0.0.1:3456/api/v2/test/users?truncate=true' \
  -H 'Authorization: test-token-for-seeding' \
  -H 'Content-Type: application/json' \
  -d '[{
    "id": 1,
    "username": "testuser",
    "password": "<bcrypt-hash-of-testpassword>",
    "email": "test@localhost.local",
    "issuer": "local",
    "status": 0
  }]'
```

Uses the testingtoken endpoint (v2) to directly insert a user row. No registration flow needed. The password is a pre-computed bcrypt hash of `testpassword`.

### Step 3 — Login

```bash
curl -s -X POST http://127.0.0.1:3456/api/v2/login \
  -H 'Content-Type: application/json' \
  -d '{"username": "testuser", "password": "testpassword"}'
```

Extracts the `token` field from the JSON response using `jq`.

### Step 4 — Print result

Prints the JWT as an exportable shell variable and a ready-to-use curl example.

**Idempotency:** Running the script again replaces the existing user (testingtoken truncates the table first).

**All v2 endpoints:** `info`, `test/users`, and `login` all use v2 paths for consistency and because the plugin API is designed as if it were a `/api/v2/custom-fields` resource.

## Test user

| Field | Value |
|-------|-------|
| Username | `testuser` |
| Password | `testpassword` |
| Email | `test@localhost.local` |
| Admin | **No** — regular user only. Plugin management is gated by the config whitelist (S8), not Vikunja's licensed admin feature. |

## CLAUDE.local.md additions

New "Test Instance" section covering:

- **Quick Start** — the single `./scripts/run-test-env.sh` command
- **Stop** — `docker compose -f compose.test.yml down`
- **Manual Testing** — how to use the JWT with curl, including the distinction between authenticated and unauthenticated endpoints
- **How It Works** — compose, config, bootstrap flow, ephemeral DB
- **The Testing Token Seed Endpoint** — detailed reference on `PUT /api/v2/test/{table}`, auth format, request body format, available tables, dependency handling. Prevents agents from rediscovering this every time they need to modify the bootstrap script.
- **Modifying the Test Harness** — guidance on adding config sections, seeding steps, and changing the image tag. Story-neutral (no references to specific story numbers).
- **Troubleshooting** — permission issues, plugin load failures, port conflicts

## Out of Scope

- CI/CD pipeline integration
- Multi-version testing matrix
- PostgreSQL/MySQL support (SQLite is sufficient for local plugin dev)
- Automated smoke tests
- Plugin config whitelist setup (that's S8's responsibility — the test harness just provides the baseline)
