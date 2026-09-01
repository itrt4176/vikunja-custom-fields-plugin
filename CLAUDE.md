# CLAUDE.md

## Git Workflow

This project uses the **Git Flow** branching model (feature, release, and hotfix
branches off `develop`/`main`). Use it for all branching work.

### Commits

Use the **Conventional Commits** style when committing changes (for example,
`feat: add foo` or `fix: correct bar`).

## Architecture

The plugin is a single file, `main.go`, interpreted live by yaegi (no compiled
build step — see Test Instance). It holds:

- **xorm data models:** `CustomFieldDefinition`, `CustomFieldValue`,
  `CustomFieldOption`, `CustomFieldValueOption`, `CustomFieldProject`.
- **Permission methods:** `CanCreate`/`CanRead`/`CanUpdate`/`CanDelete` on the
  definition and value types. Management is whitelist-gated; value access
  delegates to the host `models.Task.CanRead`/`CanUpdate`.
- **CRUD:** `Create`/`ReadOne`/`ReadAll` plus helpers `setOptions`,
  `setAssignment`, `validateAssignment`.
- **HTTP handlers** that obtain an `*xorm.Session` via `db.NewSession()` and
  pass it to the methods above.

The feature breakdown lives in `docs/stories/` (S1–S9) with a dependency graph
in `docs/stories/story-dependency-graph.md`.

### Yaegi symbol-table constraint (non-obvious)

Vikunja's `web` package (`web.Auth`, `web.HTTPError`) is **not** in the yaegi
symbol table, although `xorm`, the `db` package, `models`, `user`, and `echo`
all are. Two upstream-conversion seams follow from the missing `web` package:

- The `Can*` permission methods take `*user.User` instead of `web.Auth` (yaegi
  accepts `*user.User` where `web.Auth` is expected at runtime). Upstream:
  restore `web.Auth`.
- Errors use **plugin-local 9000s codes** translated to `echo.NewHTTPError`
  instead of `web.HTTPError`/`HTTPError()`. Upstream: switch to the host
  `HTTPError()`/`ErrCode` convention (source `vikunja/pkg/models/error.go`;
  doc https://vikunja.io/docs/custom-errors/).

Sessions are **not** a workaround: `db.NewSession()` is available in the symbol
table and handlers call it normally, passing `*xorm.Session` into the
CRUD/permission methods. (The host symbol table lives at
`vikunja/pkg/yaegi_symbols/` — check it before assuming any Vikunja symbol is
unavailable to the plugin.)

## Story Resolution

When you complete work that delivers a story, you **MUST** add a `## Resolution`
section to the story doc before finishing the branch. Think of it as the durable
summary of the progress ledger — since any ephemeral scratch workspace is deleted
when the branch finishes, the Resolution section is the durable record that
survives.

The section should cover:

- **Status:** Done (or the actual outcome) + which acceptance criteria passed
  and how they were verified. Flip the front-matter `status:` to match
  (`done`, or `rejected` per the story-rejection convention if the story was
  abandoned).
- **How it was built:** a brief description of the implementation — the
  files/tables/endpoints produced, keyed decisions from the spec.
- **Notable deviations:** anything the implementation diverged in from the
  original design, **with the reason** (e.g. a spike found a yaegi limit; a
  ruling deferred a seam). Each deviation should say *why* it diverged, not just
  *that* it did.
- **Key decisions:** the load-bearing design choices, grounded in their
  upstream evidence (the spec's "Context — decisions were grounded in upstream
  evidence" section is the source).
- **What was left open:** deferred work, known issues left as-is, and items
  deferred to a later story. Each item **MUST be self-contained** — name the
  function/file (stable across line drift, since scratch ledgers are gone), what
  it is, why it was left, and what would fix it. Do **not** point at ephemeral
  artifacts — the Resolution section is the durable replacement for them.

The story doc + the spec + git history are the durable record. The Resolution
section is what makes the story doc readable on its own after a workflow's
scratch artifacts are gone.

After updating the story doc, you **MUST** also check-off the story in
`docs/story-dependency-graph.md`.

## Test Instance

A Docker-based Vikunja 2.6 instance for local plugin development.

### Quick Start

```
./scripts/run-test-env.sh
```

This starts the container, waits for it to be healthy, creates a test user, logs
in, and prints a ready-to-use JWT.

### Stop

```
docker compose -f compose.test.yml down
```

### Manual Testing

Once you have a JWT, use it with authenticated endpoints:

```
curl -s -H "Authorization: Bearer $JWT" \
  http://127.0.0.1:4176/api/v1/your/api/endpoint | jq .
```

(Use without the Authorization header for unauthenticated endpoints like
`/api/v2/info`.)

> **Port, image tag, JWT secret, and testing token** are all defined in the
> committed config files — see `compose.test.yml` and `config.test.yml` for the
> authoritative values rather than relying on this doc.

### How It Works

- `compose.test.yml` starts the stock `vikunja/vikunja` image with the plugin
  source mounted live at `/app/vikunja/plugins/custom-fields` — edit `main.go`,
  restart the container, changes take effect. The `/db` directory is a host bind
  mount (`./db:/db`), so the SQLite database is directly readable on the host
  with `sqlite3 db/vikunja.db`.
- `config.test.yml` enables the yaegi plugin loader, sets a stable JWT secret,
  and enables the testingtoken seed endpoint.
- `run-test-env.sh` seeds a regular (non-admin) test user via the testingtoken
  endpoint and logs in to get a JWT.
- The DB persists in the host `./db/` directory (bind mount) and is wiped fresh
  on each `./scripts/run-test-env.sh` run; `docker compose -f
  compose.test.yml down` no longer destroys it (it's wiped at next startup
  instead).

### Reload After Editing

There is no compiled build — the plugin is interpreted live by yaegi from the
mounted source. After editing `main.go`, apply changes by restarting the
container:

```
docker compose -f compose.test.yml restart
```

Confirm the plugin loaded (look for "Loaded plugin" or an error):

```
docker compose -f compose.test.yml logs | grep -i "loaded plugin"
```

### The Testing Token Seed Endpoint

`config.test.yml` sets `service.testingtoken`, which exposes database seed
endpoints. These bypass normal auth — authorization is the raw token string,
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

Request body for `PUT /api/v2/test/{table}`:
A JSON array of objects, each object is a row with column names as keys.
Example — seeding a user:

```
curl -s -X PUT 'http://127.0.0.1:4176/api/v2/test/users?truncate=true' \
  -H 'Authorization: <raw-testing-token-from-config.test.yml>' \
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

Available tables include: users, projects, tasks, labels, teams, and all other
Vikunja database tables. See the Vikunja source at `pkg/db/fixtures/` for column
names and example data.

The seeding user table ("users") has a dependency on "notifications" — the
endpoint handles this automatically (clears notifications when users are
truncated).

### Modifying the Test Harness

As the plugin evolves, you may need to:
- Add config sections to `config.test.yml` as the plugin's configuration needs
  grow.
- Add seeding steps to `run-test-env.sh` — for example, creating test projects
  or pre-populating data via additional PUT calls to `/api/v2/test/{table}`.
- Change the Vikunja image tag in `compose.test.yml` to test against a different
  version.

### Troubleshooting

- Container fails to read mounted files: `chmod a+r` on `config.test.yml` and
  the plugin `.go` source files, then restart.
- Plugin not loading: check logs with `docker compose -f compose.test.yml logs`
  and look for "Loaded plugin" or error messages.
- Port already in use: ensure nothing else is running on the configured port.
