# S8 Config Whitelist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Read a management whitelist from an env var at plugin startup and expose it as a `IsManager(username) bool` predicate that S2/S9 use to gate field-definition operations.

**Architecture:** Single `main.go` (package `main`, the existing yaegi plugin file) gains a package-level whitelist set populated in `Init()` from `VIKUNJA_CUSTOMFIELDS_WHITELIST`, a lowercase-normalized `IsManager` predicate, and a temporary authenticated route that proves the predicate for the logged-in caller. No fork change; no core Vikunja files modified. Integration-verified via the existing Docker test instance.

**Tech Stack:** Go (yaegi-interpreted plugin), Vikunja 2.5 plugin API (`pkg/user`, `pkg/db`, `pkg/log`, `pkg/plugins`, echo v5), SQLite test instance, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-08-16-s8-config-whitelist-design.md`

## Global Constraints

- **No fork change:** the `vikunja/` repo is untouched. All work is in the plugin repo.
- **No core Vikunja files modified.** Everything lands in the plugin's single `main.go` plus test-harness files.
- **Env var name (verbatim):** `VIKUNJA_CUSTOMFIELDS_WHITELIST` — comma-separated usernames.
- **Imports available to yaegi (verified):** `code.vikunja.io/api/pkg/db`, `code.vikunja.io/api/pkg/log`, `code.vikunja.io/api/pkg/plugins`, `code.vikunja.io/api/pkg/user`, `github.com/labstack/echo/v5`, stdlib. **NOT** `pkg/config`, `xorm`, `xormigrate`, `web`.
- **`db.NewSession()` opens an active transaction** that auto-rolls-back on `Close()` — `Init()` already commits it in S1; S8 adds whitelist parsing **before** the existing table-creation block, within the same session lifecycle. No new session.
- **Deny-by-default:** empty whitelist → `IsManager` returns `false` for everyone (AC#3, AC#5).
- **Case-insensitive comparison:** usernames are lowercased on both sides (set population and lookup).
- **Malformed entry = empty after trim** (e.g. `alice,,bob`, trailing comma) → logged error naming position, skipped, no crash (AC#4).
- **Conventional Commits** for all commits. git-flow: implementation lands on `feature/s8-config-whitelist` off `develop` (the spec + this plan are already committed on `develop`, matching S1's flow).
- **Worktree:** per `CLAUDE.local.md`, the worktree decision is raised with the user before any code work begins. This plan assumes the executor has already resolved that (either working in a worktree or directly on the feature branch).

## Required Skills (per CLAUDE.local.md)

Invoke these before/during the matching tasks:

- **git-flow** — before any branch/commit work (Task 0 branch setup; every commit).
- **golang-testing** — for the test design in Task 4 (table-driven integration verification expectations; the plugin source itself is not unit-testable standalone).
- **golang-error-handling** — Task 1 (malformed-entry logging path) and Task 3 (handler error paths).

## Recommended Skills

- **golang-naming** — `IsManager`, `loadWhitelist`, `managerHandler` naming.
- **golang-code-style** — control-flow clarity in the parsing loop and handler.
- **golang-concurrency** — only to confirm the write-once-then-read-only whitelist needs no synchronization (it doesn't); brief, not load-bearing.

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `main.go` | Plugin: tables, routes, whitelist set, predicate, verification route | Modify |
| `compose.test.yml` | Expose `VIKUNJA_CUSTOMFIELDS_WHITELIST` to the test container | Modify |
| `config.test.yml` | Unchanged — env var is set via compose `environment:`, not the YAML | — |
| `scripts/run-test-env.sh` | Seed a second, non-whitelisted user; print both JWTs | Modify |
| `docs/stories/S8-config-whitelist.md` | Story wording: "config file" → "env var" | Modify |
| `docs/PRD.md` | "Config file — single responsibility" → env var | Modify |
| `docs/stories/S2-field-definition-api.md` | "config whitelist" reference | Modify |
| `docs/stories/S9-management-ui.md` | "config whitelist" reference | Modify |

`main.go` stays a single file (yaegi single-file safest, per S1 spec line 40). The additions are small and cohesive — whitelist parsing, predicate, one handler, one route line — so a file split is not warranted at S8's size.

## Interfaces

**Produces (consumed by S2/S9 later):**
- `func IsManager(username string) bool` — package-level, exported. Lowercases input, checks the set, deny-by-default. S2's field-definition handlers and S9's UI route call this to gate operations.

**Consumes (from S1 / Vikunja):**
- `db.NewSession() *xorm.Session`, `db.GetDialect()` — existing in `main.go` (S1).
- `user.GetCurrentUser(c *echo.Context) (*user.User, error)` — from `code.vikunja.io/api/pkg/user` (new import).
- `log.Errorf` / `log.Infof` — existing import.

---

### Task 0: Branch setup

**Files:** none (git only)

- [ ] **Step 1: Confirm develop is current and clean**

Run: `cd /mnt/data/nickp/Documents/Code/vikunja-projects/vikunja-custom-fields-plugin && git rev-parse --abbrev-ref HEAD && git status --short`
Expected: branch `develop`, no uncommitted changes (the spec `4221e79` and the S1 history are committed).

- [ ] **Step 2: Start the feature branch off develop**

Run: `git flow feature start s8-config-whitelist`
Expected: creates and switches to `feature/s8-config-whitelist` off `develop`.

- [ ] **Step 3: Confirm**

Run: `git rev-parse --abbrev-ref HEAD`
Expected: `feature/s8-config-whitelist`.

---

### Task 1: Whitelist parsing + `IsManager` predicate (the seam)

This task adds the load logic, the set, and the predicate. No route yet — that's Task 2, so the predicate can be reasoned about in isolation. S1's table-creation block stays intact; parsing runs before it in `Init()`.

**Files:**
- Modify: `main.go` (add imports `os`, `strings`, `code.vikunja.io/api/pkg/user`; add `whitelist` var, `loadWhitelist`, `IsManager`; call `loadWhitelist` in `Init`)

**Interfaces:**
- Produces: `func IsManager(username string) bool`
- Consumes: stdlib `os`, `strings`; `code.vikunja.io/api/pkg/log`

- [ ] **Step 1: Add imports and the package-level whitelist set**

In `main.go`, update the import block and add the variable. The current import block is:

```go
import (
	"fmt"
	"net/http"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
	"github.com/labstack/echo/v5"
)
```

Replace with:

```go
import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
	"code.vikunja.io/api/pkg/user"
	"github.com/labstack/echo/v5"
)
```

Then add the package-level set immediately above the `CustomFieldsPlugin` struct definition (after the `CustomFieldValue` / `TableName` block, before `type CustomFieldsPlugin struct{}`):

```go
// whitelist holds the lowercase usernames permitted to manage custom fields.
// Populated once in Init() from the VIKUNJA_CUSTOMFIELDS_WHITELIST env var;
// read-only afterward, so it needs no synchronization.
var whitelist map[string]struct{}
```

- [ ] **Step 2: Add `loadWhitelist`**

Add after the `whitelist` var declaration:

```go
// loadWhitelist reads the management whitelist from the
// VIKUNJA_CUSTOMFIELDS_WHITELIST env var and returns a lowercase-normalized
// set of permitted usernames. Source is isolated here so a future swap to
// pkg/config (if it's ever exposed to yaegi) is a one-function change.
//
// Malformed entries (empty after trimming, e.g. "alice,,bob") are logged and
// skipped — never fatal. An absent/empty var yields an empty set (deny-all).
func loadWhitelist() map[string]struct{} {
	set := map[string]struct{}{}
	raw := os.Getenv("VIKUNJA_CUSTOMFIELDS_WHITELIST")
	if raw == "" {
		log.Infof("[custom-fields] whitelist empty — no users may manage custom fields")
		return set
	}
	for i, entry := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(entry))
		if name == "" {
			log.Errorf("[custom-fields] whitelist: ignoring empty entry at position %d", i)
			continue
		}
		set[name] = struct{}{}
	}
	log.Infof("[custom-fields] whitelist loaded: %d manager(s)", len(set))
	return set
}
```

- [ ] **Step 3: Add `IsManager`**

Add after `loadWhitelist`:

```go
// IsManager reports whether username is on the management whitelist. It is the
// single authorization check S2 (field-definition API) and S9 (management UI)
// call before allowing field-definition changes. Deny-by-default: an empty
// whitelist denies everyone. Comparison is case-insensitive.
func IsManager(username string) bool {
	if username == "" {
		return false
	}
	_, ok := whitelist[strings.ToLower(username)]
	return ok
}
```

- [ ] **Step 4: Call `loadWhitelist` in `Init()` before table creation**

In `Init()`, the current first lines are:

```go
func (p *CustomFieldsPlugin) Init() error {
	s := db.NewSession()
	defer s.Close()
```

Add the whitelist load right after `defer s.Close()` and before the `switch db.GetDialect()`:

```go
	whitelist = loadWhitelist()
```

(The whitelist is config, not DB — parsing it inside the session block is fine; it has no session dependency. Placing it before table creation keeps "governance setup" logically first.)

- [ ] **Step 5: Rebuild-free sanity check — compile mentally / re-read**

There is no standalone `go build` for the plugin (imports resolve only inside the vikunja module). Verification is integration (Task 4). Here, just re-read `main.go` to confirm: imports added, `whitelist` var declared, `loadWhitelist` and `IsManager` present, `Init()` calls `loadWhitelist()` before the dialect switch, and the S1 table-creation block is unchanged.

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "feat: parse management whitelist from env var and expose IsManager"
```

---

### Task 2: Temporary verification route

Adds the authenticated `/manager` route that proves the predicate resolves for the logged-in caller. Its doc comment records that it is temporary and must be removed once S2 exercises the predicate.

**Files:**
- Modify: `main.go` (add `managerHandler`, register the route in `RegisterAuthenticatedRoutes`)

**Interfaces:**
- Consumes: `IsManager` (Task 1), `user.GetCurrentUser`, `echo.Context`
- Produces: `GET /api/v1/plugins/custom-fields/manager` → `{ "username": string, "is_manager": bool }`

- [ ] **Step 1: Add the handler**

Add after the existing `healthHandler`:

```go
// managerHandler is a temporary S8 verification route: it proves the whitelist
// predicate resolves correctly for the authenticated caller. It is not a
// management surface — S2/S9 enforce IsManager on the real endpoints. Remove
// this route once S2 is in place and the predicate is exercised there.
func managerHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"username":   u.Username,
		"is_manager": IsManager(u.Username),
	})
}
```

- [ ] **Step 2: Register the route**

The current `RegisterAuthenticatedRoutes` is:

```go
func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/health", healthHandler)
}
```

Replace with:

```go
func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/health", healthHandler)   // S1 throwaway load-proof
	g.GET("/custom-fields/manager", managerHandler) // S8 temporary, remove after S2
}
```

- [ ] **Step 3: Re-read for consistency**

Confirm `managerHandler` is defined, the route is registered, `user` and `http` are imported (Task 1 added `user`; `http` was already imported in S1). No other changes needed.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: add temporary S8 whitelist verification route"
```

---

### Task 3: Wire the env var into the test harness

Sets `VIKUNJA_CUSTOMFIELDS_WHITELIST` for the test container so the harness exercises a populated whitelist. The whitelisted user is `testuser` (the existing seed). A second, non-whitelisted user is added in Task 4's harness update.

**Files:**
- Modify: `compose.test.yml` (add `environment:` block)

- [ ] **Step 1: Add the environment block**

The current `compose.test.yml` is:

```yaml
services:
  vikunja:
    image: vikunja/vikunja:2.5
    ports:
      - "127.0.0.1:4176:3456"
    volumes:
      - ./:/app/vikunja/plugins/custom-fields
      - ./config.test.yml:/etc/vikunja/config.yml:ro
      - ./db:/db
    restart: "no"
```

Replace with (add the `environment:` block after `volumes:`):

```yaml
services:
  vikunja:
    image: vikunja/vikunja:2.5
    ports:
      - "127.0.0.1:4176:3456"
    volumes:
      - ./:/app/vikunja/plugins/custom-fields
      - ./config.test.yml:/etc/vikunja/config.yml:ro
      - ./db:/db
    environment:
      - VIKUNJA_CUSTOMFIELDS_WHITELIST=testuser
    restart: "no"
```

Only `testuser` is whitelisted. The second seed user (`otheruser`, added in Task 4) is intentionally **off** the whitelist and is used for the deny assertion in Task 5. (Task 5's malformed-entry probe temporarily adds `otheruser` to the env var to confirm a skipped empty entry doesn't prevent valid neighbors from being recognized — that's a throwaway edit, reverted in the same task.)

- [ ] **Step 2: Commit**

```bash
git add compose.test.yml
git commit -m "test(docker): set VIKUNJA_CUSTOMFIELDS_WHITELIST for test instance"
```

---

### Task 4: Seed a second, non-whitelisted test user

Adds a second user (`otheruser`, not on the whitelist) to the seed step and logs both in, printing both JWTs so the deny path can be exercised in Task 5.

**Files:**
- Modify: `scripts/run-test-env.sh`

**Interfaces:**
- Produces: `JWT_TESTUSER` and `JWT_OTHERUSER` printed for the operator; the `/manager` deny path is exercised with `JWT_OTHERUSER` in Task 5.

- [ ] **Step 1: Extend the seed payload to two users**

The current Step 3 block seeds one user. Replace the `SEED_RESPONSE=$(curl ...)` block with a two-user seed (same bcrypt hash, different username/email/id):

```bash
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
```

(The bcrypt hash is reused — both users share password `testpassword`. `otheruser` is deliberately **not** on the whitelist set in `compose.test.yml`.)

- [ ] **Step 2: Log in both users and capture both JWTs**

Replace the existing single-user Step 4 (`# ── Step 4: Login ──` block through the `JWT=` extraction) with:

```bash
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
```

- [ ] **Step 3: Update the printed result banner to show both JWTs**

Replace the Step 5 banner block with a version that prints both tokens:

```bash
# ── Step 5: Print result ────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Vikunja test instance ready                                 ║"
echo "║                                                              ║"
echo "║  Whitelisted manager (is_manager should be true):            ║"
echo "║    export JWT=\"$JWT\"                                       ║"
echo "║                                                              ║"
echo "║  Non-whitelisted user (is_manager should be false):          ║"
echo "║    export JWT_OTHERUSER=\"$JWT_OTHERUSER\"                   ║"
echo "║                                                              ║"
echo "║  Verify the whitelist:                                       ║"
echo "║    curl -s -H \"Authorization: Bearer \$JWT\" \\               ║"
echo "║      $BASE_URL/api/v1/plugins/custom-fields/manager | jq .  ║"
echo "║    curl -s -H \"Authorization: Bearer \$JWT_OTHERUSER\" \\     ║"
echo "║      $BASE_URL/api/v1/plugins/custom-fields/manager | jq .  ║"
echo "║                                                              ║"
echo "║  Stop: docker compose -f compose.test.yml down               ║"
echo "╚══════════════════════════════════════════════════════════════╝"
```

- [ ] **Step 4: Commit**

```bash
git add scripts/run-test-env.sh
git commit -m "test: seed second non-whitelisted user and print both JWTs"
```

---

### Task 5: Integration verification (all ACs)

Runs the test harness and exercises every AC against the live instance. This task is verification, not code — but it carries its own test cycle (each AC is an assertion), so it is a task, not a step.

**Files:** none (verification only). Outputs are read from logs and `curl`/`jq`.

- [ ] **Step 1: Start the instance and capture tokens**

Run: `./scripts/run-test-env.sh`
Expected: prints both `JWT` and `JWT_OTHERUSER`. Export them in your shell as the script instructs.

- [ ] **Step 2: AC#1 + startup log — whitelist declared and loaded**

Check the container startup log for the loaded-count line.

Run: `docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i "whitelist"`
Expected: a line `[custom-fields] whitelist loaded: 1 manager(s)` (only `testuser` is whitelisted, per `compose.test.yml`).

- [ ] **Step 3: AC#2 — whitelisted user allowed**

Run: `curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/manager | jq .`
Expected: `{ "username": "testuser", "is_manager": true }`.

- [ ] **Step 4: AC#3 — non-whitelisted user denied (predicate)**

Run: `curl -s -H "Authorization: Bearer $JWT_OTHERUSER" http://127.0.0.1:4176/api/v1/plugins/custom-fields/manager | jq .`
Expected: `{ "username": "otheruser", "is_manager": false }`. (Enforcement on field-definition ops is S2/S9 — out of S8 scope; this proves the predicate itself.)

- [ ] **Step 5: AC#4 — malformed entry logs error, no crash**

Temporarily set a malformed whitelist and restart.

Run:
```bash
# Edit compose.test.yml environment to: VIKUNJA_CUSTOMFIELDS_WHITELIST=testuser,,otheruser
# then restart:
docker compose -f compose.test.yml down
docker compose -f compose.test.yml up -d
# wait for health:
until curl -sf -o /dev/null http://127.0.0.1:4176/api/v2/info; do sleep 2; done
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i "whitelist"
```
Expected: a line `[custom-fields] whitelist: ignoring empty entry at position 1` AND a line `[custom-fields] whitelist loaded: 2 manager(s)` (both `testuser` and `otheruser` now counted). Vikunja is healthy (the `/api/v2/info` probe succeeded) — no crash.

Then **revert** the `compose.test.yml` change back to `VIKUNJA_CUSTOMFIELDS_WHITELIST=testuser` and restart again for subsequent steps:
```bash
docker compose -f compose.test.yml down
docker compose -f compose.test.yml up -d
until curl -sf -o /dev/null http://127.0.0.1:4176/api/v2/info; do sleep 2; done
```
Do not commit the malformed-value edit — it was a temporary verification probe. After reverting, `git diff compose.test.yml` should show no changes.

- [ ] **Step 6: AC#5 — absent whitelist → empty, no crash**

Temporarily unset the env var entirely and restart.

Run:
```bash
# Temporarily comment out / remove the environment block in compose.test.yml, then:
docker compose -f compose.test.yml down
docker compose -f compose.test.yml up -d
until curl -sf -o /dev/null http://127.0.0.1:4176/api/v2/info; do sleep 2; done
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i "whitelist"
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/manager | jq .
```
Expected: a line `[custom-fields] whitelist empty — no users may manage custom fields`, and the `/manager` route returns `{ "is_manager": false }` for `testuser` too. Vikunja is healthy — no crash.

Then **restore** the `environment:` block with `VIKUNJA_CUSTOMFIELDS_WHITELIST=testuser` and restart for the doc tasks. Again, `git diff compose.test.yml` should be clean afterward.

- [ ] **Step 7: AC#6 note**

AC#6 ("shared with both S2 and S9") is **not verifiable in S8 alone** — it is proven when S2 imports and calls `IsManager`. No action here beyond recording this in the S8 story's status notes during Task 6. This step is a checkpoint, not a command.

- [ ] **Step 8: Commit (if any harness artifact changed)**

Only commit if Steps 5/6 left a real harness change (they shouldn't — both were reverted). If `git status --short` is clean, skip. If a genuinely intended harness fix emerged during verification, commit it as `test: ...`.

---

### Task 6: Doc updates — "config file" → "env var" consistency

Updates the story, PRD, and S2/S9 references so docs match the env-var mechanism. The whitelist *concept* and *name* are unchanged; only the *mechanism* wording changes.

**Files:**
- Modify: `docs/stories/S8-config-whitelist.md`
- Modify: `docs/PRD.md`
- Modify: `docs/stories/S2-field-definition-api.md`
- Modify: `docs/stories/S9-management-ui.md`

- [ ] **Step 1: S8 story**

In `docs/stories/S8-config-whitelist.md`:

- Outcome, line 14: `A whitelist of usernames (or user identifiers) in Vikunja's config file defines...` → `A whitelist of usernames in the VIKUNJA_CUSTOMFIELDS_WHITELIST environment variable defines...`
- What & Why, line 18: `The config file — freely editable by any instance admin — is the natural license-free surface...` → `An environment variable — freely set by any instance admin — is the natural license-free surface...` and `This story makes the config file hold a single thing: the whitelist...` → `This story exposes a single thing: the whitelist...`
- AC#1, line 36: `A whitelist of permitted users can be declared in Vikunja's config file.` → `A whitelist of permitted users can be declared via the VIKUNJA_CUSTOMFIELDS_WHITELIST environment variable.`
- AC#4, line 39: `A malformed whitelist entry logs a clear error on startup but does not crash Vikunja.` → keep (still accurate — malformed = empty-after-trim entry in the comma list).
- AC#5, line 40: `Vikunja starts successfully when the whitelist is absent (empty — no users may manage fields).` → keep (still accurate).
- Scope, line 47: `Config schema for the management whitelist` → `Env-var schema for the management whitelist`.

Keep the title `Config Whitelist` — the concept is unchanged; only the mechanism did.

- [ ] **Step 2: PRD**

In `docs/PRD.md`:

- Line 51, the "Config file" bullet under Architecture: `**Config file** — A single responsibility: the whitelist of users permitted to manage custom fields. It is not a source of field definitions.` → `**Config** — A single responsibility: the whitelist of users permitted to manage custom fields, declared via the VIKUNJA_CUSTOMFIELDS_WHITELIST env var. It is not a source of field definitions.`
- Any "config-declared whitelist" / "config whitelist" phrasings (lines 21, 31, 47, etc.) → "env-var-declared whitelist" / "whitelist" where the mechanism is the point; leave "whitelist" alone where only the concept matters.

- [ ] **Step 3: S2 story**

In `docs/stories/S2-field-definition-api.md`:

- Line 20: `Access is governed by the config whitelist (S8)` → `Access is governed by the management whitelist (S8, read from the VIKUNJA_CUSTOMFIELDS_WHITELIST env var)`.
- Dependencies / scope references to "config whitelist" → "whitelist (S8)".

- [ ] **Step 4: S9 story**

In `docs/stories/S9-management-ui.md`:

- Line 14: `A whitelisted user (from the config whitelist, S8) can manage...` → `A whitelisted user (from the management whitelist, S8) can manage...`
- Line 20: `gated by the config whitelist (S8)` → `gated by the management whitelist (S8)`.
- Line 24 (Design Principles): `the UI is the whitelisted user's tool; it checks the whitelist (S8)...` → keep "whitelist (S8)" (concept, not mechanism).

- [ ] **Step 5: Verify no stray "config file" mechanism references remain**

Run: `cd /mnt/data/nickp/Documents/Code/vikunja-projects/vikunja-custom-fields-plugin && grep -rn "config file" docs/stories/S8-config-whitelist.md docs/PRD.md docs/stories/S2-field-definition-api.md docs/stories/S9-management-ui.md`
Expected: no matches referring to the whitelist *mechanism* (the PRD's `config.yml.sample` / config-package references, if any, are unrelated and fine; the whitelist mechanism should read "env var" or "whitelist" everywhere).

- [ ] **Step 6: Commit**

```bash
git add docs/stories/S8-config-whitelist.md docs/PRD.md docs/stories/S2-field-definition-api.md docs/stories/S9-management-ui.md
git commit -m "docs: switch whitelist mechanism wording from config file to env var"
```

---

### Task 7: Finish the feature branch

**Files:** none (git only)

- [ ] **Step 1: Confirm all verification passed and the tree is clean**

Run: `git status --short`
Expected: clean (any temporary `compose.test.yml` edits from Task 5 were reverted).

- [ ] **Step 2: Finish the feature branch into develop**

Run: `git flow feature finish s8-config-whitelist`
Expected: merges `feature/s8-config-whitelist` → `develop`, deletes the feature branch, switches to `develop`.

- [ ] **Step 3: Confirm**

Run: `git rev-parse --abbrev-ref HEAD && git log --oneline -8`
Expected: on `develop`; the S8 commits (whitelist parse, route, harness, docs) appear in history above the spec commit `4221e79`.

- [ ] **Step 4: Update the critical-path checklist**

In `docs/stories/story-dependency-graph.md`, mark S8 complete on the critical-path checklist:

```
- [x] **S8** — Config Whitelist
```

Commit:
```bash
git add docs/stories/story-dependency-graph.md
git commit -m "docs: mark S8 config whitelist complete on critical path"
```

---

## Verification matrix (AC → task)

| AC | Task | Step |
|---|---|---|
| 1. Whitelist declarable | 5 | 2 |
| 2. Whitelisted → allowed | 5 | 3 |
| 3. Non-whitelisted → denied | 5 | 4 |
| 4. Malformed entry → error, no crash | 5 | 5 |
| 5. Absent → empty, no crash | 5 | 6 |
| 6. Shared with S2/S9 | (S2's verification, not S8's) | 7 |

## Removal reminder (tracked debt)

The temporary `/custom-fields/manager` route and `managerHandler` (Task 2) are removed once S2 enforces `IsManager` on its field-definition CRUD endpoints and the predicate's allow/deny is exercised there. The handler's doc comment records both facts. This is tracked as a removal step in S2's plan, not left as silent debt.
