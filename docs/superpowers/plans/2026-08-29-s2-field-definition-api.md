# S2 — Field Definition API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the CRUD API for custom field definitions — create, list, read, update, delete — governed by the S8 whitelist, in a single-file yaegi plugin modeled as if it were native Vikunja code.

**Architecture:** Single `main.go` with internal model/permissions layering mirroring Vikunja's `CRUDable`/`Permissions` split: `CustomFieldDefinition` gains `Create/ReadAll/Update/Delete(*xorm.Session, *user.User)` + `CanCreate/CanRead/CanUpdate/CanDelete` methods (wrapping `IsManager`), with thin Echo handlers driving each. Three tables (expand `custom_field_definitions`; add `custom_field_options`, `custom_field_projects` with a `project_id=0` sentinel for "all projects"). Events dispatched on commit as seams for S3.

**Tech Stack:** Go (yaegi-interpreted, single `main.go`), xorm/xormigrate, echo v5, Vikunja `pkg/db`/`pkg/user`/`pkg/events`/`pkg/models`. Tested by integration against the Docker test instance (SQLite) — the plugin source cannot be `go test`ed standalone.

**Spec:** `docs/superpowers/specs/2026-08-29-s2-field-definition-api-design.md` — every decision below is argued from the spec; read both.

## Global Constraints

- **Single `main.go`**, `package main`, plugin repo root. No multi-file (yaegi evaluates files individually; `plugin-development.md` mandates single-file). All new code is added to this one file.
- **No `xorm.io/builder`** in the yaegi symbol table → use xorm string-chaining (`s.Where(...).And(...).Or(...)`, `.In()`), never `builder.Eq/Or/In`.
- **No `pkg/web/handler`** in the symbol table → hand-roll Echo handlers; do not reuse `DoCreate`/`DoDelete`.
- **`*user.User`, not `web.Auth`**, in `CanX`/model signatures (`web` unavailable; `*user.User` is). Upstream-conversion point.
- **Explicit-table form** for all `Sync2`: `tx.Table(name).Sync2(&T{})`, never `Sync2(new(T))` (PR #3501; yaegi hides `TableName()` from xorm reflection).
- **`db.NewSession()` opens an active transaction that auto-rolls-back on `Close()`** → every DB path ends with `s.Commit()` or it rolls back.
- **Plugin route prefix** is `/api/v1/plugins/custom-fields/...`. No v2 plugin mechanism.
- **Whitelist**: `customfields.whitelist: "testuser"` (config.test.yml) — `testuser` is a manager, `otheruser` is not. Both JWTs are printed by `run-test-env.sh` (`$JWT`, `$JWT_OTHERUSER`).
- **Verbs**: POST create, GET list/read, PUT update (full-replace), DELETE. No PATCH (deferred to upstreaming).
- **`field_config` storage** — Task 1 spike chose `xorm:"json null"` (rung 1 round-trips under yaegi, including `*float64`). Task 2 uses `xorm:"json null"` as written; the `text+manual` fallback variant is NOT used.
- **Model layer** — Task 1 spike chose **methods-on-struct**. Task 7 uses methods on `CustomFieldDefinition` as written (not free-functions).
- **Events: DEFERRED** (Task 1 spike 3). Plugin-defined event types are unmarshalable by host `json.Marshal` under yaegi, so they're never published. User chose to defer rather than add host-defined event types to the fork. **Task 5 (Events) is SKIPPED.** Tasks 7 and 8 do NOT call `events.DispatchOnCommit`/`DispatchPending`/`CleanupPending` and do NOT import `pkg/events`. S2 registers no events.
- **Response serialization** (Task 1 spike 1 caveat) — interpreted structs serialize as `{}` through `c.JSON`; handlers build **response maps** field-by-field, never echo an interpreted struct. xorm DB read/write of interpreted structs works fine.
- **Conventional Commits**; branch `feature/s2-field-definition-api-design` (already started via `git flow feature start`). Do not commit to `develop`.

## File Structure

All work is in **`main.go`** (single file) plus one harness script edit:

| Section of `main.go` | Responsibility | Task |
|---|---|---|
| `FieldConfig` struct | typed scalar-constraint blob | 2 |
| `CustomFieldDefinition` struct (expanded) | field schema + `field_config` | 2 |
| `CustomFieldOption` struct | select option rows | 2 |
| `CustomFieldProject` struct | assignment M2M (sentinel `project_id=0`) | 2 |
| `Migrations()` (modified) | `Sync2` all four tables | 2 |
| Custom error types + `ErrCode*` consts | validation/auth errors (9000s range) | 3 |
| `validate*` pure funcs | type/name/options/constraints/assignment checks | 4 |
| Event types + `Name()` | **SKIPPED** — events deferred (spike 3) | 5 |
| `CanCreate/CanRead/CanUpdate/CanDelete` | wrap `IsManager` | 6 |
| `Create/ReadAll/ReadOne/Update/Delete` | DB CRUD on a `*xorm.Session` (no events) | 7 |
| Handlers + `RegisterAuthenticatedRoutes` | thin Echo → CanX → model → commit → JSON map | 8 |
| `scripts/run-test-env.sh` | seed projects for assignment-validation AC | 9 |

---

### Task 1: De-risking spikes (the gate)

The two new reflect paths under yaegi are verified before the real code, exactly as S1 de-risked xorm struct reflection. **Both spikes are throwaway — restore `main.go` after each.** The container mounts the plugin source live at `/app/vikunja/plugins/custom-fields`, so a spike temporarily replaces `main.go`.

**Files:**
- Modify (temporarily): `main.go` (back up first)
- Create (throwaway, then delete): `$CLAUDE_JOB_DIR/tmp/spike-json.go`, `$CLAUDE_JOB_DIR/tmp/spike-method.go`
- Probe DB: `db/vikunja.db` (host bind mount, readable via `sqlite3`)

**Interfaces:**
- Produces: two decisions recorded as notes — (a) `field_config` storage mechanism (`json` vs `text`+manual), (b) model-layer shape (method-on-struct vs free-function). These feed Task 2 (struct tag) and Task 7 (signature style). Task 2 and Task 7 MUST adopt whichever the spike confirms.

- [ ] **Step 1: Back up the real main.go**

```bash
cd /mnt/data/nickp/Documents/Code/vikunja-projects/vikunja-custom-fields-plugin
cp main.go "$CLAUDE_JOB_DIR/tmp/main.go.real-backup"
```

- [ ] **Step 2: Spike 1 — xorm `json` reflection on a yaegi struct**

Write a throwaway `main.go` that defines a struct with a `xorm:"json null"` field and round-trips it. Walk the fallback ladder; stop at the first rung that round-trips successfully.

```go
package main

import (
	"fmt"
	"net/http"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/plugins"
	"github.com/labstack/echo/v5"
	"src.techknowlogick.com/xormigrate"
	"xorm.io/xorm"
)

type SpikeThing struct {
	ID    int64       `xorm:"bigint autoincr not null unique pk" json:"id"`
	Cfg   SpikeCfg    `xorm:"json null" json:"cfg"`
}
type SpikeCfg struct {
	Required bool    `json:"required,omitempty"`
	Min      *float64 `json:"min,omitempty"`
}
func (SpikeThing) TableName() string { return "spike_things" }

type SpikePlugin struct{}
func (p *SpikePlugin) Name() string    { return "spike" }
func (p *SpikePlugin) Version() string { return "0.0.1" }
func (p *SpikePlugin) Init() error    { return nil }
func (p *SpikePlugin) Shutdown() error { return nil }

func (p *SpikePlugin) Migrations() []*xormigrate.Migration {
	return []*xormigrate.Migration{{
		ID: "20260829170000-spike", Description: "spike",
		Migrate: func(tx *xorm.Engine) error {
			return tx.Table("spike_things").Sync2(&SpikeThing{})
		},
		Rollback: func(tx *xorm.Engine) error { return tx.DropTables("spike_things") },
	}}
}
func (p *SpikePlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/spike/json", func(c *echo.Context) error {
		s := db.NewSession()
		defer s.Close()
		min := 5.0
		in := SpikeThing{Cfg: SpikeCfg{Required: true, Min: &min}}
		if _, err := s.Table("spike_things").Insert(&in); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"insert_err": err.Error()})
		}
		var out SpikeThing
		has, err := s.Table("spike_things").ID(in.ID).Get(&out)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"get_err": err.Error()})
		}
		if err := s.Commit(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"commit_err": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]interface{}{"has": has, "roundtrip": out})
	})
}

var spikeSingleton = &SpikePlugin{}
func NewPlugin() plugins.Plugin { return spikeSingleton }
func NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin { return spikeSingleton }
func NewMigrationPlugin() plugins.MigrationPlugin { return spikeSingleton }

var _ = log.Infof
```

- [ ] **Step 3: Run spike 1 against the test instance**

```bash
cd /mnt/data/nickp/Documents/Code/vikunja-projects/vikunja-custom-fields-plugin
./scripts/run-test-env.sh   # restarts container with the spike main.go
curl -s -H "Authorization: Bearer $JWT" \
  http://127.0.0.1:4176/api/v1/plugins/spike/json | jq .
sqlite3 db/vikunja.db ".schema spike_things"
sqlite3 db/vikunja.db "SELECT id, cfg FROM spike_things;"
```

- [ ] **Step 4: Decide field_config storage from spike 1 outcome**

Inspect the curl response and the `cfg` column value:
- If `roundtrip.cfg` equals `{required:true, min:5}` and the column holds JSON text → **rung 1 succeeded: `field_config` uses `xorm:"json null"`.**
- Else if the route errors on reflect/json → try rung 3: replace the `Cfg` field with `Cfg []byte \`xorm:"text null"\`` and marshal/unmarshal manually inside the handler; re-run. If that round-trips → **rung 3: `field_config` uses `text null` + `encoding/json` in the model methods.**
- Also consider rung 5: if you find any other mechanism that round-trips a typed struct (e.g. a reachable xorm `convert.Conversion`), pursue and record it.
- Record the chosen mechanism in `$CLAUDE_JOB_DIR/tmp/spike-result.md` (one line: `field_config: json` or `field_config: text+manual`).

- [ ] **Step 5: Spike 2 — session-arg method on a yaegi struct**

Restore the backup, then write a throwaway `main.go` that puts a method with a `*xorm.Engine`/`*xorm.Session` arg on a struct and calls it through a route.

```bash
cp "$CLAUDE_JOB_DIR/tmp/main.go.real-backup" main.go
```

```go
package main

import (
	"fmt"
	"net/http"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/plugins"
	"code.vikunja.io/api/pkg/user"
	"github.com/labstack/echo/v5"
	"xorm.io/xorm"
)

type SpikeRec struct{ ID int64 `xorm:"bigint autoincr not null unique pk"` }
func (SpikeRec) TableName() string { return "spike_recs" }

// The new reflect path: a method taking *xorm.Session + *user.User.
func (r *SpikeRec) Create(s *xorm.Session, u *user.User) error {
	_, err := s.Table("spike_recs").Insert(r)
	return err
}

type Spike2Plugin struct{}
func (p *Spike2Plugin) Name() string { return "spike2" }
func (p *Spike2Plugin) Version() string { return "0.0.1" }
func (p *Spike2Plugin) Init() error { return nil }
func (p *Spike2Plugin) Shutdown() error { return nil }
func (p *Spike2Plugin) Migrations() []*xormigrate.Migration {
	return []*xormigrate.Migration{{
		ID: "20260829170100-spike2", Description: "spike2",
		Migrate: func(tx *xorm.Engine) error { return tx.Table("spike_recs").Sync2(&SpikeRec{}) },
		Rollback: func(tx *xorm.Engine) error { return tx.DropTables("spike_recs") },
	}}
}
func (p *Spike2Plugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/spike2/method", func(c *echo.Context) error {
		u, err := user.GetCurrentUser(c)
		if err != nil { return c.JSON(http.StatusUnauthorized, map[string]string{"error":"unauth"}) }
		s := db.NewSession()
		defer s.Close()
		r := &SpikeRec{}
		if err := r.Create(s, u); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"method_err": err.Error()})
		}
		if err := s.Commit(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"commit_err": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]int64{"id": r.ID})
	})
}
var spike2 = &Spike2Plugin{}
func NewPlugin() plugins.Plugin { return spike2 }
func NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin { return spike2 }
func NewMigrationPlugin() plugins.MigrationPlugin { return spike2 }
var _ = fmt.Sprintf
```

- [ ] **Step 6: Run spike 2 and decide model-layer shape**

```bash
./scripts/run-test-env.sh
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/spike2/method | jq .
```

- If the response is `{"id": <N>}` → **method-on-struct works: Task 7 uses methods on `CustomFieldDefinition`.**
- If `method_err` is a yaegi reflect error → fallback to free functions `func Create(s *xorm.Session, d *CustomFieldDefinition, u *user.User) error`; write a quick probe to confirm. Also consider rung "any other viable path."
- Append to `$CLAUDE_JOB_DIR/tmp/spike-result.md` (one line: `model-layer: methods` or `model-layer: free-funcs`).

- [ ] **Step 7: Restore real main.go and verify clean state**

```bash
cp "$CLAUDE_JOB_DIR/tmp/main.go.real-backup" main.go
git diff --exit-code main.go   # must show no changes
git status --short
```

The spikes registered migration IDs `20260829170000-spike` and `20260829170100-spike2`.
After restoring `main.go` these IDs vanish from `Migrations()`, but Vikunja's
migrations table may still mark them applied. `run-test-env.sh` wipes and recreates
the DB (including the migrations table) on each run, so the next restart self-heals
— but to be safe, before the next restart drop the spike tables if they linger:

```bash
sqlite3 db/vikunja.db "DROP TABLE IF EXISTS spike_things; DROP TABLE IF EXISTS spike_recs;" 2>/dev/null || true
```

- [ ] **Step 8: Spike 3 — plugin event type passed to a host interface (the third reflect path)**

The events seam depends on a yaegi path neither spike above exercises: a
*plugin-defined* event struct implementing the host `events.Event` interface, passed
into host `events.DispatchOnCommit(s, evt)`. yaegi must bridge an interpreted type
to a host interface param — a distinct reflect risk from struct-field or
session-arg reflection. If it fails, the entire S3 event seam is dead regardless of
the C2 fix. Spike it now.

Write a third throwaway `main.go` (the backup is already safe) with one extra route:

```go
package main

import (
	"net/http"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/plugins"
	"github.com/labstack/echo/v5"
	"src.techknowlogick.com/xormigrate"
)

type SpikeEvent struct{ Msg string }
func (SpikeEvent) Name() string { return "spike.event" }

type Spike3Plugin struct{}
func (p *Spike3Plugin) Name() string    { return "spike3" }
func (p *Spike3Plugin) Version() string { return "0.0.1" }
func (p *Spike3Plugin) Init() error     { return nil }
func (p *Spike3Plugin) Shutdown() error { return nil }
func (p *Spike3Plugin) Migrations() []*xormigrate.Migration { return nil }
func (p *Spike3Plugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/spike3/event", func(c *echo.Context) error {
		s := db.NewSession()
		defer s.Close()
		events.DispatchOnCommit(s, &SpikeEvent{Msg: "hello"})
		if err := s.Commit(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"commit_err": err.Error()})
		}
		events.DispatchPending(c.Request().Context(), s)
		return c.JSON(http.StatusOK, map[string]string{"dispatched": "spike.event"})
	})
}
var spike3 = &Spike3Plugin{}
func NewPlugin() plugins.Plugin { return spike3 }
func NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin { return spike3 }
func NewMigrationPlugin() plugins.MigrationPlugin { return spike3 }
```

Run:

```bash
./scripts/run-test-env.sh
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/spike3/event | jq .
docker compose -f compose.test.yml logs 2>&1 | grep -iE "spike.event|panic|interface" | tail -5
```

- If the response is `{"dispatched":"spike.event"}` with no panic → **plugin event
  types bridge to the host interface: Task 5 + Task 7's `DispatchOnCommit` calls
  work.** Append `model-events: host-interface-ok` to `spike-result.md`.
- If it panics/errors on the interface bridge (look for "cannot convert" /
  "not an events.Event" / interface-assertion errors) → the events seam can't use
  plugin-defined types under yaegi. **Stop and surface to the user** before
  implementing Task 5: options include a host-defined event type the plugin
  populates (if a suitable one is in the symbol table), or deferring the event seam
  and noting it as a known gap for S3. Do not proceed with a dead seam.

Restore and clean up after the spike:

```bash
cp "$CLAUDE_JOB_DIR/tmp/main.go.real-backup" main.go
git diff --exit-code main.go
```

- [ ] **Step 9: Note the remaining unspiked reflect risks (taken on faith, watched during implementation)**

The three spikes cover the highest-risk yaegi paths. These lower-risk paths are
*taken on faith* — if any fails during implementation, spike it the same way:
- `s.Find(&slice)` on a yaegi-interpreted slice (ReadOne/ReadAll) — distinct from
  the `Get(&struct)` / `Insert(&struct)` the spikes exercise.
- `s.Table(...).Insert(&[]CustomFieldProject{})` — inserting a yaegi-interpreted
  slice (Create/Update assignment).
- `c.Bind(&req)` into a struct with a nested `FieldConfig` (with `*float64`
  pointer fields) — nested-struct JSON binding under yaegi.
- `s.Table("projects").Where(...).Exist(&models.Project{})` — a *host*-type bean
  through yaegi (vs the plugin-type beans in the spikes).
- `.OrderBy(...)`, `.AllCols()`, `.UseBool()` chain methods — low risk, registered
  via xorm, but unexercised by the spikes.

- [ ] **Step 10: Commit the spike result notes (not the throwaway code)**

The spikes themselves are throwaway (already restored). Commit only the recorded decisions so later tasks inherit them.

```bash
# Move the result into the repo so it travels with the branch:
cp "$CLAUDE_JOB_DIR/tmp/spike-result.md" docs/superpowers/plans/2026-08-29-s2-spike-result.md
git add docs/superpowers/plans/2026-08-29-s2-spike-result.md
git commit -m "docs(s2): record de-risking spike outcomes"
```

---

### Task 2: Data model structs + migration

Expand the three struct definitions and modify the existing migration to `Sync2` all four tables. Adopt the `field_config` storage decision from Task 1 (default code below shows the `json` rung; if spike chose `text+manual`, swap the tag to `text null` and defer marshaling to Task 7).

**Files:**
- Modify: `main.go:19-42` (structs) and `main.go:113-131` (migration)

**Interfaces:**
- Consumes: spike-result `field_config: json|text+manual`
- Produces: `CustomFieldDefinition`, `CustomFieldOption`, `CustomFieldProject`, `FieldConfig` types; `Migrations()` syncing four tables. Tasks 4–8 reference these exact names.

> **If the Task 1 spike chose `text+manual` (rung 3/4):** use this variant of the
> `CustomFieldDefinition` struct instead of the one below. The `FieldConfig` Go
> type stays the same (for the API/JSON shape), but it is **not** the stored
> column — a `FieldConfigRaw string \`xorm:"text null"\`` column holds the JSON,
> and `FieldConfig` becomes a transient `xorm:"-"` field the model methods
> marshal/unmarshal via `encoding/json`. This keeps the API + model shape
> upstream-identical; only the storage mechanism differs (the S1 raw-DDL analog).
> ```go
> type CustomFieldDefinition struct {
>     ID           int64       `xorm:"bigint autoincr not null unique pk" json:"id"`
>     Name         string      `xorm:"varchar(255) not null" json:"name"`
>     Type         string      `xorm:"varchar(50) not null" json:"type"`
>     Description  string      `xorm:"varchar(500) null" json:"description,omitempty"`
>     FieldConfigRaw string    `xorm:"text null" json:"-"`                 // stored JSON
>     FieldConfig  FieldConfig `xorm:"-" json:"field_config"`              // transient; marshaled in methods
>     DisplayOrder int         `xorm:"int not null default 0" json:"display_order"`
>     Created      time.Time   `xorm:"created not null" json:"-"`
>     Updated      time.Time   `xorm:"updated not null" json:"-"`
> }
> ```
> Then in Task 7, `Create`/`Update` must `json.Marshal(d.FieldConfig)` into
> `d.FieldConfigRaw` before writing, and `ReadOne`/`ReadAll` must
> `json.Unmarshal([]byte(d.FieldConfigRaw), &d.FieldConfig)` after reading. Add
> those marshal/unmarshal lines exactly where the `json` variant does a plain
> insert/get — the rest of the method bodies are identical. If the spike picked
> `json` (rung 1), ignore this entire note and use the struct below as written.

- [ ] **Step 1: Replace the two S1 structs with the three expanded/new ones + FieldConfig**

Replace `main.go:19-42` (the `CustomFieldDefinition` and `CustomFieldValue` blocks) with:

```go
// FieldConfig holds a field definition's scalar constraints. xorm serializes it
// to a JSON column (TEXT under sqlite, JSON/JSONB under mysql/postgres) — the
// same xorm:"json" mechanism api_tokens.APIPermissions uses. If the Task 1 spike
// chose text+manual, swap this tag to `xorm:"text null"` and marshal in the model
// methods (Task 7); the Go type stays the same.
type FieldConfig struct {
	Required  bool     `json:"required,omitempty"`
	Default   string   `json:"default,omitempty"`
	Min       *float64 `json:"min,omitempty"` // integer/decimal range; pointer so 0 ≠ unset
	Max       *float64 `json:"max,omitempty"`
	IsAPIOnly bool     `json:"is_api_only,omitempty"` // PRD stretch; S3 owns behavior
}

// CustomFieldDefinition is a single custom field's schema.
type CustomFieldDefinition struct {
	ID           int64       `xorm:"bigint autoincr not null unique pk" json:"id"`
	Name         string      `xorm:"varchar(255) not null" json:"name"`
	Type         string      `xorm:"varchar(50) not null" json:"type"`
	Description  string      `xorm:"varchar(500) null" json:"description,omitempty"`
	FieldConfig  FieldConfig `xorm:"json null" json:"field_config"`
	DisplayOrder int         `xorm:"int not null default 0" json:"display_order"`
	Created      time.Time   `xorm:"created not null" json:"-"`
	Updated      time.Time   `xorm:"updated not null" json:"-"`
}

func (CustomFieldDefinition) TableName() string { return "custom_field_definitions" }

// CustomFieldValue is one field's value on one task. S3 refines value typing and
// adds the UNIQUE(field, task) constraint and query indexes.
type CustomFieldValue struct {
	ID                      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64     `xorm:"bigint not null" json:"custom_field_definition_id"`
	TaskID                  int64     `xorm:"bigint not null" json:"task_id"`
	Value                   string    `xorm:"text" json:"value"`
	Created                 time.Time `xorm:"created not null" json:"-"`
	Updated                 time.Time `xorm:"updated not null" json:"-"`
}

func (CustomFieldValue) TableName() string { return "custom_field_values" }

// CustomFieldOption is one row of a select/multiselect field's option list.
type CustomFieldOption struct {
	ID                      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64     `xorm:"bigint not null index" json:"custom_field_definition_id"`
	Value                   string    `xorm:"varchar(255) not null" json:"value"`
	Label                   string    `xorm:"varchar(255) null" json:"label,omitempty"`
	DisplayOrder            int       `xorm:"int not null default 0" json:"display_order"`
	Created                 time.Time `xorm:"created not null" json:"-"`
	Updated                 time.Time `xorm:"updated not null" json:"-"`
}

func (CustomFieldOption) TableName() string { return "custom_field_options" }

// CustomFieldProject assigns a field to a project. ProjectID 0 is the sentinel
// for "all projects"; a specific ID means that project only. The handler enforces
// that a field has either the 0-row or ≥1 specific rows, never both.
type CustomFieldProject struct {
	ID                      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64     `xorm:"bigint not null index" json:"custom_field_definition_id"`
	ProjectID               int64     `xorm:"bigint not null index" json:"project_id"`
	Created                 time.Time `xorm:"created not null" json:"-"`
}

func (CustomFieldProject) TableName() string { return "custom_field_projects" }
```

- [ ] **Step 2: Modify the migration to Sync2 all four tables**

Replace `main.go:113-131` (the `Migrations()` method) with:

```go
// Migrations creates the plugin's tables. Vikunja runs plugin migrations
// automatically on startup after core migrations and before Init().
//
// Yaegi interprets plugin structs as anonymous reflect structs with no methods,
// so a TableName() method is invisible to xorm and the table name must be passed
// explicitly via tx.Table(name).Sync2(&T{}) — not Sync2(new(T)), which would
// produce an empty table name and a SQL syntax error. See upstream PR #3549.
//
// This migration is modified in place across stories (pattern B: unreleased
// feature, project_views precedent) until the plugin runs in production, after
// which further schema changes become append-only new migrations.
func (p *CustomFieldsPlugin) Migrations() []*xormigrate.Migration {
	return []*xormigrate.Migration{{
		ID:          "20260829160000-create-custom-field-tables",
		Description: "Create custom field definition, value, option, and project-assignment tables",
		Migrate: func(tx *xorm.Engine) error {
			if err := tx.Table("custom_field_definitions").Sync2(&CustomFieldDefinition{}); err != nil {
				return fmt.Errorf("custom-fields: sync definitions: %w", err)
			}
			if err := tx.Table("custom_field_values").Sync2(&CustomFieldValue{}); err != nil {
				return fmt.Errorf("custom-fields: sync values: %w", err)
			}
			if err := tx.Table("custom_field_options").Sync2(&CustomFieldOption{}); err != nil {
				return fmt.Errorf("custom-fields: sync options: %w", err)
			}
			if err := tx.Table("custom_field_projects").Sync2(&CustomFieldProject{}); err != nil {
				return fmt.Errorf("custom-fields: sync assignments: %w", err)
			}
			return nil
		},
		Rollback: func(tx *xorm.Engine) error {
			// Drop in dependency order: values + options + assignments reference definitions.
			return tx.DropTables("custom_field_values", "custom_field_options", "custom_field_projects", "custom_field_definitions")
		},
	}}
}
```

- [ ] **Step 3: Restart the test instance and verify the four tables + new columns**

```bash
./scripts/run-test-env.sh
echo "--- definitions columns ---"
sqlite3 db/vikunja.db "PRAGMA table_info(custom_field_definitions);"
echo "--- options table ---"
sqlite3 db/vikunja.db "PRAGMA table_info(custom_field_options);"
echo "--- projects table ---"
sqlite3 db/vikunja.db "PRAGMA table_info(custom_field_projects);"
echo "--- plugin loaded? ---"
docker compose -f compose.test.yml logs 2>&1 | grep -i "custom-fields\|plugin" | tail -5
```
Expected: `custom_field_definitions` has `description`, `field_config`, `display_order` columns; `custom_field_options` and `custom_field_projects` exist with the columns above; logs show the plugin loaded with no migration errors.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat(s2): expand field-definition schema and add options/assignment tables"
```

---

### Task 3: Custom errors

Define the plugin's error types following the `custom-errors.md` recipe (struct + `IsErrXxx` + `Error()` + `ErrCodeXxx` const). Handlers return `echo.NewHTTPError(code, msg)` (since `web.HTTPError`/`web` is unavailable). Reserve the 9000s range.

**Files:**
- Modify: `main.go` (add an errors block after the structs, before the whitelist)

**Interfaces:**
- Produces: error types and codes used by validation (Task 4) and handlers (Task 8):
  `ErrCustomFieldNameEmpty` (400, 9001), `ErrCustomFieldInvalidType` (400, 9002),
  `ErrCustomFieldOptionsForNonSelect` (400, 9003), `ErrCustomFieldDuplicateOption` (400, 9004),
  `ErrCustomFieldConstraintForType` (400, 9005), `ErrCustomFieldInvalidConstraint` (400, 9006),
  `ErrCustomFieldProjectNotFound` (400, 9007), `ErrCustomFieldNotFound` (404, 9008),
  `ErrCustomFieldGlobalConflict` (400, 9009).

- [ ] **Step 1: Add the error block**

Insert after `func (CustomFieldProject) TableName() ...`:

```go
// ── Errors (plugin-local 9000s range; web.HTTPError is unavailable to yaegi,
// so handlers translate these to echo.NewHTTPError(code, message). Upstream
// conversion: replace echo.NewHTTPError with HTTPError()/ErrCode per custom-errors.md.)

const (
	ErrCodeCustomFieldNameEmpty            = 9001
	ErrCodeCustomFieldInvalidType          = 9002
	ErrCodeCustomFieldOptionsForNonSelect  = 9003
	ErrCodeCustomFieldDuplicateOption      = 9004
	ErrCodeCustomFieldConstraintForType    = 9005
	ErrCodeCustomFieldInvalidConstraint    = 9006
	ErrCodeCustomFieldProjectNotFound      = 9007
	ErrCodeCustomFieldNotFound             = 9008
	ErrCodeCustomFieldGlobalConflict       = 9009
)

type ErrCustomFieldNameEmpty struct{}
func (ErrCustomFieldNameEmpty) Error() string { return "custom field name must not be empty" }

type ErrCustomFieldInvalidType struct{ Type string }
func (e ErrCustomFieldInvalidType) Error() string {
	return fmt.Sprintf("invalid custom field type: %s", e.Type)
}

type ErrCustomFieldOptionsForNonSelect struct{ Type string }
func (e ErrCustomFieldOptionsForNonSelect) Error() string {
	return fmt.Sprintf("options are only allowed for select/multiselect, not %s", e.Type)
}

type ErrCustomFieldDuplicateOption struct{ Value string }
func (e ErrCustomFieldDuplicateOption) Error() string {
	return fmt.Sprintf("duplicate option value: %s", e.Value)
}

type ErrCustomFieldConstraintForType struct{ Type, Constraint string }
func (e ErrCustomFieldConstraintForType) Error() string {
	return fmt.Sprintf("constraint %s is not valid for type %s", e.Constraint, e.Type)
}

type ErrCustomFieldInvalidConstraint struct{ Detail string }
func (e ErrCustomFieldInvalidConstraint) Error() string {
	return fmt.Sprintf("invalid constraint: %s", e.Detail)
}

type ErrCustomFieldProjectNotFound struct{ ID int64 }
func (e ErrCustomFieldProjectNotFound) Error() string {
	return fmt.Sprintf("project %d does not exist", e.ID)
}

type ErrCustomFieldNotFound struct{ ID int64 }
func (e ErrCustomFieldNotFound) Error() string {
	return fmt.Sprintf("custom field definition %d not found", e.ID)
}

// ErrCustomFieldGlobalConflict: assignment mixes the global sentinel (project_id=0)
// with specific projects, or carries the sentinel alongside specific rows.
type ErrCustomFieldGlobalConflict struct{}
func (ErrCustomFieldGlobalConflict) Error() string {
	return "a field is either global (all projects) or assigned to specific projects, not both"
}
```

- [ ] **Step 2: Commit**

```bash
git add main.go
git commit -m "feat(s2): add custom field definition error types"
```

---

### Task 4: Validation (pure functions)

Pure validation funcs over a parsed definition + its options/project_ids. They return the Task 3 errors. No DB except the project-existence check (which needs a session). Two flavors: `validateDefinition` (no DB) and `validateAssignment` (DB, checks `projects`).

**Files:**
- Modify: `main.go` (add after the errors block)

**Interfaces:**
- Consumes: error types from Task 3; `models.Project` (registered) for project existence.
- Produces:
  `func validateDefinition(d *CustomFieldDefinition, options []CustomFieldOption) error`
  `func validateAssignment(s *xorm.Session, projectIDs []int64) error`
  `var validFieldTypes = map[string]struct{}{...}` — Tasks 7 and 8 call these before writing.

- [ ] **Step 1: Add valid types map + validateDefinition**

```go
var validFieldTypes = map[string]struct{}{
	"text": {}, "textarea": {}, "integer": {}, "decimal": {},
	"date": {}, "datetime": {}, "select": {}, "multiselect": {},
	"checkbox": {}, "url": {},
}

func isSelectLike(t string) bool {
	return t == "select" || t == "multiselect"
}

// validateDefinition checks the type/name/options/constraints of a definition.
// It does no DB access. project assignment is validated separately
// (validateAssignment) because that needs a session.
func validateDefinition(d *CustomFieldDefinition, options []CustomFieldOption) error {
	if strings.TrimSpace(d.Name) == "" {
		return ErrCustomFieldNameEmpty{}
	}
	if _, ok := validFieldTypes[d.Type]; !ok {
		return ErrCustomFieldInvalidType{Type: d.Type}
	}
	if len(options) > 0 && !isSelectLike(d.Type) {
		return ErrCustomFieldOptionsForNonSelect{Type: d.Type}
	}
	seen := map[string]struct{}{}
	for _, o := range options {
		if strings.TrimSpace(o.Value) == "" {
			return ErrCustomFieldInvalidConstraint{Detail: "option value must not be empty"}
		}
		if _, dup := seen[o.Value]; dup {
			return ErrCustomFieldDuplicateOption{Value: o.Value}
		}
		seen[o.Value] = struct{}{}
	}
	if (d.FieldConfig.Min != nil || d.FieldConfig.Max != nil) && !(d.Type == "integer" || d.Type == "decimal") {
		return ErrCustomFieldConstraintForType{Type: d.Type, Constraint: "min/max"}
	}
	if d.FieldConfig.Min != nil && d.FieldConfig.Max != nil && *d.FieldConfig.Min > *d.FieldConfig.Max {
		return ErrCustomFieldInvalidConstraint{Detail: "min must not exceed max"}
	}
	return nil
}

// validateAssignment confirms each specific project exists. The global sentinel
// (empty/nil projectIDs) needs no such check. Mixing sentinel with specific IDs
// is a client error the handler prevents before calling here.
func validateAssignment(s *xorm.Session, projectIDs []int64) error {
	for _, pid := range projectIDs {
		has, err := s.Table("projects").Where("id = ?", pid).Exist(&models.Project{})
		if err != nil {
			return fmt.Errorf("custom-fields: check project %d: %w", pid, err)
		}
		if !has {
			return ErrCustomFieldProjectNotFound{ID: pid}
		}
	}
	return nil
}
```

- [ ] **Step 2: Restart and smoke-verify the plugin still loads (no syntax errors)**

```bash
./scripts/run-test-env.sh
docker compose -f compose.test.yml logs 2>&1 | grep -iE "error|custom-fields|Loaded plugin" | tail -8
```
Expected: `Loaded plugin custom-fields v0.1.0`; no errors.

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat(s2): add field-definition validation"
```

---

### Task 5: Events — SKIPPED (deferred)

**SKIPPED.** Task 1 spike 3 found plugin-defined event types are unmarshalable by
host `json.Marshal` under yaegi (`json: unsupported type: func() string`), so they
queue but never publish. The user chose to defer the seam rather than add
host-defined event types to the fork. S2 registers no event types, imports
`pkg/events` nowhere, and dispatches nothing. Tasks 7 and 8 contain no
`events.*` calls. The reference event design and the verified two-call dispatch
mechanism are retained in the spec's Events section for whatever S3 builds.
No code, no commit for this task.

---

### Task 6: Permissions (CanX methods)

The `CanX` methods wrap `IsManager`. Every handler calls the relevant `CanX` first; false → 403. Adopt the model-layer shape from Task 1's spike 2 (methods shown below; if spike chose free-functions, make these package-level funcs taking `*user.User`).

**Files:**
- Modify: `main.go` (add after events)

**Interfaces:**
- Consumes: `IsManager` (already present, `main.go:80`).
- Produces: `(*CustomFieldDefinition).CanCreate/CanRead/CanUpdate/CanDelete(*xorm.Session, *user.User) (bool, error)` — called by Task 8 handlers.

- [ ] **Step 1: Add the CanX methods**

```go
// ── Permissions. All field-definition management is whitelist-gated. These
// mirror Vikunja's Permissions interface (CanCreate/CanRead/CanUpdate/CanDelete);
// the only deviation is *user.User instead of web.Auth (web is unavailable to
// yaegi) — an upstream-conversion point.

func (d *CustomFieldDefinition) CanCreate(s *xorm.Session, u *user.User) (bool, error) {
	return IsManager(u.Username), nil
}

func (d *CustomFieldDefinition) CanRead(s *xorm.Session, u *user.User) (bool, error) {
	return IsManager(u.Username), nil
}

func (d *CustomFieldDefinition) CanUpdate(s *xorm.Session, u *user.User) (bool, error) {
	return IsManager(u.Username), nil
}

func (d *CustomFieldDefinition) CanDelete(s *xorm.Session, u *user.User) (bool, error) {
	return IsManager(u.Username), nil
}
```

- [ ] **Step 2: Add the xorm import if not present**

`xorm.io/xorm` is already imported (`main.go:16`); `*xorm.Session` resolves there. No import change needed.

- [ ] **Step 3: Restart and verify load**

```bash
./scripts/run-test-env.sh
docker compose -f compose.test.yml logs 2>&1 | grep -iE "error|Loaded plugin custom-fields" | tail -5
```

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat(s2): add whitelist permission gates for field definitions"
```

---

### Task 7: Model CRUD methods

The DB layer. Each method takes `*xorm.Session` + `*user.User` and does the xorm work. **No events** (Task 5 skipped — events deferred). The handler (Task 8) owns the session lifecycle (open, call method, commit, close). **Adopt the spike-2 shape** (methods-on-struct; spike 2 confirmed this works). These methods do NOT call `CanX` — the handler does.

**Files:**
- Modify: `main.go` (add after permissions)

**Interfaces:**
- Consumes: structs (Task 2), errors (Task 3), `validateDefinition`/`validateAssignment` (Task 4). **No events** (Task 5 skipped).
- Produces (methods, per Task 1 spike-2 = methods-on-struct):
  `func (d *CustomFieldDefinition) Create(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error)`
  `func (d *CustomFieldDefinition) ReadOne(s *xorm.Session) (*CustomFieldDefinition, []CustomFieldOption, []int64, error)`
  `func ReadAll(s *xorm.Session, projectID int64) ([]CustomFieldDefinition, error)`
  `func (d *CustomFieldDefinition) Update(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error)`
  `func (d *CustomFieldDefinition) Delete(s *xorm.Session) error`
  **No `events.*` calls anywhere** — events are deferred. The handler (Task 8) owns the session: open → CanX → method → commit → close. No `old` param on Update (it existed only for the deferred event).

**R6 (DRY helpers):** extract two shared helpers and use them in BOTH Create and Update (eliminates verbatim-duplicated options/assignment-insert blocks):
`func setOptions(s *xorm.Session, defID int64, t string, options []CustomFieldOption) error` — delete existing options for defID (no-op on Create), then if `isSelectLike(t)` insert the options (set each `CustomFieldDefinitionID = defID`).
`func setAssignment(s *xorm.Session, defID int64, projectIDs []int64) error` — delete existing assignment for defID (no-op on Create), then insert `resolveProjectIDs(projectIDs)` rows.
Create calls them directly (delete-existing is a no-op when none exist); Update calls them after its own definition-row update. Keep `resolveProjectIDs` as written.

- [ ] **Step 1: Add Create**

```go
// ── Model CRUD. Handlers (Task 8) own the session: open, call CanX, call these,
// commit, close. These methods never open/commit the session themselves. No events
// (deferred — see spec Events section).

// resolveProjectIDs enforces mutual exclusivity: empty ⟹ global sentinel [0];
// non-empty ⟹ those IDs. The caller must not pass both a sentinel and specifics.
func resolveProjectIDs(projectIDs []int64) []int64 {
	if len(projectIDs) == 0 {
		return []int64{0} // sentinel: all projects
	}
	return projectIDs
}

// setOptions replaces a definition's option rows. delete-existing is a no-op on
// Create (none exist yet); on Update it clears the old set before re-inserting.
// Options are only written for select/multiselect.
func setOptions(s *xorm.Session, defID int64, t string, options []CustomFieldOption) error {
	if _, err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", defID).Delete(&CustomFieldOption{}); err != nil {
		return fmt.Errorf("custom-fields: clear options: %w", err)
	}
	if !isSelectLike(t) || len(options) == 0 {
		return nil
	}
	for i := range options {
		options[i].CustomFieldDefinitionID = defID
	}
	if _, err := s.Table("custom_field_options").Insert(&options); err != nil {
		return fmt.Errorf("custom-fields: insert options: %w", err)
	}
	return nil
}

// setAssignment replaces a definition's project assignment. delete-existing is a
// no-op on Create; on Update it clears the old set before re-inserting.
func setAssignment(s *xorm.Session, defID int64, projectIDs []int64) error {
	if _, err := s.Table("custom_field_projects").Where("custom_field_definition_id = ?", defID).Delete(&CustomFieldProject{}); err != nil {
		return fmt.Errorf("custom-fields: clear assignment: %w", err)
	}
	assign := resolveProjectIDs(projectIDs)
	rows := make([]CustomFieldProject, len(assign))
	for i, pid := range assign {
		rows[i] = CustomFieldProject{CustomFieldDefinitionID: defID, ProjectID: pid}
	}
	if _, err := s.Table("custom_field_projects").Insert(&rows); err != nil {
		return fmt.Errorf("custom-fields: insert assignment: %w", err)
	}
	return nil
}

func (d *CustomFieldDefinition) Create(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error) {
	if err := validateDefinition(d, options); err != nil {
		return nil, err
	}
	if err := validateAssignment(s, projectIDs); err != nil {
		return nil, err
	}
	if _, err := s.Table("custom_field_definitions").Insert(d); err != nil {
		return nil, fmt.Errorf("custom-fields: insert definition: %w", err)
	}
	if err := setOptions(s, d.ID, d.Type, options); err != nil {
		return nil, err
	}
	if err := setAssignment(s, d.ID, projectIDs); err != nil {
		return nil, err
	}
	return d, nil
}
```

- [ ] **Step 2: Add ReadOne and ReadAll**

```go
// ReadOne fetches a definition with its options and project assignment. Returns
// the definition, its options (empty for non-select), and its project_ids (empty
// slice if global — callers treat empty as "all projects").
func (d *CustomFieldDefinition) ReadOne(s *xorm.Session) (*CustomFieldDefinition, []CustomFieldOption, []int64, error) {
	has, err := s.Table("custom_field_definitions").ID(d.ID).Get(d)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("custom-fields: get definition: %w", err)
	}
	if !has {
		return nil, nil, nil, ErrCustomFieldNotFound{ID: d.ID}
	}
	var opts []CustomFieldOption
	if err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", d.ID).OrderBy("display_order asc").Find(&opts); err != nil {
		return nil, nil, nil, fmt.Errorf("custom-fields: get options: %w", err)
	}
	var assigns []CustomFieldProject
	if err := s.Table("custom_field_projects").Where("custom_field_definition_id = ?", d.ID).Find(&assigns); err != nil {
		return nil, nil, nil, fmt.Errorf("custom-fields: get assignment: %w", err)
	}
	pids := make([]int64, 0, len(assigns))
	for _, a := range assigns {
		if a.ProjectID != 0 { // omit the global sentinel from the response list
			pids = append(pids, a.ProjectID)
		}
	}
	return d, opts, pids, nil
}

// ReadAll lists all definitions. If projectID > 0, filters to fields that apply
// to that project (global sentinel OR a row for that project).
func ReadAll(s *xorm.Session, projectID int64) ([]CustomFieldDefinition, error) {
	var defs []CustomFieldDefinition
	if projectID == 0 {
		if err := s.Table("custom_field_definitions").OrderBy("display_order asc").Find(&defs); err != nil {
			return nil, fmt.Errorf("custom-fields: list definitions: %w", err)
		}
		return defs, nil
	}
	// Fields applying to projectID: those with a custom_field_projects row where
	// project_id = projectID OR project_id = 0 (global).
	subQuery := "(SELECT DISTINCT custom_field_definition_id FROM custom_field_projects WHERE project_id = ? OR project_id = 0)"
	if err := s.Table("custom_field_definitions").Where("id IN "+subQuery, projectID).OrderBy("display_order asc").Find(&defs); err != nil {
		return nil, fmt.Errorf("custom-fields: list definitions by project: %w", err)
	}
	return defs, nil
}
```

- [ ] **Step 3: Add Update (full-replace)**

```go
// Update replaces the definition and its options + assignment wholesale (PUT
// full-replace). It does NOT touch custom_field_values (S3's table). No event
// (deferred). The handler captures no `old` state.
func (d *CustomFieldDefinition) Update(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error) {
	has, err := s.Table("custom_field_definitions").ID(d.ID).Exist(&CustomFieldDefinition{})
	if err != nil {
		return nil, fmt.Errorf("custom-fields: check old definition: %w", err)
	}
	if !has {
		return nil, ErrCustomFieldNotFound{ID: d.ID}
	}
	if err := validateDefinition(d, options); err != nil {
		return nil, err
	}
	if err := validateAssignment(s, projectIDs); err != nil {
		return nil, err
	}
	// AllCols writes every column including zero values (xorm's Update skips
	// zero-valued cols by default, which would break PUT full-replace for
	// cleared fields, display_order=0, field_config.required=false, etc.).
	// UseBool ensures the bools inside FieldConfig are not skipped either.
	// (Mirrors upstream label.go's explicit .Cols(...) approach.)
	if _, err := s.Table("custom_field_definitions").ID(d.ID).AllCols().UseBool().Update(d); err != nil {
		return nil, fmt.Errorf("custom-fields: update definition: %w", err)
	}
	if err := setOptions(s, d.ID, d.Type, options); err != nil {
		return nil, err
	}
	if err := setAssignment(s, d.ID, projectIDs); err != nil {
		return nil, err
	}
	return d, nil
}
```

- [ ] **Step 4: Add Delete**

```go
// Delete hard-cascades the definition's OWN rows: definition + options +
// assignment. It does NOT touch custom_field_values (S3's table). No event
// (deferred).
func (d *CustomFieldDefinition) Delete(s *xorm.Session) error {
	has, err := s.Table("custom_field_definitions").ID(d.ID).Exist(&CustomFieldDefinition{})
	if err != nil {
		return fmt.Errorf("custom-fields: check definition: %w", err)
	}
	if !has {
		return ErrCustomFieldNotFound{ID: d.ID}
	}
	if _, err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", d.ID).Delete(&CustomFieldOption{}); err != nil {
		return fmt.Errorf("custom-fields: delete options: %w", err)
	}
	if _, err := s.Table("custom_field_projects").Where("custom_field_definition_id = ?", d.ID).Delete(&CustomFieldProject{}); err != nil {
		return fmt.Errorf("custom-fields: delete assignment: %w", err)
	}
	if _, err := s.Table("custom_field_definitions").ID(d.ID).Delete(&CustomFieldDefinition{}); err != nil {
		return fmt.Errorf("custom-fields: delete definition: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Restart and verify load**

```bash
./scripts/run-test-env.sh
docker compose -f compose.test.yml logs 2>&1 | grep -iE "error|Loaded plugin custom-fields" | tail -5
```

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "feat(s2): add field-definition CRUD model methods"
```

---

### Task 8: Handlers + route registration

Thin Echo handlers: parse body → `CanX` (403 if false) → model method → commit → dispatch event → JSON response. Register the five routes; remove S8's temporary `managerHandler`.

**Files:**
- Modify: `main.go:133-161` (routes + `managerHandler`) — replace with the new handlers + registration.

**Interfaces:**
- Consumes: model methods (Task 7), errors (Task 3). **No events** (Task 5 skipped).
- Produces: five registered routes; `managerHandler` removed.

- [ ] **Step 1: Add request type + response-map helpers**

```go
// ── Handlers. Thin: parse → CanX (403) → model → commit → JSON map. No events
// (deferred). web.HTTPError is unavailable, so handlers use echo.NewHTTPError.
//
// R7 (spike 1 caveat): interpreted structs serialize as {} through c.JSON, so
// responses are built as maps field-by-field, never by echoing a struct. xorm
// DB read/write of interpreted structs works; only the c.JSON path is affected.

type definitionRequest struct {
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Description  string              `json:"description"`
	FieldConfig  FieldConfig         `json:"field_config"`
	DisplayOrder int                 `json:"display_order"`
	Options      []CustomFieldOption `json:"options"`
	ProjectIDs   []int64             `json:"project_ids"`
}

// fieldConfigMap builds the field_config map with concrete float64 values
// (dereferenced pointers) so c.JSON never has to marshal a yaegi-wrapped *float64.
func fieldConfigMap(fc FieldConfig) map[string]interface{} {
	m := map[string]interface{}{
		"required":    fc.Required,
		"default":     fc.Default,
		"is_api_only": fc.IsAPIOnly,
	}
	if fc.Min != nil {
		m["min"] = *fc.Min
	}
	if fc.Max != nil {
		m["max"] = *fc.Max
	}
	return m
}

// definitionFieldsMap is the definition fields only (for list items, no relations).
func definitionFieldsMap(d *CustomFieldDefinition) map[string]interface{} {
	return map[string]interface{}{
		"id":            d.ID,
		"name":          d.Name,
		"type":          d.Type,
		"description":   d.Description,
		"field_config":  fieldConfigMap(d.FieldConfig),
		"display_order": d.DisplayOrder,
	}
}

// definitionToMap is the full single-resource response: definition fields +
// resolved options + project_ids ([] for global). Used by create/read/update.
func definitionToMap(d *CustomFieldDefinition, opts []CustomFieldOption, pids []int64) map[string]interface{} {
	m := definitionFieldsMap(d)
	optMaps := make([]map[string]interface{}, 0, len(opts))
	for _, o := range opts {
		optMaps = append(optMaps, map[string]interface{}{
			"id":            o.ID,
			"custom_field_definition_id": o.CustomFieldDefinitionID,
			"value":         o.Value,
			"label":         o.Label,
			"display_order": o.DisplayOrder,
		})
	}
	m["options"] = optMaps
	m["project_ids"] = pids
	return m
}

// validateProjectIDList (R4) rejects a client-supplied project_id == 0 — the
// reserved internal sentinel. Clients express "all projects" via [] (omitted),
// never via [0]. This makes ErrCustomFieldGlobalConflict reachable and separates
// the mutual-exclusivity guard from validateAssignment's existence check.
func validateProjectIDList(ids []int64) error {
	for _, pid := range ids {
		if pid == 0 {
			return ErrCustomFieldGlobalConflict{}
		}
	}
	return nil
}
```

- [ ] **Step 2: Add a helper to translate model errors to HTTP**

```go
func toHTTPError(err error) error {
	switch err.(type) {
	case ErrCustomFieldNameEmpty:
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case ErrCustomFieldInvalidType:
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case ErrCustomFieldOptionsForNonSelect:
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case ErrCustomFieldDuplicateOption:
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case ErrCustomFieldConstraintForType:
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case ErrCustomFieldInvalidConstraint:
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case ErrCustomFieldProjectNotFound:
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case ErrCustomFieldGlobalConflict:
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case ErrCustomFieldNotFound:
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
}
```

- [ ] **Step 3: Add createHandler**

```go
func createHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	var req definitionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateProjectIDList(req.ProjectIDs); err != nil { // R4: reject sentinel
		return toHTTPError(err)
	}
	d := &CustomFieldDefinition{
		Name: req.Name, Type: req.Type, Description: req.Description,
		FieldConfig: req.FieldConfig, DisplayOrder: req.DisplayOrder,
	}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanCreate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	created, err := d.Create(s, u, req.Options, req.ProjectIDs)
	if err != nil {
		return toHTTPError(err)
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// R5: re-read for a canonical response (real option IDs, resolved project_ids).
	rd := &CustomFieldDefinition{ID: created.ID}
	def, opts, pids, err := rd.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusCreated, definitionToMap(def, opts, pids))
}
```

- [ ] **Step 4: Add readOneHandler, listHandler**

```go
func readOneHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	d := &CustomFieldDefinition{ID: id}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	def, opts, pids, err := d.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, definitionToMap(def, opts, pids))
}

func listHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	d := &CustomFieldDefinition{}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	pidStr := c.QueryParam("project_id")
	pid := int64(0)
	if pidStr != "" {
		pid, err = strconv.ParseInt(pidStr, 10, 64)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid project_id")
		}
	}
	defs, err := ReadAll(s, pid)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// R7: build a []map (interpreted structs would serialize as {}).
	out := make([]map[string]interface{}, 0, len(defs))
	for i := range defs {
		out = append(out, definitionFieldsMap(&defs[i]))
	}
	return c.JSON(http.StatusOK, out)
}
```

- [ ] **Step 5: Add updateHandler, deleteHandler**

```go
func updateHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	var req definitionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateProjectIDList(req.ProjectIDs); err != nil { // R4: reject sentinel
		return toHTTPError(err)
	}
	d := &CustomFieldDefinition{
		ID: id, Name: req.Name, Type: req.Type, Description: req.Description,
		FieldConfig: req.FieldConfig, DisplayOrder: req.DisplayOrder,
	}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanUpdate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	if _, err := d.Update(s, u, req.Options, req.ProjectIDs); err != nil {
		return toHTTPError(err)
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// R5: re-read for a canonical response (no old-state capture — events deferred).
	rd := &CustomFieldDefinition{ID: id}
	def, opts, pids, err := rd.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, definitionToMap(def, opts, pids))
}

func deleteHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	d := &CustomFieldDefinition{ID: id}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanDelete(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	if err := d.Delete(s); err != nil {
		return toHTTPError(err)
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}
```

- [ ] **Step 6: Add `db` + `strconv` imports and replace route registration**

Add `"code.vikunja.io/api/pkg/db"` and `"strconv"` to the import block (handlers use `db.NewSession()` and `strconv.ParseInt`). Do **NOT** add a `pkg/events` import — events are deferred. **R3:** the line anchors below (`main.go:133-138`, `:148-161`) are stale after Tasks 2–7 inserted code above — locate `RegisterAuthenticatedRoutes` and `managerHandler` by function name, not line number. Replace the `RegisterAuthenticatedRoutes` method with:

```go
// RegisterAuthenticatedRoutes mounts the plugin's authenticated routes on the
// /api/v1/plugins/ group. The temporary S8 manager route is removed; IsManager
// is now exercised on the real field-definition endpoints.
func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/health", healthHandler) // S1 throwaway load-proof
	g.POST("/custom-fields/definitions", createHandler)
	g.GET("/custom-fields/definitions", listHandler)
	g.GET("/custom-fields/definitions/:id", readOneHandler)
	g.PUT("/custom-fields/definitions/:id", updateHandler)
	g.DELETE("/custom-fields/definitions/:id", deleteHandler)
}
```

- [ ] **Step 7: Remove the temporary managerHandler**

Delete the `managerHandler` function (`main.go:148-161`) entirely. Its verification role is now covered by the 403/200 paths on the real endpoints.

- [ ] **Step 8: Restart and verify routes load**

```bash
./scripts/run-test-env.sh
docker compose -f compose.test.yml logs 2>&1 | grep -iE "error|Loaded plugin custom-fields" | tail -5
```

- [ ] **Step 9: Commit**

```bash
git add main.go
git commit -m "feat(s2): add field-definition CRUD endpoints and remove temp manager route"
```

---

### Task 9: Test-instance project seeding + AC verification

Seed a project so the assignment-validation AC can run, then execute the full acceptance-criteria verification table from the spec.

**Files:**
- Modify: `scripts/run-test-env.sh` (add a project-seed step after the users seed, Step 3.5)

**Interfaces:**
- Produces: a seeded project (id 1) owned by `testuser`, so AC#2 and AC#6 (project assignment) have a real project to reference.

- [ ] **Step 1: Add a project-seed step to run-test-env.sh**

Seed the project via the real `POST /api/v2/projects` endpoint (with the manager JWT) rather than raw-row seeding via the testing token. The `projects` table has version-dependent NOT-NULL columns and defaults that the API sets correctly but a hand-built PUT row may not — using the API avoids constraint failures. Insert after the login block (Step 4), so `$JWT` is available:

```bash
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
```

> The created project's id is assigned by the API (auto-increment, likely 1 on a
> fresh DB) and exported as `$PROJECT_ID`. Use `$PROJECT_ID` in the
> assignment-validation curls below instead of hardcoding `1`. If the create fails
> (e.g. the v2 project-create shape differs), inspect `sqlite3 db/vikunja.db
> "PRAGMA table_info(projects);"` and the v2 projects route at
> `pkg/routes/api/v2/projects.go` for the correct body shape.

- [ ] **Step 2: Restart and confirm the project seeded**

```bash
./scripts/run-test-env.sh
sqlite3 db/vikunja.db "SELECT id, title, owner_id FROM projects;"
```
Expected: a row with id 1.

- [ ] **Step 3: AC#1 — create a definition (whitelisted user)**

```bash
ID=$(curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name":"Cost Center","type":"select","field_config":{"required":true,"default":"draft"},"display_order":3,"options":[{"value":"draft","label":"Draft","display_order":0},{"value":"final","label":"Final","display_order":1}],"project_ids":['"$PROJECT_ID"']}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions | jq -r .id)
export ID
echo "created id=$ID"
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/$ID | jq .
```
Expected: `$ID` is a non-empty number; the follow-up GET shows `name` Cost Center, `field_config`, `options` (2), `project_ids` `[1]`. (This `$ID` is reused in Steps 5 and 6.)

- [ ] **Step 4: AC#2 — list (unfiltered and by project)**

```bash
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions | jq .
curl -s -H "Authorization: Bearer $JWT" "http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions?project_id=$PROJECT_ID" | jq .
```
Expected: bare JSON array; the second contains the Cost Center field. Then create a global field and confirm it appears under `?project_id=$PROJECT_ID`:

```bash
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name":"Global Note","type":"text","project_ids":[]}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions | jq .
curl -s -H "Authorization: Bearer $JWT" "http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions?project_id=$PROJECT_ID" | jq '.[] | .name'
```
Expected: both `Cost Center` and `Global Note` appear under project $PROJECT_ID.

- [ ] **Step 5: AC#3 — update (PUT full-replace)**

```bash
# Use the id from AC#1.
curl -s -X PUT -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name":"Cost Center v2","type":"select","field_config":{"required":false},"display_order":5,"options":[{"value":"draft","label":"Draft","display_order":0}],"project_ids":[]}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/$ID | jq .
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/$ID | jq .
```
Expected: 200; GET shows renamed field, one option, `project_ids` `[]` (now global), updated `display_order`.

- [ ] **Step 6: AC#4 — delete; verify own rows gone, values untouched**

```bash
curl -s -X DELETE -H "Authorization: Bearer $JWT" \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/$ID -w "\n%{http_code}\n"
sqlite3 db/vikunja.db "SELECT count(*) FROM custom_field_definitions WHERE id=$ID;"
sqlite3 db/vikunja.db "SELECT count(*) FROM custom_field_options WHERE custom_field_definition_id=$ID;"
sqlite3 db/vikunja.db "SELECT count(*) FROM custom_field_projects WHERE custom_field_definition_id=$ID;"
sqlite3 db/vikunja.db "SELECT count(*) FROM custom_field_values;"
```
Expected: 204; the three counts are 0; `custom_field_values` count is 0 (untouched — there were none to delete; S2 never touches it).

- [ ] **Step 7: AC#5 — non-whitelisted user gets 403 on all five verbs**

```bash
curl -s -o /dev/null -w "POST:%{http_code}\n" -X POST -H "Authorization: Bearer $JWT_OTHERUSER" -H "Content-Type: application/json" -d '{"name":"x","type":"text"}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions
curl -s -o /dev/null -w "GET:%{http_code}\n" -H "Authorization: Bearer $JWT_OTHERUSER" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions
curl -s -o /dev/null -w "GET1:%{http_code}\n" -H "Authorization: Bearer $JWT_OTHERUSER" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/$ID
curl -s -o /dev/null -w "PUT:%{http_code}\n" -X PUT -H "Authorization: Bearer $JWT_OTHERUSER" -H "Content-Type: application/json" -d '{"name":"x","type":"text"}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/$ID
curl -s -o /dev/null -w "DELETE:%{http_code}\n" -X DELETE -H "Authorization: Bearer $JWT_OTHERUSER" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/$ID
```
Expected: all five are `403`.

- [ ] **Step 8: AC#6 — validation errors**

```bash
# Empty name → 400
curl -s -o /dev/null -w "empty-name:%{http_code}\n" -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" -d '{"name":"","type":"text"}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions
# Bad type → 400
curl -s -o /dev/null -w "bad-type:%{http_code}\n" -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" -d '{"name":"x","type":"bogus"}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions
# Options on non-select → 400
curl -s -o /dev/null -w "opts-nonselect:%{http_code}\n" -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" -d '{"name":"x","type":"text","options":[{"value":"a"}]}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions
# min>max → 400
curl -s -o /dev/null -w "minmax:%{http_code}\n" -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" -d '{"name":"x","type":"integer","field_config":{"min":10,"max":5}}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions
# duplicate option value → 400
curl -s -o /dev/null -w "dup-opt:%{http_code}\n" -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" -d '{"name":"x","type":"select","options":[{"value":"a"},{"value":"a"}]}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions
# nonexistent project_id → 400
curl -s -o /dev/null -w "bad-project:%{http_code}\n" -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" -d '{"name":"x","type":"text","project_ids":[9999]}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions
```
Expected: all `400`.

- [ ] **Step 9: AC#7 — response shapes**

Re-run AC#1's curl and confirm: keys are snake_case (`field_config`, `display_order`, `project_ids`); list endpoint (AC#2) returns a bare JSON array (confirm against a native list endpoint if shape is uncertain):

```bash
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/projects | jq 'type'   # native list shape reference
```

- [ ] **Step 10: Commit**

```bash
git add scripts/run-test-env.sh
git commit -m "test(s2): seed project and verify field-definition acceptance criteria"
```

---

## Self-Review

**Spec coverage:**
- AC#1 (create) → Task 8 createHandler + Task 9 Step 3. ✓
- AC#2 (list, filter by project) → ReadAll + listHandler + Task 9 Steps 4. ✓
- AC#3 (update) → Update + updateHandler + Task 9 Step 5. ✓
- AC#4 (delete) → Delete + deleteHandler + Task 9 Step 6. ✓
- AC#5 (non-whitelisted 403) → CanX in every handler + Task 9 Step 7. ✓
- AC#6 (validation) → Task 4 validate* + Task 9 Step 8. ✓
- AC#7 (snake_case, envelope) → struct json tags + Task 9 Step 9. ✓
- Data model (3 tables) → Task 2. ✓
- Migration (modify-existing) → Task 2 Step 2. ✓
- Events → Task 5 SKIPPED (deferred per spike 3; spec amended). No events in S2. ✓
- S3 seams (guard insertion points) → CanUpdate/CanDelete in Task 6 (where S3 adds a block-if-values check). ✓
- Spikes (2, fallback ladder) → Task 1. ✓
- Remove managerHandler → Task 8 Step 7. ✓
- `field_config` spike-dependent → Task 2 Step 1 note + Task 1. ✓

**Placeholder scan:** No TBD/TODO in plan body. One explicit note in Task 7 Step 3 ("pick one and keep it consistent") — that is a real engineering choice with both options shown, not a gap. Task 9 Step 1 has a genuine contingency note about `projects` column names (version-dependent) with a concrete fallback command — not a placeholder.

**Type consistency:** `CustomFieldDefinition`/`CustomFieldOption`/`CustomFieldProject`/`FieldConfig` defined in Task 2, used identically in Tasks 4/7/8. Error types defined Task 3, switched-on in Task 8 `toHTTPError`. Events deferred (Task 5 skipped — no event types). `definitionRequest` + response-map helpers (`definitionToMap`/`definitionFieldsMap`/`fieldConfigMap`/`validateProjectIDList`) defined Task 8, used in handlers. `setOptions`/`setAssignment`/`resolveProjectIDs` defined Task 7 (R6). `IsManager` from `main.go:80` (existing) consumed in Task 6. Method signatures match between Task 7 (defines) and Task 8 (calls). ✓

No gaps found. Plan is complete.

## Required Skills

- **`git-flow`** — the whole plan executes on `feature/s2-field-definition-api-design` (already started); finish via `git flow feature finish` after review/approval, never commit to `develop`.

## Recommended Skills

- **`golang-testing`** — for reasoning about test design (note: plugin source is not standalone-testable; tests are integration via the test instance per the spec).
- **`golang-error-handling`** — for the Task 3 error-type pattern and the Task 8 `toHTTPError` translation.
- **`golang-database`** — for the xorm session/query patterns in Tasks 7–8 (remember: no `builder`, string-chaining only).
- **`migration`** (from the vikunja repo skills) — for the Task 2 migration; the spec's `Sync2`-vs-`partialSync` divergence from upstream is recorded and watched.