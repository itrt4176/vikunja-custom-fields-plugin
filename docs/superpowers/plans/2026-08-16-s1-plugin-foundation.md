# S1 — Plugin Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the minimum viable yaegi plugin that loads on Vikunja startup, creates two database tables, registers an authenticated health-check route, and shuts down cleanly — with zero core Vikunja files modified.

**Architecture:** A single `main.go` yaegi plugin (`package main`) lives in the plugin repo root, mounted live into the `vikunja/vikunja:2.4` test container at `/app/vikunja/plugins/custom-fields`. It implements the base `Plugin` interface (Name/Version/Init/Shutdown) plus the `AuthenticatedRouterPlugin` capability, exporting typed factory functions so yaegi's loader can discover them. Tables are created in `Init()` via `db.NewSession()` + xorm `Sync2` (with a verified raw-SQL fallback). No `MigrationPlugin` (xorm/xormigate are absent from the yaegi symbol table — this is verified, not assumed).

**Tech Stack:** Go (yaegi-interpreted, not compiled), xorm v1.4.1 (via the host binary's symbol table), Vikunja 2.4 plugin interfaces, echo v5, SQLite (test instance).

## How Testing Works In This Project

**The plugin source cannot be `go test`ed.** Its imports (`code.vikunja.io/api/pkg/...`) resolve only inside the Vikunja module, and yaegi interprets it at runtime inside the host process. There are no unit tests for this code.

**Testing is integration via the existing test instance** (`compose.test.yml`, SQLite). The pattern for every task's verification:

1. Edit `main.go` (it's mounted live into the container).
2. `docker compose -f compose.test.yml down` (clean slate — the DB is tmpfs, destroyed on down).
3. `./scripts/run-test-env.sh` (starts the container, waits for health, seeds the test user, logs in, prints a JWT).
4. Observe container logs / curl the endpoint / inspect the DB.
5. `docker compose -f compose.test.yml down` when done.

Each task's "verify" steps are this integration cycle. This is a deliberate, faithful adaptation of TDD's "write the failing test → run → implement → run" cadence: here the "test" is the integration observation, and it is written/expected *before* the code is assumed correct. The spike (Task 1) is the one genuine up-front probe.

### Two SQLite-inspection facts (because the image is distroless)

The `vikunja/vikunja:2.4` image has **no shell and no `sqlite3` binary**, and the DB is a **tmpfs mount inside the container** (`/db/vikunja.db`) — not host-accessible. So SQLite is inspected by copying the file out of the *running* container to the host, then using the host `sqlite3` (confirmed at `/home/nmanos/Android/Sdk/platform-tools/sqlite3`, on `$PATH`):

```bash
docker compose -f compose.test.yml cp vikunja:/db/vikunja.db /tmp/vikunja.db
sqlite3 /tmp/vikunja.db ".tables"
sqlite3 /tmp/vikunja.db ".schema custom_field_definitions"
```

This must run **while the container is up** (tmpfs vanishes on `down`). The copy is a filesystem read via the container runtime, so it works on distroless images (no exec needed).

## Global Constraints

Copied verbatim from the approved spec. Every task's requirements implicitly include these.

- **No core Vikunja files are modified.** The plugin lives entirely in its own repo; the `vikunja/` fork is untouched. (Acceptance Criterion 3.)
- **Import path is `code.vikunja.io/api`** (e.g. `code.vikunja.io/api/pkg/plugins`). The docs that show `github.com/vikunja/vikunja/...` are an upstream docs error.
- **`db.NewSession()` opens an active transaction that auto-rolls-back on `Close()`.** Any DB work — `Sync2` or raw `Exec` — MUST end with `s.Commit()` or it is rolled back. Verified at `pkg/db/db.go:457`.
- **`MigrationPlugin` is unusable under yaegi** (xorm/xormigate not in the symbol table). Tables are created in `Init()`, not via migrations.
- **The plugin must not depend on Vikunja's licensed admin feature.** (The whole admin feature — API and UI — requires a Pro license; this was the flawed assumption that sank the first attempt. S1 touches no admin feature, but this constraint is inherited by all later stories.)
- **Single `main.go`, `package main`**, in the plugin repo root. Yaegi single-file is safest.
- **Route prefix:** authenticated plugin routes are under `/api/v1/plugins/…`. No v2 plugin mechanism exists.
- **Git-flow is the rule.** Feature branch `feature/s1-plugin-foundation` off `develop`; Conventional Commits. (The test-instance commits on `develop` were a rare overruled exception, not a pattern.)

---

## File Structure

A single file is created and progressively built up. No other files change.

| File | Responsibility |
|------|----------------|
| `main.go` | The entire plugin: struct + lifecycle (Name/Version/Init/Shutdown), table structs + creation in `Init`, the health route handler, and the typed factory functions (`NewPlugin`, `NewAuthenticatedRouterPlugin`). |

The repo already contains: `compose.test.yml`, `config.test.yml`, `scripts/run-test-env.sh` (the test harness — unchanged), `docs/` (stories, specs, plans — unchanged), and no `.go` files yet. This plan adds `main.go` only.

---

## Pre-Task: Git-flow setup + worktree decision

**Files:** none.

- [ ] **Step 1: Invoke the git-flow skill**

Use the `Skill` tool with `skill: git-flow`. Follow it to create the feature branch off `develop`:

```bash
git flow feature start s1-plugin-foundation
# equivalently: git checkout develop && git checkout -b feature/s1-plugin-foundation
```

- [ ] **Step 2: Ask the user about a worktree**

Per `CLAUDE.local.md`: "Before doing ANY development work, ask the user if they want to do it in a worktree. If they say yes, use the `EnterWorktree` tool." Ask now; if yes, enter the worktree before Task 1. If no, proceed on the feature branch in the working tree.

- [ ] **Step 3: Confirm the branch**

```bash
git branch --show-current   # expect: feature/s1-plugin-foundation
```

---

### Task 1: Verification spike — does `Sync2` work on yaegi-interpreted structs?

**This is the gate.** It resolves the one unverified assumption in the spec: whether xorm's reflection can read a yaegi-interpreted struct's fields and `xorm` tags to generate DDL. The outcome decides Task 3's implementation path. The spike is throwaway — it lives as the first version of `main.go`, is verified, then **overwritten** by Task 2.

**Files:**
- Create: `main.go` (spike version — temporary)

**Interfaces:**
- Consumes: nothing.
- Produces: a recorded decision (Sync2 works → Task 3 uses `Sync2`; Sync2 fails → Task 3 uses the raw-SQL fallback). This decision is written into Task 3, not into code.

**Required Skills:**
- `golang-database` — xorm `Sync2`, `db.NewSession()` transaction semantics, `s.Commit()`.
- `golang-structs-interfaces` — struct field tags (`xorm:"..."`), `TableName()`.

**Recommended Skills:**
- `golang-error-handling` — wrapping errors with `%w`.
- `golang-naming` — struct/table naming.

- [ ] **Step 1: Write the spike plugin**

Create `main.go` in the repo root:

```go
package main

import (
	"fmt"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
)

// SpikeTable is a throwaway struct to prove xorm reflects yaegi-interpreted structs.
type SpikeTable struct {
	ID    int64  `xorm:"bigint autoincr not null unique pk"`
	Name  string `xorm:"varchar(255) not null"`
}

func (SpikeTable) TableName() string { return "spike_tables" }

type SpikePlugin struct{}

func (p *SpikePlugin) Name() string    { return "spike" }
func (p *SpikePlugin) Version() string { return "0.0.0" }
func (p *SpikePlugin) Init() error {
	s := db.NewSession()
	defer s.Close()
	if err := s.Sync2(&SpikeTable{}); err != nil {
		return fmt.Errorf("spike: sync: %w", err)
	}
	if err := s.Commit(); err != nil {
		return fmt.Errorf("spike: commit: %w", err)
	}
	log.Infof("[spike] Sync2 succeeded")
	return nil
}
func (p *SpikePlugin) Shutdown() error { return nil }

var singleton = &SpikePlugin{}

func NewPlugin() plugins.Plugin { return singleton }
```

- [ ] **Step 2: Start the test instance**

```bash
./scripts/run-test-env.sh
```

Expected: the instance becomes healthy and prints the JWT box. The plugin dir `./` is mounted, so the spike is live.

- [ ] **Step 3: Check the logs for Sync2 success or failure**

```bash
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i 'spike\|loaded plugin\|failed to init\|sync'
```

Expected (success): a line `Loaded plugin spike v0.0.0` and `[spike] Sync2 succeeded`.
Expected (failure): `Plugin spike failed to init: spike: sync: ...` with a yaegi/xorm reflection error.

- [ ] **Step 4: Verify the table was actually created (only if logs say success)**

The container must still be running. Copy the DB out and inspect:

```bash
docker compose -f compose.test.yml cp vikunja:/db/vikunja.db /tmp/vikunja.db
sqlite3 /tmp/vikunja.db ".tables"
sqlite3 /tmp/vikunja.db ".schema spike_tables"
```

Expected: `.tables` lists `spike_tables`; `.schema` shows `id` (bigint, pk, autoincr) and `name` (varchar(255), not null).

- [ ] **Step 5: Record the decision and tear down**

Decide based on Steps 3–4:
- ✅ **Sync2 works** (table exists with correct columns) → Task 3 uses the `Sync2` path as written.
- ❌ **Sync2 fails** (reflection error, or table missing/wrong) → Task 3 uses the **raw-SQL fallback path** (Step alt in Task 3).

```bash
docker compose -f compose.test.yml down
```

No commit — the spike is throwaway. (The `spike_tables` table vanishes with the tmpfs DB.) Do not delete `main.go` yet — Task 2 overwrites it.

---

### Task 2: Plugin skeleton — loads and logs

Replace the spike with the real plugin skeleton: the struct, lifecycle methods, singleton, and the required `NewPlugin` factory. `Init` is intentionally empty here (no tables, no route yet). This delivers AC#1 (starts + logs name/version) and half of AC#5 (clean startup, no panic).

**Files:**
- Modify: `main.go` (overwrite the spike)

**Interfaces:**
- Consumes: nothing.
- Produces: `CustomFieldsPlugin` struct; `Name() → "custom-fields"`, `Version() → "0.1.0"`; `NewPlugin() plugins.Plugin` factory; a `singleton` instance. Later tasks add methods to this same struct and the same `singleton`.

**Required Skills:**
- `golang-naming` — type/function naming.
- `git-flow` — already active; this task's commits land on `feature/s1-plugin-foundation`.

**Recommended Skills:**
- `golang-structs-interfaces` — interface implementation shape.

- [ ] **Step 1: Write the skeleton**

Overwrite `main.go`:

```go
package main

import (
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
)

// CustomFieldsPlugin is the main plugin struct. All capabilities (tables, routes)
// are methods added to this struct in later tasks.
type CustomFieldsPlugin struct{}

func (p *CustomFieldsPlugin) Name() string    { return "custom-fields" }
func (p *CustomFieldsPlugin) Version() string { return "0.1.0" }
func (p *CustomFieldsPlugin) Init() error {
	log.Infof("[custom-fields] plugin v0.1.0 initialized")
	return nil
}
func (p *CustomFieldsPlugin) Shutdown() error {
	log.Infof("[custom-fields] plugin shutting down")
	return nil
}

var singleton = &CustomFieldsPlugin{}

func NewPlugin() plugins.Plugin { return singleton }
```

- [ ] **Step 2: Start the test instance**

```bash
./scripts/run-test-env.sh
```

- [ ] **Step 3: Verify the plugin loaded and logged (AC#1)**

```bash
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i 'custom-fields\|loaded plugin'
```

Expected: `Loaded plugin custom-fields v0.1.0` (auto-logged by the manager on register) AND `[custom-fields] plugin v0.1.0 initialized` (from `Init`).

Also confirm no init error:
```bash
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i 'failed to init'
```
Expected: no output.

- [ ] **Step 4: Verify clean shutdown (AC#5, startup half)**

```bash
docker compose -f compose.test.yml down
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i 'shutting down\|panic'
```

Expected: `[custom-fields] plugin shutting down` appears; no `panic` in the output.

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "feat: add custom-fields plugin skeleton with lifecycle"
```

---

### Task 3: Create the field definition and field value tables

Add the two minimal-skeleton structs and create their tables in `Init()`.

**If Task 1 decided Sync2 works → use Step path A.** **If Task 1 decided Sync2 fails → use Step path B (raw-SQL fallback).** Implement only one path.

**Files:**
- Modify: `main.go` (add structs, expand `Init`)

**Interfaces:**
- Consumes: `db.NewSession()`, `s.Commit()` (verified).
- Produces: `CustomFieldDefinition` struct → table `custom_field_definitions`; `CustomFieldValue` struct → table `custom_field_values`. These structs and table names are consumed by S2 (definitions CRUD) and S3 (values CRUD). S2/S3 will add columns to these structs and re-`Sync2` (auto-adds columns).

**Required Skills:**
- `golang-database` — `Sync2`, session/transaction lifecycle, dialect detection (path B).
- `golang-structs-interfaces` — `xorm` field tags, `TableName()`.
- `golang-error-handling` — error wrapping with `%w`.

**Recommended Skills:**
- `golang-naming` — column/table names.

- [ ] **Step 1: Add the table structs**

Add these two structs to `main.go` (above the `CustomFieldsPlugin` struct, or anywhere at package level):

```go
// CustomFieldDefinition is a single custom field's schema. S2 adds columns
// (description, constraints, project assignment, etc.) to this struct.
type CustomFieldDefinition struct {
	ID      int64  `xorm:"bigint autoincr not null unique pk" json:"id"`
	Name    string `xorm:"varchar(255) not null" json:"name"`
	Type    string `xorm:"varchar(50) not null" json:"type"`
	Created string `xorm:"created not null" json:"-"`
	Updated string `xorm:"updated not null" json:"-"`
}

func (CustomFieldDefinition) TableName() string { return "custom_field_definitions" }

// CustomFieldValue is one field's value on one task. S3 refines value typing and adds
// the UNIQUE(field, task) constraint and query indexes.
type CustomFieldValue struct {
	ID                       int64  `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID  int64  `xorm:"bigint not null" json:"custom_field_definition_id"`
	TaskID                   int64  `xorm:"bigint not null" json:"task_id"`
	Value                    string `xorm:"text" json:"value"`
	Created                  string `xorm:"created not null" json:"-"`
	Updated                  string `xorm:"updated not null" json:"-"`
}

func (CustomFieldValue) TableName() string { return "custom_field_values" }
```

> Note on `Created`/`Updated` types: the spec shows them as `created not null` / `updated not null` with no Go type specified. Use `time.Time` if xorm's `created`/`updated` tags reflect cleanly under yaegi; if the Task-1 spike revealed struct-reflection issues with non-string fields, fall back to `string` and let xorm's `created`/`updated` machinery populate them, or omit the tags and let S2/S3 settle them. The spike's verdict on field-reflection governs this — if in doubt, mirror the archived branch's pragmatic choice (`time.Time` with the tags) only after the spike confirms reflection works. **Do not guess: re-run a one-line spike variant if `time.Time` causes a load failure.**

(The above note is intentional guidance, not a placeholder — `time.Time` vs `string` for `created`/`updated` under yaegi is genuinely unverified and should be confirmed by reusing the Task-1 spike's finding. Prefer `time.Time` and the xorm tags if reflection works; that matches native Vikunja models like `Label`.)

**Step 2 — PATH A (use if Task 1 said Sync2 works): create tables via Sync2**

Expand `Init()`:

```go
func (p *CustomFieldsPlugin) Init() error {
	s := db.NewSession()
	defer s.Close()
	if err := s.Sync2(&CustomFieldDefinition{}, &CustomFieldValue{}); err != nil {
		log.Errorf("[custom-fields] failed to sync tables: %v", err)
		return fmt.Errorf("custom-fields: sync tables: %w", err)
	}
	if err := s.Commit(); err != nil {
		log.Errorf("[custom-fields] failed to commit: %v", err)
		return fmt.Errorf("custom-fields: commit: %w", err)
	}
	log.Infof("[custom-fields] plugin v0.1.0 initialized, tables created")
	return nil
}
```

Add `"fmt"` and `"code.vikunja.io/api/pkg/db"` to the import block.

**Step 2 — PATH B (use only if Task 1 said Sync2 fails): create tables via per-dialect raw SQL**

Expand `Init()` (imports: add `"fmt"`, `"code.vikunja.io/api/pkg/db"`):

```go
func (p *CustomFieldsPlugin) Init() error {
	s := db.NewSession()
	defer s.Close()

	// Auto-increment syntax is the only non-portable part, hence the dialect branch.
	// db.GetDialect() returns "sqlite3" | "mysql" | "postgres" (xorm builder constants).
	switch db.GetDialect() {
	case "sqlite3":
		_, err := s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Errorf("[custom-fields] failed to create custom_field_definitions: %v", err)
			return fmt.Errorf("custom-fields: create custom_field_definitions: %w", err)
		}
		_, err = s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_values (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			custom_field_definition_id INTEGER NOT NULL,
			task_id INTEGER NOT NULL,
			value TEXT,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			log.Errorf("[custom-fields] failed to create custom_field_values: %v", err)
			return fmt.Errorf("custom-fields: create custom_field_values: %w", err)
		}
	case "mysql":
		_, err := s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			return fmt.Errorf("custom-fields: create custom_field_definitions: %w", err)
		}
		_, err = s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_values (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			custom_field_definition_id BIGINT NOT NULL,
			task_id BIGINT NOT NULL,
			value TEXT,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			return fmt.Errorf("custom-fields: create custom_field_values: %w", err)
		}
	case "postgres":
		_, err := s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (
			id BIGSERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			return fmt.Errorf("custom-fields: create custom_field_definitions: %w", err)
		}
		_, err = s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_values (
			id BIGSERIAL PRIMARY KEY,
			custom_field_definition_id BIGINT NOT NULL,
			task_id BIGINT NOT NULL,
			value TEXT,
			created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
		if err != nil {
			return fmt.Errorf("custom-fields: create custom_field_values: %w", err)
		}
	}

	if err := s.Commit(); err != nil {
		log.Errorf("[custom-fields] failed to commit: %v", err)
		return fmt.Errorf("custom-fields: commit: %w", err)
	}
	log.Infof("[custom-fields] plugin v0.1.0 initialized, tables created")
	return nil
}
```

> If Path B is used, the `CustomFieldDefinition`/`CustomFieldValue` structs from Step 1 are still defined (S2/S3 will need them for xorm CRUD ops), but their `TableName()` method is the only thing used by S1 (and only if Path A; under Path B the DDL names the tables directly). Keep the structs regardless — S2/S3 depend on them.

- [ ] **Step 3: Start the test instance**

```bash
./scripts/run-test-env.sh
```

- [ ] **Step 4: Verify no init error**

```bash
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i 'custom-fields\|failed to init\|sync\|create custom_field'
```

Expected: `Loaded plugin custom-fields v0.1.0`, `[custom-fields] plugin v0.1.0 initialized, tables created`, and no `failed to init` / error lines.

- [ ] **Step 5: Verify the tables exist (AC#2)**

Container still running. Copy the DB out and inspect:

```bash
docker compose -f compose.test.yml cp vikunja:/db/vikunja.db /tmp/vikunja.db
sqlite3 /tmp/vikunja.db ".tables"
sqlite3 /tmp/vikunja.db ".schema custom_field_definitions"
sqlite3 /tmp/vikunja.db ".schema custom_field_values"
```

Expected:
- `.tables` lists both `custom_field_definitions` and `custom_field_values`.
- `.schema custom_field_definitions` shows columns `id`, `name`, `type`, `created`, `updated`.
- `.schema custom_field_values` shows columns `id`, `custom_field_definition_id`, `task_id`, `value`, `created`, `updated`.

- [ ] **Step 6: Tear down and commit**

```bash
docker compose -f compose.test.yml down
git add main.go
git commit -m "feat: create field definition and value tables on startup"
```

---

### Task 4: Register the authenticated health-check route

Add the `AuthenticatedRouterPlugin` capability: a typed `NewAuthenticatedRouterPlugin` factory and a `RegisterAuthenticatedRoutes` method that mounts `GET /custom-fields/health`. This delivers AC#4.

**Files:**
- Modify: `main.go` (add imports `net/http`, `github.com/labstack/echo/v5`; add the route method, handler, and factory)

**Interfaces:**
- Consumes: `*echo.Group` (passed by the loader), the `singleton` from Task 2.
- Produces: `NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin` factory; `GET /api/v1/plugins/custom-fields/health` returning `{"name","version","status"}`. S2 mounts its CRUD routes on the same `echo.Group` via the same method.

**Required Skills:**
- `golang-structs-interfaces` — implementing `AuthenticatedRouterPlugin`, typed factory shape.

**Recommended Skills:**
- `golang-naming` — route/handler names.

- [ ] **Step 1: Add the route registration, handler, and factory**

Add to `main.go`. Update the import block to include `"net/http"` and `"github.com/labstack/echo/v5"`. Add these methods and the factory (the factory goes next to the existing `NewPlugin`):

```go
// RegisterAuthenticatedRoutes mounts the plugin's authenticated routes on the
// /api/v1/plugins/ group. S2 adds its custom-fields CRUD routes here.
func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/health", healthHandler)
}

// healthHandler is a throwaway load-proof. It does no DB access.
func healthHandler(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{
		"name":    "custom-fields",
		"version": "0.1.0",
		"status":  "ok",
	})
}
```

And the typed factory (next to `NewPlugin`):

```go
// NewAuthenticatedRouterPlugin is the typed factory yaegi's loader looks for to
// register authenticated routes. Yaegi wraps return values per declared type, so
// sub-interface assertions don't work — this typed factory is mandatory under yaegi.
func NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin { return singleton }
```

- [ ] **Step 2: Start the test instance**

```bash
./scripts/run-test-env.sh
```

- [ ] **Step 3: Verify the route is registered and authenticated (AC#4)**

Using the JWT the bootstrap script printed:

```bash
export JWT="<paste the token from the script's output box>"

# Authenticated request → 200 with the health payload
curl -s -H "Authorization: Bearer $JWT" \
  http://127.0.0.1:3456/api/v1/plugins/custom-fields/health | jq .
```

Expected: HTTP 200, body `{"name":"custom-fields","version":"0.1.0","status":"ok"}`.

Also confirm the route is actually behind auth (no token → 401/403, not 200):

```bash
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:3456/api/v1/plugins/custom-fields/health
```

Expected: `401` or `403` (not `200`).

- [ ] **Step 4: Tear down and commit**

```bash
docker compose -f compose.test.yml down
git add main.go
git commit -m "feat: register authenticated health-check route"
```

---

### Task 5: Full acceptance-criteria sweep and finish

Run the complete AC verification as one coherent sequence, confirm no regressions, then finish the git-flow feature branch (merge to `develop`).

**Files:** none modified (verification + git finish only).

**Interfaces:**
- Consumes: the finished `main.go` from Tasks 2–4.
- Produces: a merged `develop` with the S1 plugin foundation, ready for S2.

**Required Skills:**
- `git-flow` — finishing the feature branch (`git flow feature finish`).
- `golang-testing` — for the integration-verification mindset (table of expected vs. actual per AC), even though these are not `go test` runs.

**Recommended Skills:**
- `superpowers:verification-before-completion` — running the full AC sweep before declaring done.
- `superpowers:requesting-code-review` — optional self-review pass before merge.

- [ ] **Step 1: Start clean**

```bash
docker compose -f compose.test.yml down
./scripts/run-test-env.sh
```

- [ ] **Step 2: Verify AC#1 — starts + logs name/version**

```bash
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i 'loaded plugin custom-fields\|initialized'
```

Expected: `Loaded plugin custom-fields v0.1.0` and `[custom-fields] plugin v0.1.0 initialized, tables created`.

- [ ] **Step 3: Verify AC#2 — tables created**

```bash
docker compose -f compose.test.yml cp vikunja:/db/vikunja.db /tmp/vikunja.db
sqlite3 /tmp/vikunja.db ".tables"
```

Expected: lists `custom_field_definitions` and `custom_field_values`.

- [ ] **Step 4: Verify AC#4 — authenticated route**

```bash
export JWT="<paste token>"
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:3456/api/v1/plugins/custom-fields/health | jq .
```

Expected: 200 + `{"name":"custom-fields","version":"0.1.0","status":"ok"}`.

- [ ] **Step 5: Verify AC#3 — no core files modified**

```bash
# From the plugin repo root. The only added/changed tracked file should be main.go.
git diff --name-only develop
```

Expected: only `main.go` (plus this plan and the spec, if they weren't already committed — they were committed on `develop` earlier, so expect just `main.go` on the feature branch's diff against the merge base). No files under any `vikunja/` fork path.

- [ ] **Step 6: Verify AC#5 + AC#6 — clean shutdown, no regressions**

```bash
docker compose -f compose.test.yml down
docker compose -f compose.test.yml logs vikunja 2>&1 | grep -i 'panic\|error\|shutting down'
```

Expected: `[custom-fields] plugin shutting down` present; no `panic`. AC#6 ("Vikunja and the plugin start correctly together") is satisfied by the instance having started healthy with the plugin enabled (Steps 2–4 all passed against the stock `vikunja/vikunja:2.4` image). Vikunja's own suite is unaffected by S1's zero core changes — it is not re-run.

- [ ] **Step 7: Finish the git-flow feature branch**

Invoke the `git-flow` skill and follow it to finish:

```bash
git flow feature finish s1-plugin-foundation
# merges feature/s1-plugin-foundation into develop, deletes the feature branch
```

Confirm `develop` now contains the plugin foundation:

```bash
git checkout develop
git log --oneline -5
ls main.go   # exists
```

---

## Self-Review

**1. Spec coverage** — each spec section → task:

| Spec section / AC | Covered by |
|---|---|
| Architecture & file layout (single `main.go`, no core changes) | Tasks 2–4 (build `main.go`); AC#3 verified in Task 5 Step 5 |
| Plugin lifecycle (Name/Version/Init/Shutdown, singleton, `NewPlugin`) | Task 2 |
| The one unverified assumption (xorm reflects yaegi structs) | Task 1 (the spike gate) |
| Schema — `CustomFieldDefinition` minimal skeleton | Task 3 Step 1 |
| Schema — `CustomFieldValue` minimal skeleton | Task 3 Step 1 |
| Table creation via `Sync2` (primary path) | Task 3 Step 2 Path A |
| Fallback raw-SQL path (if spike fails) | Task 3 Step 2 Path B |
| `db.NewSession()` transaction → `s.Commit()` mandatory | Global Constraints; applied in Task 3 (both paths) |
| Routes — authenticated health route, `*echo.Context`, typed factory | Task 4 |
| Error handling & shutdown | Task 2 (Shutdown); Task 3 (Init errors); AC#5 in Task 5 Step 6 |
| AC#1 (starts + logs) | Task 2 Step 3; Task 5 Step 2 |
| AC#2 (tables created) | Task 3 Step 5; Task 5 Step 3 |
| AC#3 (no core files) | Task 5 Step 5 |
| AC#4 (authenticated route) | Task 4 Step 3; Task 5 Step 4 |
| AC#5 (clean shutdown) | Task 2 Step 4; Task 5 Step 6 |
| AC#6 (no regressions — starts correctly together) | Task 5 Step 6 |
| Yaegi constraints & upstream note | Reflected in Global Constraints + code comments (Task 3/4) |
| Git workflow (git-flow, Conventional Commits) | Pre-Task; each task commits; Task 5 finishes the branch |

No spec section is unaccounted for.

**2. Placeholder scan** — No "TBD"/"TODO"/"implement later". The `Created`/`Updated` type note in Task 3 Step 1 is explicit guidance with a concrete default (`time.Time`), not a placeholder. The Path A/Path B branch is a verified decision gate, not an unresolved item.

**3. Type/name consistency** —
- Struct names: `CustomFieldsPlugin`, `CustomFieldDefinition`, `CustomFieldValue` — consistent across Tasks 2, 3, 4.
- Table names: `custom_field_definitions`, `custom_field_values` — consistent in Task 3 Path A (`TableName()`) and Path B (DDL).
- Factory names: `NewPlugin`, `NewAuthenticatedRouterPlugin` — match the official example and the loader's lookups (`loader.go` evaluates `main.NewPlugin`, `main.NewAuthenticatedRouterPlugin`).
- `singleton` is the single shared instance across all factories — consistent.
- JSON field name `custom_field_definition_id` matches the column name in both paths.
- `*echo.Context` (not `echo.Context`) — matches the official example; consistent in Task 4.

No mismatches found.
