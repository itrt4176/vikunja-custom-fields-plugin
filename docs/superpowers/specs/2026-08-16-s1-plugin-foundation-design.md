# S1 — Plugin Foundation: Design Spec

**Date:** 2026-08-16
**Story:** [S1 — Plugin Foundation](../../stories/S1-plugin-foundation.md)
**Status:** Approved (pending spec review)

## Summary

The minimum viable yaegi plugin: it loads on Vikunja startup, creates two database tables (field definitions, field values) for all later stories, registers an authenticated route group with a health-check endpoint, and shuts down cleanly. No core Vikunja files are modified.

This is the second attempt at this project. The first attempt's `feature/s2-field-definition-api` branch and the `archived-develop` S1 branch are **abandoned prior art** — built on a flawed assumption (see Context) by a weaker model. Nothing from those branches is adopted without independent, adversarial re-verification. Every load-bearing technical fact in this spec was re-verified against the live `vikunja/` fork source, not inherited.

## Context — the foundational assumption this project must not repeat

The first attempt assumed only the admin **UI panel** was locked behind a Pro license. In reality, the **entire admin feature** (API and UI) requires a Pro license. This was discovered while testing S2 during implementation and caused the restart.

**Consequence for the design:** the plugin must be fully self-contained — its own routes, its own management surface served by the plugin, governed by the config whitelist (S8) — and must never reach into Vikunja's licensed admin capability. S1's scope (load, create its own tables, register an authenticated route, health check) touches no admin feature, so S1 is inherently safe. This constraint is recorded here so every subsequent story inherits it.

## Independently verified facts (live `vikunja/` fork source)

These were checked against the current source, not the abandoned branches:

- **Module/import path** is `code.vikunja.io/api` (e.g. `code.vikunja.io/api/pkg/plugins`). The published docs that show `github.com/vikunja/vikunja/...` are an upstream docs error.
- **Plugin interfaces** (`pkg/plugins/interfaces.go`): `Plugin` (Name/Version/Init/Shutdown), `AuthenticatedRouterPlugin` (RegisterAuthenticatedRoutes(`*echo.Group`)), `UnauthenticatedRouterPlugin`, `MigrationPlugin` (Migrations() `[]*xormigrate.Migration`).
- **Yaegi symbol table** (`pkg/yaegi_symbols/`): registers `db` (incl. `NewSession`, `GetDialect`, `Type`), `log`, `plugins`, `models`, `user`, `events`, echo v5, watermill. It does **not** register `xorm.io/xorm`, `src.techknowlogick.com/xormigrate`, or `config`. (`config` and the `web` package are unavailable to yaegi plugins.)
- **`MigrationPlugin` is blocked under yaegi:** a plugin cannot construct `*xormigrate.Migration` values or name `*xorm.Engine` in a `Migrate` callback, because xorm/xormigate are not importable symbol packages. The loader looks for `NewMigrationPlugin` but the plugin cannot produce valid migrations. → Tables are created in `Init()` instead.
- **`db.NewSession()`** (`pkg/db/db.go:457`) creates a session and calls `s.Begin()` — it opens an **active transaction** that auto-rolls-back on `Close()`. **Any DB work must end with `s.Commit()`** or it is rolled back. (The abandoned branches' comment to this effect was correct, but re-verified here.)
- **`*xorm.Session.Sync2(beans ...any) error`** exists (xorm 1.4.1, `sync.go:205`), so `s.Sync2(&Struct{})` is a valid call.
- **Lifecycle/order** (`pkg/plugins/manager.go`): `Initialize()` loads plugins → (if `NewMigrationPlugin` exists, collects migrations) → `migration.Migrate(nil)` runs → `p.Init()` runs for each plugin → `RegisterPluginRoutes` runs later during route setup. So `Init()` runs after the DB engine is ready and before routes are registered.
- **Route prefix:** authenticated plugin routes are under `/api/v1/plugins/…` (`pkg/routes/routes.go:967` groups the authenticated `/api/v1` group under `/plugins`). No v2 plugin mechanism exists.
- **Official example** (`examples/plugins/example/main.go`) confirms the yaegi plugin shape: singleton, `NewPlugin` + typed `NewAuthenticatedRouterPlugin`/`NewUnauthenticatedRouterPlugin` factories, `db.NewSession()` with method calls, `*echo.Context` handler signature.

### The one unverified assumption

Whether xorm's reflection correctly reads a **yaegi-interpreted** struct's fields and `xorm` tags to generate DDL. The only evidence it works is the abandoned branches (`s.Insert(&CustomField)`), which is distrusted. This is resolved by the Task 0 spike (see Testing) before the real S1 code is written.

## Architecture & file layout

- **Single `main.go`**, `package main`, in the plugin repo root (mounted live at `/app/vikunja/plugins/custom-fields`).
- Yaegi's loader evaluates every `.go` file in the plugin dir via `os.ReadDir` in one interpreter. Single-file is the safest path (matches the official example and the yaegi docs' guidance). Multi-file is *possible* later (files eval in filename-sorted order, so no cross-file eval-order dependencies), but that's a future-story call if the file grows.
- **No core Vikunja files are modified** (AC#3). The plugin lives entirely in its own repo; the `vikunja/` fork is untouched.
- **Imports** (all verified available to yaegi): `code.vikunja.io/api/pkg/db`, `code.vikunja.io/api/pkg/log`, `code.vikunja.io/api/pkg/plugins`, `github.com/labstack/echo/v5`, and stdlib `fmt`, `net/http`, `time`. **Not** `config`, `xorm`, `xormigate`, or `web`.

## Plugin lifecycle

```
type CustomFieldsPlugin struct{}

Name()    → "custom-fields"     // matches the mounted dir name
Version() → "0.1.0"
Init()    → create tables (§Schema), log "initialized", return err on failure
Shutdown()→ return nil (no resources to clean in S1)
```

- **Factories** (singleton pattern, matching the official example):
  - `NewPlugin() plugins.Plugin` (required)
  - `NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin` (for the health route)
  - Deliberately **no** `NewMigrationPlugin` (blocked) and **no** `NewUnauthenticatedRouterPlugin` (not needed).
- **AC#1** is auto-satisfied: `manager.go:201` logs `Loaded plugin custom-fields v0.1.0` on register; `Init` adds an "initialized" line.

## Schema — minimal skeleton

S1 creates only the two tables the story names, with the irreducible columns that make them meaningful. Design-decision columns are deferred to the stories that own them.

**`CustomFieldDefinition`** → table `custom_field_definitions`

| Column | xorm tag | Notes |
|---|---|---|
| ID | `bigint autoincr not null unique pk` | |
| Name | `varchar(255) not null` | |
| Type | `varchar(50) not null` | field-type enum string (text/textarea/integer/decimal/date/datetime/select/multiselect/checkbox/url) |
| Created | `created not null` | |
| Updated | `updated not null` | |

**`CustomFieldValue`** → table `custom_field_values`

| Column | xorm tag | Notes |
|---|---|---|
| ID | `bigint autoincr not null unique pk` | |
| CustomFieldDefinitionID | `bigint not null` | FK-ish; no hard constraint (portable) |
| TaskID | `bigint not null` | |
| Value | `text` | S3 may refine per field type |
| Created | `created not null` | |
| Updated | `updated not null` | |

Each struct implements `TableName() string` returning the plural table name, matching Vikunja convention (`labels`, `projects`).

**Deferred to S2 (field definitions):** description, `field_config`/constraints storage, `display_order`, `is_api_only`, `is_global`, the field↔project assignment M2M table, name-uniqueness (needs project context).

**Deferred to S3 (field values):** value-type refinement, `UNIQUE(field, task)`, query indexes on `task_id`/`custom_field_definition_id`.

**Rationale for deferral:** the project's story slicing assigns data-model design to S2 (definitions) and S3 (values); S1 is the foundation, not the data model. The project restarted because a foundation locked in a wrong assumption — the defense is an assumption-light foundation, not a maximalist one. Deferring is cheap: `Sync2` auto-adds columns when structs gain fields, and the test DB is ephemeral (tmpfs — destroyed on `docker compose down`), so there's no migration friction in dev. The only real risk (adding NOT NULL columns to non-empty production tables later) is S2/S3's to manage, with full knowledge, at their time — and the tables are empty until S2 creates definitions.

### Table creation in `Init()`

```go
s := db.NewSession()
defer s.Close()
if err := s.Sync2(&CustomFieldDefinition{}, &CustomFieldValue{}); err != nil {
    return fmt.Errorf("custom-fields: sync tables: %w", err)
}
if err := s.Commit(); err != nil {        // NewSession() Begins a tx that auto-rolls-back
    return fmt.Errorf("custom-fields: commit: %w", err)  // on Close(); Commit is mandatory.
}
log.Infof("[custom-fields] plugin v0.1.0 initialized, tables created")
```

- **Portability:** `Sync2` maps column types per dialect automatically (SQLite/MySQL/Postgres) — the primary reason it is preferred over raw DDL.
- `TableName()` is part of this path and rides on the same xorm-reflection assumption validated by the Task 0 spike.

### Fallback path (if the Task 0 spike fails)

If xorm cannot reflect yaegi-interpreted structs, S1 creates the same two tables with per-dialect raw DDL:

```go
s := db.NewSession()
defer s.Close()
switch db.GetDialect() {                  // returns "sqlite3" | "mysql" | "postgres" (xorm builder constants)
case "sqlite3": s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (id INTEGER PRIMARY KEY AUTOINCREMENT, …)`)
case "mysql":   s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (id BIGINT AUTO_INCREMENT PRIMARY KEY, …)`)
case "postgres": s.Exec(`CREATE TABLE IF NOT EXISTS custom_field_definitions (id BIGSERIAL PRIMARY KEY, …)`)
// …same for custom_field_values…
}
s.Commit()                                // still mandatory
```

The auto-increment syntax is the one non-portable bit, hence the dialect branch. The `s.Commit()` requirement applies identically. The struct definitions are dropped in this path (no `TableName()` needed; DDL names the tables directly).

## Routes

```go
func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
    g.GET("/custom-fields/health", healthHandler)   // → /api/v1/plugins/custom-fields/health
}

func healthHandler(c *echo.Context) error {          // *echo.Context — matches official example
    return c.JSON(http.StatusOK, map[string]string{
        "name": "custom-fields", "version": "0.1.0", "status": "ok",
    })
}
```

- Authenticated route group under the plugin API prefix (AC#4). Auth = a valid JWT/API key for **any** logged-in user — **not** admin-gated (consistent with the no-admin-license-dependency constraint; the regular test user's JWT works).
- The health route is a throwaway load-proof — it exists only to demonstrate route registration and the plugin is live. It does no DB access. No handler logic beyond it (per S1 out-of-scope).

## Error handling & shutdown

- `Init` failure (`Sync2`/`Commit`) → `log.Errorf` + return `err`. `manager.go:75` logs `Plugin custom-fields failed to init` and Vikunja continues starting (it does not fatal on `Init` error). The test-harness verification catches this.
- Health handler has no error paths (no I/O).
- `Shutdown` returns `nil` — nothing to clean (no goroutines/listeners in S1). AC#5 (clean shutdown, no panics) is verified by `docker compose down` showing no panic in the logs.

## Testing strategy

The plugin source cannot be `go test`ed standalone (its imports resolve only inside the vikunja module), so testing is **integration via the existing test instance** (`compose.test.yml`, SQLite).

### Task 0 — verification spike (the gate)

Before the real S1 code, a **throwaway** yaegi plugin with one `xorm`-tagged struct that calls `s.Sync2(&struct{})` against the SQLite test instance. Confirms xorm reflects yaegi-interpreted structs (the one unverified assumption). Outcome:

- ✅ Sync2 works → build S1 with the Sync2 path (§Schema).
- ❌ Sync2 fails → build S1 with the raw-DDL fallback path.

Discard the spike after deciding. This de-risks the whole project early: if struct-reflection is broken, every later story (S2/S3 CRUD) learns it on day one and designs around raw SQL, instead of rediscovering it mid-implementation as the last attempt did.

### Acceptance-criteria verification

| AC | How verified |
|---|---|
| 1. Starts + logs name/version | Test-instance logs contain `Loaded plugin custom-fields v0.1.0` (manager auto-log) + `[custom-fields] plugin v0.1.0 initialized`. |
| 2. Tables created on first load | Query SQLite in the container → `custom_field_definitions` + `custom_field_values` exist with the right columns. |
| 3. No core files modified | Structural — no files outside the plugin repo change. |
| 4. Authenticated route group | `curl -H "Authorization: Bearer $JWT" http://127.0.0.1:3456/api/v1/plugins/custom-fields/health` → 200 `{name, version, status}`. |
| 5. Clean shutdown | `docker compose down` → no panic in logs. |
| 6. No regressions | Interpreted as "Vikunja and the plugin start correctly together" — the test instance (stock image + plugin mounted) starts healthy. Not a re-run of Vikunja's own suite (that runs against the fork without the plugin and is unaffected by S1's zero core changes). |

A lightweight health-endpoint smoke check may optionally be added to `scripts/run-test-env.sh`, but the core S1 verification is manual via the harness.

## Yaegi constraints & upstream-conversion note

- **No `MigrationPlugin`** (xorm/xormigate absent from the symbol table). Tables created in `Init()` instead.
- **No `config` import**; dialect detection (raw-DDL fallback only) uses `db.GetDialect()` / `db.Type()`.
- **Single `main.go`** (yaegi single-file safest).
- **Upstreaming:** when this feature moves into Vikunja core, the `Init`-based `Sync2` becomes a proper versioned `xormigrate` migration and the structs become native models (gaining `web.CRUDable`/`web.Permissions` embeds). This is a mechanical move — the "plugin as proving ground" trade-off, recorded for the future.

## Git workflow

Per `CLAUDE.local.md`: git-flow, branch `feature/s1-plugin-foundation` off `develop`, Conventional Commits (e.g. `feat: add plugin foundation skeleton`, `feat: create field definition and value tables`, `feat: register health route`). A worktree decision will be raised before any development work. The implementation plan (next step) details the branch/commit structure and includes Required/Recommended skills lists per `CLAUDE.local.md`.

## Out of scope

- Any API handler logic beyond the health-check route.
- Field definition CRUD — S2.
- Field value CRUD — S3.
- Config whitelist parsing — S8.
- Any frontend changes.
- The full data model (constraints, options, project assignment, value typing, indexes) — S2/S3.
- Automated unit tests of the plugin source (infeasible standalone; covered by integration via the test instance).
