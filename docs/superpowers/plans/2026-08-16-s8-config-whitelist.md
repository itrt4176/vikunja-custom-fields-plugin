# S8 Config Whitelist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Amended 2026-08-29:** mechanism changed from `os.Getenv` (infeasible under
> yaegi) to `viper.GetString("customfields.whitelist")` (Vikunja's native config).
> Base changed to current `develop` (xormigrate migration in S1; trivial `Init()`).
> See the spec's amendment note.

**Goal:** Read a management whitelist from Vikunja's config (`customfields.whitelist`, overridable by the `VIKUNJA_CUSTOMFIELDS_WHITELIST` env var) at plugin startup and expose it as an `IsManager(username) bool` predicate that S2/S9 use to gate field-definition operations.

**Architecture:** Single `main.go` (package `main`, the existing yaegi plugin file) gains a package-level whitelist set populated in `Init()` via `viper.GetString`, a lowercase-normalized `IsManager` predicate, and a temporary authenticated route that proves the predicate for the logged-in caller. No per-feature fork change (the `config`/`viper` symbol-table exposure is upstream PR #3502, backported to the test image); no core Vikunja files modified. Integration-verified via the existing Docker test instance.

**Tech Stack:** Go (yaegi-interpreted plugin), Vikunja 2.5 plugin API (`pkg/user`, `pkg/log`, `pkg/plugins`, `github.com/spf13/viper`, echo v5, `xorm`/`xormigate` for the S1 migration), SQLite test instance, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-08-16-s8-config-whitelist-design.md`

## Global Constraints

- **No per-feature fork change:** the `vikunja/` repo is untouched by S8. The `pkg/config` + `viper` (PR #3502) and `xorm` + `xormigate` (PR #3549) symbol-table exposures are upstream, backported to the test image `itrt4176/vikunja:2.5-plugin-fix-backport` — not S8's work.
- **No core Vikunja files modified.** Everything lands in the plugin's single `main.go` plus test-harness files.
- **Config key (verbatim):** `customfields.whitelist` — comma-separated usernames, under a `customfields:` section in `config.yml`. Env override (verbatim): `VIKUNJA_CUSTOMFIELDS_WHITELIST`.
- **Source accessor (verbatim):** `viper.GetString("customfields.whitelist")` — reads Vikunja's global viper (the same instance `pkg/config` loads); `AutomaticEnv` makes `VIKUNJA_CUSTOMFIELDS_WHITELIST` override it.
- **Imports available to yaegi (verified, post-backport):** `code.vikunja.io/api/pkg/log`, `code.vikunja.io/api/pkg/plugins`, `code.vikunja.io/api/pkg/user`, `code.vikunja.io/api/pkg/config`, `github.com/spf13/viper`, `github.com/labstack/echo/v5`, `xorm.io/xorm`, `src.techknowlogick.com/xormigrate`, stdlib. (S8 uses `viper`, not `config`, for directness.)
- **`Init()` no longer opens a DB session** on the new base — S1's tables are created by the `xormigrate` migration, so `Init()` is now trivial. S8 adds `whitelist = loadWhitelist()` as the first line of `Init()`; no session lifecycle involved.
- **Deny-by-default:** empty whitelist → `IsManager` returns `false` for everyone (AC#3, AC#5).
- **Case-insensitive comparison:** usernames are lowercased on both sides (set population and lookup).
- **Malformed entry = empty after trim** (e.g. `alice,,bob`, trailing comma) → logged error naming position, skipped, no crash (AC#4).
- **Conventional Commits** for all commits. git-flow: implementation lands on `feature/s8-config-whitelist` off `develop` (the spec + this plan are committed on `develop`, matching S1's flow).
- **Worktree:** resolved with the user — no worktree; work directly on the feature branch.

## Required Skills (per CLAUDE.local.md)

Invoke these before/during the matching tasks:

- **git-flow** — before any branch/commit work (Task 0 branch setup; every commit).
- **golang-error-handling** — Task 1 (malformed-entry logging path) and Task 2 (handler error paths).
- **golang-testing** — for the test design in Task 5 (per-AC integration assertions; the plugin source itself is not unit-testable standalone).

## Recommended Skills

- **golang-naming** — `IsManager`, `loadWhitelist`, `managerHandler` naming.
- **golang-code-style** — control-flow clarity in the parsing loop and handler.
- **golang-concurrency** — only to confirm the write-once-then-read-only whitelist needs no synchronization (it doesn't); brief, not load-bearing.

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `main.go` | Plugin: tables (via migration), routes, whitelist set, predicate, verification route | Modify |
| `config.test.yml` | Declare `customfields.whitelist` for the test instance | Modify |
| `compose.test.yml` | Unchanged — whitelist lives in config.test.yml; env override used only as a temporary AC#4 probe | — |
| `scripts/run-test-env.sh` | Seed a second, non-whitelisted user; print both JWTs | Modify |
| `docs/stories/S8-config-whitelist.md` | Story wording: name the `customfields.whitelist` key + env override | Modify |
| `docs/PRD.md` | "Config file" bullet: name the key + env override | Modify |
| `docs/stories/S2-field-definition-api.md` | "whitelist (S8)" reference | Modify |
| `docs/stories/S9-management-ui.md` | "whitelist (S8)" reference | Modify |

`main.go` stays a single file (yaegi single-file safest, per S1 spec line 40). The additions are small and cohesive — whitelist parsing, predicate, one handler, one route line — so a file split is not warranted at S8's size.

## Interfaces

**Produces (consumed by S2/S9 later):**
- `func IsManager(username string) bool` — package-level, exported. Lowercases input, checks the set, deny-by-default. S2's field-definition handlers and S9's UI route call this to gate operations.

**Consumes (from S1 / Vikunja):**
- `viper.GetString(key string) string` — from `github.com/spf13/viper` (new import). Reads Vikunja's global loaded config.
- `user.GetCurrentUser(c *echo.Context) (*user.User, error)` — from `code.vikunja.io/api/pkg/user` (new import).
- `log.Errorf` / `log.Infof` — existing import.

---

### Task 0: Branch setup

**Files:** none (git only)

- [ ] **Step 1: Confirm the base**

Run: `cd /mnt/data/nickp/Documents/Code/vikunja-projects/vikunja-custom-fields-plugin && git rev-parse --abbrev-ref HEAD && git status --short`
Expected: on `feature/s8-config-whitelist` (already reset onto current `develop` by the controller), clean tree. `develop` HEAD is `4b2e3ff` (xormigrate refactor). Confirm `git log --oneline -1 develop` shows the xormigrate commit.

- [ ] **Step 2: Confirm the branch is based on the new base**

Run: `git log --oneline -3`
Expected: the xormigrate refactor commit `4b2e3ff` and the custom-image commit `fb5e64d` are in history (the branch sits on current `develop`). No stale `os.Getenv`/raw-SQL commits from the first pass remain.

---

### Task 1: Whitelist parsing + `IsManager` predicate (the seam)

This task adds the load logic, the set, and the predicate. No route yet — that's Task 2, so the predicate can be reasoned about in isolation. The new base's `Init()` is trivial (tables are created by the migration); S8 prepends the whitelist load.

**Files:**
- Modify: `main.go` (add imports `strings`, `github.com/spf13/viper`; add `whitelist` var, `loadWhitelist`, `IsManager`; call `loadWhitelist` in `Init`). Note: the `code.vikunja.io/api/pkg/user` import is NOT added here — it is unused until Task 2's `managerHandler`, and Go rejects unused imports (yaegi type-checks via `go/types`, so an unused import fails plugin load at import time). Task 2 adds `user` where it is first used.

**Interfaces:**
- Produces: `func IsManager(username string) bool`
- Consumes: stdlib `strings`; `github.com/spf13/viper`; `code.vikunja.io/api/pkg/log`

**Required Skills:** git-flow, golang-error-handling. **Recommended:** golang-naming, golang-code-style.

- [ ] **Step 1: Add imports and the package-level whitelist set**

The current (develop) import block is:

```go
import (
	"fmt"
	"net/http"
	"time"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"

	"github.com/labstack/echo/v5"
	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)
```

Replace with:

```go
import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"

	"github.com/labstack/echo/v5"
	"github.com/spf13/viper"
	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)
```

(Note: `code.vikunja.io/api/pkg/user` is NOT added in this task — it is unused until Task 2's `managerHandler`, and Go rejects unused imports. Task 2 adds it where it is first used.)

Then add the package-level set immediately above the `CustomFieldsPlugin` struct definition (after the `CustomFieldValue` / `TableName` block, before `type CustomFieldsPlugin struct{}`):

```go
// whitelist holds the lowercase usernames permitted to manage custom fields.
// Populated once in Init() from Vikunja's config (customfields.whitelist,
// overridable by the VIKUNJA_CUSTOMFIELDS_WHITELIST env var); read-only
// afterward, so it needs no synchronization.
var whitelist map[string]struct{}
```

- [ ] **Step 2: Add `loadWhitelist`**

Add after the `whitelist` var declaration:

```go
// loadWhitelist reads the management whitelist from Vikunja's config
// (the customfields.whitelist key, overridable by the VIKUNJA_CUSTOMFIELDS_WHITELIST
// env var) and returns a lowercase-normalized set of permitted usernames. Source
// is isolated here so a future swap to config.Key(...) is a one-function change.
//
// Malformed entries (empty after trimming, e.g. "alice,,bob") are logged and
// skipped — never fatal. An absent/empty value yields an empty set (deny-all).
func loadWhitelist() map[string]struct{} {
	set := map[string]struct{}{}
	raw := viper.GetString("customfields.whitelist")
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

- [ ] **Step 4: Call `loadWhitelist` in `Init()`**

The current (develop) `Init()` is:

```go
func (p *CustomFieldsPlugin) Init() error {
	log.Infof("[custom-fields] plugin v0.1.0 initialized")
	return nil
}
```

Replace with:

```go
func (p *CustomFieldsPlugin) Init() error {
	whitelist = loadWhitelist()
	log.Infof("[custom-fields] plugin v0.1.0 initialized")
	return nil
}
```

(The whitelist is config, not DB. `Init()` no longer opens a session on this base — tables come from the migration — so the load is the first thing `Init()` does. No session lifecycle to worry about.)

- [ ] **Step 5: Re-read sanity check**

There is no standalone `go build` for the plugin (imports resolve only inside the vikunja module). Re-read `main.go` to confirm: imports added (`strings`, `viper`) — and **no unused `user` import** (it is added in Task 2, not here), `whitelist` var declared, `loadWhitelist` and `IsManager` present, `Init()` calls `loadWhitelist()` first, and the S1 migration block / struct definitions / other factories are unchanged.

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "feat: parse management whitelist from Vikunja config and expose IsManager"
```

---

### Task 2: Temporary verification route

Adds the authenticated `/manager` route that proves the predicate resolves for the logged-in caller. Its doc comment records that it is temporary and must be removed once S2 exercises the predicate.

**Files:**
- Modify: `main.go` (add the `code.vikunja.io/api/pkg/user` import — first use; add `managerHandler`; register the route in `RegisterAuthenticatedRoutes`)

**Interfaces:**
- Consumes: `IsManager` (Task 1), `user.GetCurrentUser`, `echo.Context`
- Produces: `GET /api/v1/plugins/custom-fields/manager` → `{ "username": string, "is_manager": bool }`

**Required Skills:** git-flow, golang-error-handling. **Recommended:** golang-naming, golang-code-style.

- [ ] **Step 1: Add the `user` import**

Task 1 deliberately did not add `code.vikunja.io/api/pkg/user` (it was unused there and Go rejects unused imports). This task's `managerHandler` is the first use, so add it now. The current import block (after Task 1) is:

```go
import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"

	"github.com/labstack/echo/v5"
	"github.com/spf13/viper"
	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)
```

Add `"code.vikunja.io/api/pkg/user"` to the vikunja import group (after `plugins`):

```go
import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
	"github.com/spf13/viper"
	"src.techknowlogick.com/xormigate"
	"xorm.io/xorm"
)
```

- [ ] **Step 2: Add the handler**

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

- [ ] **Step 3: Register the route**

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

- [ ] **Step 4: Re-read for consistency**

Confirm `managerHandler` is defined, the route is registered, and `user` is now imported (Step 1 added it — first use here) alongside `http` (already imported in S1). No other changes needed.

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "feat: add temporary S8 whitelist verification route"
```

---

### Task 3: Declare the whitelist in the test config

Sets `customfields.whitelist` in `config.test.yml` so the harness exercises a populated whitelist read from the config file (the documented mechanism). The whitelisted user is `testuser` (the existing seed). A second, non-whitelisted user is added in Task 4's harness update. `compose.test.yml` is NOT modified — the env override is used only as a temporary AC#4 probe in Task 5.

**Files:**
- Modify: `config.test.yml` (add a `customfields:` section)

**Required Skills:** git-flow. **Recommended:** golang-code-style.

- [ ] **Step 1: Add the `customfields` section**

The current `config.test.yml` ends with:

```yaml
ratelimit:
  enabled: false
```

Append a `customfields` section (top-level key, after `ratelimit`):

```yaml
ratelimit:
  enabled: false

customfields:
  whitelist: "testuser"
```

Only `testuser` is whitelisted. The second seed user (`otheruser`, added in Task 4) is intentionally **off** the whitelist and is used for the deny assertion in Task 5. (Task 5's malformed-entry probe temporarily sets the `VIKUNJA_CUSTOMFIELDS_WHITELIST` env override to `testuser,,otheruser` to confirm a skipped empty entry doesn't prevent valid neighbors from being recognized — that's a throwaway compose edit, reverted in the same task.)

- [ ] **Step 2: Commit**

```bash
git add config.test.yml
git commit -m "test: declare customfields.whitelist for the test instance"
```

---

### Task 4: Seed a second, non-whitelisted test user

Adds a second user (`otheruser`, not on the whitelist) to the seed step and logs both in, printing both JWTs so the deny path can be exercised in Task 5.

**Files:**
- Modify: `scripts/run-test-env.sh`

**Interfaces:**
- Produces: `JWT` and `JWT_OTHERUSER` printed for the operator; the `/manager` deny path is exercised with `JWT_OTHERUSER` in Task 5.

**Required Skills:** git-flow, golang-testing (expectation design). **Recommended:** golang-naming.

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

(The bcrypt hash is reused — both users share password `testpassword`. `otheruser` is deliberately **not** on the whitelist declared in `config.test.yml`.)

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

**Required Skills:** golang-testing, systematic-debugging (only if an AC fails). **Recommended:** golang-error-handling.

- [ ] **Step 1: Start the instance and capture tokens**

Run: `./scripts/run-test-env.sh`
Expected: prints both `JWT` and `JWT_OTHERUSER`. Export them in your shell as the script instructs.

- [ ] **Step 2: AC#1 + startup log — whitelist declared and loaded**

Check the container startup log for the loaded-count line.

Run: `docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i "whitelist"`
Expected: a line `[custom-fields] whitelist loaded: 1 manager(s)` (only `testuser` is whitelisted, per `config.test.yml`).

If instead you see the "whitelist empty" line, the config key was not read — check that `config.test.yml` has `customfields:\n  whitelist: "testuser"` and that the image is the backport build (which exposes `viper`). This is the residual runtime check from the spec.

- [ ] **Step 3: AC#2 — whitelisted user allowed**

Run: `curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/manager | jq .`
Expected: `{ "username": "testuser", "is_manager": true }`.

- [ ] **Step 4: AC#3 — non-whitelisted user denied (predicate)**

Run: `curl -s -H "Authorization: Bearer $JWT_OTHERUSER" http://127.0.0.1:4176/api/v1/plugins/custom-fields/manager | jq .`
Expected: `{ "username": "otheruser", "is_manager": false }`. (Enforcement on field-definition ops is S2/S9 — out of S8 scope; this proves the predicate itself.)

- [ ] **Step 5: AC#4 — malformed entry logs error, no crash**

Temporarily set a malformed whitelist via the env override and restart.

Run:
```bash
# Temporarily add to compose.test.yml under `vikunja:`:
#     environment:
#       - VIKUNJA_CUSTOMFIELDS_WHITELIST=testuser,,otheruser
# then restart:
docker compose -f compose.test.yml down
docker compose -f compose.test.yml up -d
# wait for health:
until curl -sf -o /dev/null http://127.0.0.1:4176/api/v2/info; do sleep 2; done
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i "whitelist"
```
Expected: a line `[custom-fields] whitelist: ignoring empty entry at position 1` AND a line `[custom-fields] whitelist loaded: 2 manager(s)` (both `testuser` and `otheruser` now counted — the env override replaced the config-file value). Vikunja is healthy (the `/api/v2/info` probe succeeded) — no crash.

Then **revert** the `compose.test.yml` change (remove the `environment:` block) and restart for subsequent steps:
```bash
docker compose -f compose.test.yml down
docker compose -f compose.test.yml up -d
until curl -sf -o /dev/null http://127.0.0.1:4176/api/v2/info; do sleep 2; done
```
Do not commit the malformed-value edit — it was a temporary verification probe. After reverting, `git diff compose.test.yml` should show no changes.

- [ ] **Step 6: AC#5 — absent whitelist → empty, no crash**

Temporarily remove the config key entirely and restart.

Run:
```bash
# Temporarily comment out / remove the `customfields:` section in config.test.yml, then:
docker compose -f compose.test.yml down
docker compose -f compose.test.yml up -d
until curl -sf -o /dev/null http://127.0.0.1:4176/api/v2/info; do sleep 2; done
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i "whitelist"
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/manager | jq .
```
Expected: a line `[custom-fields] whitelist empty — no users may manage custom fields`, and the `/manager` route returns `{ "is_manager": false }` for `testuser` too. Vikunja is healthy — no crash.

Then **restore** the `customfields:` section with `whitelist: "testuser"` and restart for the doc tasks. Again, `git diff config.test.yml` should be clean afterward.

- [ ] **Step 7: AC#6 note**

AC#6 ("shared with both S2 and S9") is **not verifiable in S8 alone** — it is proven when S2 imports and calls `IsManager`. No action here beyond recording this in the S8 story's status notes during Task 6. This step is a checkpoint, not a command.

- [ ] **Step 8: Commit (if any harness artifact changed)**

Only commit if Steps 5/6 left a real harness change (they shouldn't — both were reverted). If `git status --short` is clean, skip. If a genuinely intended harness fix emerged during verification, commit it as `test: ...`.

- [ ] **Step 9: Report**

Write the verification report (per-AC PASS/FAIL with command output) to the report file named for this task's brief. Return status, and if any AC failed, return BLOCKED with the failing AC and its output (do NOT attempt a code fix yourself — the controller routes fix work).

---

### Task 6: Doc updates — name the config mechanism consistently

The stories and PRD originally worded the whitelist as living in "Vikunja's config file" — that wording is now correct (the whitelist IS read from the config file via viper). This task is a light touch: ensure the mechanism names the `customfields.whitelist` key and the `VIKUNJA_CUSTOMFIELDS_WHITELIST` env override, and that no stray "environment variable" mechanism claims remain. The whitelist *concept* and *name* ("Config Whitelist") are unchanged.

**Files:**
- Modify: `docs/stories/S8-config-whitelist.md`
- Modify: `docs/PRD.md`
- Modify: `docs/stories/S2-field-definition-api.md`
- Modify: `docs/stories/S9-management-ui.md`

**Required Skills:** git-flow. **Recommended:** golang-naming.

- [ ] **Step 1: S8 story**

In `docs/stories/S8-config-whitelist.md`, ensure the mechanism wording says the whitelist lives in Vikunja's config under `customfields.whitelist` (overridable by the `VIKUNJA_CUSTOMFIELDS_WHITELIST` env var). If any line says the whitelist is an "environment variable" only, correct it to "Vikunja config (`customfields.whitelist`, overridable by the `VIKUNJA_CUSTOMFIELDS_WHITELIST` env var)". Keep the title `Config Whitelist`.

- [ ] **Step 2: PRD**

In `docs/PRD.md`, ensure the "Config file" / "Config" bullet names the `customfields.whitelist` key and env override. Phrasings like "config-declared whitelist" / "config whitelist" may stay (the concept is config); add the key name where the mechanism is the point.

- [ ] **Step 3: S2 story**

In `docs/stories/S2-field-definition-api.md`, references to "config whitelist (S8)" → "management whitelist (S8, read from Vikunja config)". Keep "whitelist (S8)" where only the concept matters.

- [ ] **Step 4: S9 story**

In `docs/stories/S9-management-ui.md`, references to "config whitelist (S8)" → "management whitelist (S8)". Keep "whitelist (S8)" where only the concept matters.

- [ ] **Step 5: Verify no stray mechanism references remain**

Run: `cd /mnt/data/nickp/Documents/Code/vikunja-projects/vikunja-custom-fields-plugin && grep -rni "environment variable\|env var\|env-var" docs/stories/S8-config-whitelist.md docs/PRD.md docs/stories/S2-field-definition-api.md docs/stories/S9-management-ui.md`
Expected: no matches describing the whitelist as an env var *only*. (Mentions of the env *override* alongside the config key are fine.)

- [ ] **Step 6: Commit**

```bash
git add docs/stories/S8-config-whitelist.md docs/PRD.md docs/stories/S2-field-definition-api.md docs/stories/S9-management-ui.md
git commit -m "docs: name the customfields.whitelist config mechanism consistently"
```

---

### Task 7: Finish the feature branch

**Files:** none (git only)

**STOP REASON:** Per the controller's standing ruling, this task's `git flow feature finish` (merge into `develop`) is a side effect on the shared integration branch. Execute Steps 1 and 4, then **PAUSE before Step 2** and confirm with the user before merging. The branch stays ready-to-finish.

- [ ] **Step 1: Confirm all verification passed and the tree is clean**

Run: `git status --short`
Expected: clean (any temporary `compose.test.yml` / `config.test.yml` edits from Task 5 were reverted).

- [ ] **Step 2: Finish the feature branch into develop** — ⏸ PAUSE FOR USER CONFIRMATION BEFORE THIS STEP

Run: `git flow feature finish s8-config-whitelist`
Expected: merges `feature/s8-config-whitelist` → `develop`, deletes the feature branch, switches to `develop`.

- [ ] **Step 3: Confirm**

Run: `git rev-parse --abbrev-ref HEAD && git log --oneline -8`
Expected: on `develop`; the S8 commits (whitelist parse, route, config, harness, docs) appear in history above the spec/plan commits.

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