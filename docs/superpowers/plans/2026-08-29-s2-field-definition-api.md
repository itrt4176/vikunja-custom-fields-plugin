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
- **`field_config` storage is spike-dependent** (Task 1 decides): `xorm:"json null"` (preferred) or `text null` + manual marshal (fallback). Task 2 adopts the spike outcome.
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
| Event types + `Name()` | `customfieldef.created/.updated/.deleted` | 5 |
| `CanCreate/CanRead/CanUpdate/CanDelete` | wrap `IsManager` | 6 |
| `Create/ReadAll/ReadOne/Update/Delete` | DB CRUD on a `*xorm.Session` | 7 |
| Handlers + `RegisterAuthenticatedRoutes` | thin Echo → CanX → model → commit → dispatch | 8 |
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

- [ ] **Step 8: Commit the spike result notes (not the throwaway code)**

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

### Task 5: Events

Three event types dispatched on commit, as seams for S3. S2 registers no listeners.

**Files:**
- Modify: `main.go` (add after validation)

**Interfaces:**
- Consumes: `code.vikunja.io/api/pkg/events` (registered; `DispatchOnCommit` available).
- Produces: `FieldDefinitionCreatedEvent`, `FieldDefinitionUpdatedEvent{Old,New}`, `FieldDefinitionDeletedEvent{DefinitionID}` — used by Task 7's CRUD methods.

- [ ] **Step 1: Add the event types**

```go
// ── Events (seams for S3). Dispatched on commit via events.DispatchOnCommit so
// listeners run after the transaction commits, matching TaskDeletedEvent. S2
// registers no listeners.

type FieldDefinitionCreatedEvent struct {
	Definition *CustomFieldDefinition
}

func (FieldDefinitionCreatedEvent) Name() string { return "customfieldef.created" }

type FieldDefinitionUpdatedEvent struct {
	Old *CustomFieldDefinition
	New *CustomFieldDefinition
}

func (FieldDefinitionUpdatedEvent) Name() string { return "customfieldef.updated" }

type FieldDefinitionDeletedEvent struct {
	DefinitionID int64
}

func (FieldDefinitionDeletedEvent) Name() string { return "customfieldef.deleted" }
```

- [ ] **Step 2: Add the events import**

Add `"code.vikunja.io/api/pkg/events"` to the import block at `main.go:3-17`.

- [ ] **Step 3: Restart and verify load**

```bash
./scripts/run-test-env.sh
docker compose -f compose.test.yml logs 2>&1 | grep -iE "error|Loaded plugin custom-fields" | tail -5
```
Expected: plugin loads, no errors.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat(s2): add field-definition lifecycle events"
```

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

The DB layer. Each method takes `*xorm.Session` + `*user.User`, does the xorm work, and (for Create/Update/Delete) dispatches the Task 5 events on commit. The handler (Task 8) owns the session lifecycle (open, call method, commit, close). **Adopt the spike-2 shape** (methods below; free-functions if the spike so chose). These methods do NOT call `CanX` — the handler does.

**Files:**
- Modify: `main.go` (add after permissions)

**Interfaces:**
- Consumes: structs (Task 2), errors (Task 3), `validateDefinition`/`validateAssignment` (Task 4), events (Task 5).
- Produces (methods shown as spike-2 default):
  `func (d *CustomFieldDefinition) Create(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error)`
  `func (d *CustomFieldDefinition) ReadOne(s *xorm.Session) (*CustomFieldDefinition, []CustomFieldOption, []int64, error)`
  `func ReadAll(s *xorm.Session, projectID int64) ([]CustomFieldDefinition, error)`
  `func (d *CustomFieldDefinition) Update(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error)`
  `func (d *CustomFieldDefinition) Delete(s *xorm.Session) error`
  (If spike-2 chose free-functions, make these package-level with `d` as a leading param.)

- [ ] **Step 1: Add Create**

```go
// ── Model CRUD. Handlers (Task 8) own the session: open, call CanX, call these,
// commit, close. These methods never open/commit the session themselves. Events
// are dispatched on commit by the handler after a successful method return.

// resolveProjectIDs enforces mutual exclusivity: empty ⟹ global sentinel [0];
// non-empty ⟹ those IDs. The caller must not pass both a sentinel and specifics.
func resolveProjectIDs(projectIDs []int64) []int64 {
	if len(projectIDs) == 0 {
		return []int64{0} // sentinel: all projects
	}
	return projectIDs
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
	if isSelectLike(d.Type) {
		for i := range options {
			options[i].CustomFieldDefinitionID = d.ID
		}
		if len(options) > 0 {
			if _, err := s.Table("custom_field_options").Insert(&options); err != nil {
				return nil, fmt.Errorf("custom-fields: insert options: %w", err)
			}
		}
	}
	assign := resolveProjectIDs(projectIDs)
	rows := make([]CustomFieldProject, len(assign))
	for i, pid := range assign {
		rows[i] = CustomFieldProject{CustomFieldDefinitionID: d.ID, ProjectID: pid}
	}
	if _, err := s.Table("custom_field_projects").Insert(&rows); err != nil {
		return nil, fmt.Errorf("custom-fields: insert assignment: %w", err)
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
// full-replace). It does NOT touch custom_field_values (S3's table). Dispatches
// FieldDefinitionUpdatedEvent(old+new) — the handler fires it on commit.
func (d *CustomFieldDefinition) Update(s *xorm.Session, u *user.User, options []CustomFieldOption, projectIDs []int64) (*CustomFieldDefinition, error) {
	var old CustomFieldDefinition
	has, err := s.Table("custom_field_definitions").ID(d.ID).Get(&old)
	if err != nil {
		return nil, fmt.Errorf("custom-fields: get old definition: %w", err)
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
	if _, err := s.Table("custom_field_definitions").ID(d.ID).Update(d); err != nil {
		return nil, fmt.Errorf("custom-fields: update definition: %w", err)
	}
	// Replace options wholesale.
	if _, err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", d.ID).Delete(&CustomFieldOption{}); err != nil {
		return nil, fmt.Errorf("custom-fields: clear options: %w", err)
	}
	if isSelectLike(d.Type) && len(options) > 0 {
		for i := range options {
			options[i].CustomFieldDefinitionID = d.ID
		}
		if _, err := s.Table("custom_field_options").Insert(&options); err != nil {
			return nil, fmt.Errorf("custom-fields: re-insert options: %w", err)
		}
	}
	// Replace assignment wholesale.
	if _, err := s.Table("custom_field_projects").Where("custom_field_definition_id = ?", d.ID).Delete(&CustomFieldProject{}); err != nil {
		return nil, fmt.Errorf("custom-fields: clear assignment: %w", err)
	}
	assign := resolveProjectIDs(projectIDs)
	rows := make([]CustomFieldProject, len(assign))
	for i, pid := range assign {
		rows[i] = CustomFieldProject{CustomFieldDefinitionID: d.ID, ProjectID: pid}
	}
	if _, err := s.Table("custom_field_projects").Insert(&rows); err != nil {
		return nil, fmt.Errorf("custom-fields: re-insert assignment: %w", err)
	}
	d.FieldConfig = old.FieldConfig // not used downstream; kept for event fidelity
	return d, nil
}
```

> Note: store the old definition for the event before the Update. The handler will
> capture `old` via a separate read or by having Update return it; simplest: have
> the handler read `old` before calling Update and pass it when dispatching. See
> Task 8 Step 4. (If you prefer Update to return `old`, add a `*CustomFieldDefinition`
> out-param; either is acceptable — pick one and keep it consistent.)

- [ ] **Step 4: Add Delete**

```go
// Delete hard-cascades the definition's OWN rows: definition + options +
// assignment. It does NOT touch custom_field_values (S3's table). The handler
// dispatches FieldDefinitionDeletedEvent{DefinitionID} on commit.
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
- Consumes: model methods (Task 7), events (Task 5), errors (Task 3).
- Produces: five registered routes; `managerHandler` removed.

- [ ] **Step 1: Add request/response helper types**

```go
// ── Handlers. Thin: parse → CanX (403) → model → commit → dispatch → JSON.
// web.HTTPError is unavailable, so handlers use echo.NewHTTPError(code, msg).

type definitionRequest struct {
	Name         string           `json:"name"`
	Type         string           `json:"type"`
	Description  string           `json:"description"`
	FieldConfig  FieldConfig      `json:"field_config"`
	DisplayOrder int              `json:"display_order"`
	Options      []CustomFieldOption `json:"options"`
	ProjectIDs   []int64          `json:"project_ids"`
}

type definitionResponse struct {
	CustomFieldDefinition
	Options     []CustomFieldOption `json:"options"`
	ProjectIDs  []int64             `json:"project_ids"`
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
	events.DispatchOnCommit(s, &FieldDefinitionCreatedEvent{Definition: created})
	return c.JSON(http.StatusCreated, definitionResponse{
		CustomFieldDefinition: *created,
		Options:               req.Options,
		ProjectIDs:           req.ProjectIDs,
	})
}
```

- [ ] **Step 4: Add readOneHandler, listHandler**

```go
func readOneHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.PathParam("id"), 10, 64)
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
	return c.JSON(http.StatusOK, definitionResponse{CustomFieldDefinition: *def, Options: opts, ProjectIDs: pids})
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
	return c.JSON(http.StatusOK, defs)
}
```

- [ ] **Step 5: Add updateHandler, deleteHandler**

```go
func updateHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.PathParam("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	var req definitionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
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
	// Capture old state for the event before mutation.
	var old CustomFieldDefinition
	_ = s.Table("custom_field_definitions").ID(id).Get(&old) // best-effort; event is informational
	updated, err := d.Update(s, u, req.Options, req.ProjectIDs)
	if err != nil {
		return toHTTPError(err)
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	events.DispatchOnCommit(s, &FieldDefinitionUpdatedEvent{Old: &old, New: updated})
	return c.JSON(http.StatusOK, definitionResponse{
		CustomFieldDefinition: *updated,
		Options:               req.Options,
		ProjectIDs:            req.ProjectIDs,
	})
}

func deleteHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.PathParam("id"), 10, 64)
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
	events.DispatchOnCommit(s, &FieldDefinitionDeletedEvent{DefinitionID: id})
	return c.NoContent(http.StatusNoContent)
}
```

- [ ] **Step 6: Add `strconv` import and replace route registration**

Add `"strconv"` to the import block. Replace `main.go:133-138` (the `RegisterAuthenticatedRoutes` method) with:

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

Insert after the users seed block (after the `if echo "$SEED_RESPONSE" | grep -qi "error"` block, before Step 4 login):

```bash
# ── Step 3.5: Seed a test project (owned by testuser) ──────────
echo "==> Seeding test project..."
PROJECT_RESPONSE=$(curl -s -X PUT "$BASE_URL/api/v2/test/projects?truncate=true" \
  -H "Authorization: $TESTING_TOKEN" \
  -H "Content-Type: application/json" \
  -d "[{
    \"id\": 1,
    \"title\": \"Test Project\",
    \"owner_id\": 1,
    \"created\": \"2026-08-08T00:00:00Z\",
    \"updated\": \"2026-08-08T00:00:00Z\"
  }]")
if echo "$PROJECT_RESPONSE" | grep -qi "error"; then
  echo "WARNING: project seed response: $PROJECT_RESPONSE"
fi
echo "   Test project created (id 1)"
```

> The exact column set for `projects` may differ across Vikunja versions; if the
> seed returns a schema error, run `sqlite3 db/vikunja.db "PRAGMA table_info(projects);"`
> and adjust the JSON keys to match the actual columns (owner_id vs owner_id,
> title vs title). The point is to have one project with id 1 that
> `validateAssignment` finds via `models.Project`.

- [ ] **Step 2: Restart and confirm the project seeded**

```bash
./scripts/run-test-env.sh
sqlite3 db/vikunja.db "SELECT id, title, owner_id FROM projects;"
```
Expected: a row with id 1.

- [ ] **Step 3: AC#1 — create a definition (whitelisted user)**

```bash
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name":"Cost Center","type":"select","field_config":{"required":true,"default":"draft"},"display_order":3,"options":[{"value":"draft","label":"Draft","display_order":0},{"value":"final","label":"Final","display_order":1}],"project_ids":[1]}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions | jq .
```
Expected: HTTP 201; response has `id`, `name`, `field_config`, `options` (2), `project_ids` `[1]`.

- [ ] **Step 4: AC#2 — list (unfiltered and by project)**

```bash
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions | jq .
curl -s -H "Authorization: Bearer $JWT" "http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions?project_id=1" | jq .
```
Expected: bare JSON array; the second contains the Cost Center field. Then create a global field and confirm it appears under `?project_id=1`:

```bash
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name":"Global Note","type":"text","project_ids":[]}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions | jq .
curl -s -H "Authorization: Bearer $JWT" "http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions?project_id=1" | jq '.[] | .name'
```
Expected: both `Cost Center` and `Global Note` appear under project 1.

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
curl -s -o /dev/null -w "GET1:%{http_code}\n" -H "Authorization: Bearer $JWT_OTHERUSER" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/1
curl -s -o /dev/null -w "PUT:%{http_code}\n" -X PUT -H "Authorization: Bearer $JWT_OTHERUSER" -H "Content-Type: application/json" -d '{"name":"x","type":"text"}' http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/1
curl -s -o /dev/null -w "DELETE:%{http_code}\n" -X DELETE -H "Authorization: Bearer $JWT_OTHERUSER" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/1
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
- Events (3, on-commit) → Task 5 + dispatched in Task 8 handlers. ✓
- S3 seams (guard insertion points) → CanUpdate/CanDelete in Task 6 (where S3 adds a block-if-values check). ✓
- Spikes (2, fallback ladder) → Task 1. ✓
- Remove managerHandler → Task 8 Step 7. ✓
- `field_config` spike-dependent → Task 2 Step 1 note + Task 1. ✓

**Placeholder scan:** No TBD/TODO in plan body. One explicit note in Task 7 Step 3 ("pick one and keep it consistent") — that is a real engineering choice with both options shown, not a gap. Task 9 Step 1 has a genuine contingency note about `projects` column names (version-dependent) with a concrete fallback command — not a placeholder.

**Type consistency:** `CustomFieldDefinition`/`CustomFieldOption`/`CustomFieldProject`/`FieldConfig` defined in Task 2, used identically in Tasks 4/7/8. Error types defined Task 3, switched-on in Task 8 `toHTTPError`. Event types defined Task 5, dispatched in Task 8. `definitionRequest`/`definitionResponse` defined Task 8, used in handlers. `IsManager` from `main.go:80` (existing) consumed in Task 6. Method signatures match between Task 7 (defines) and Task 8 (calls). ✓

No gaps found. Plan is complete.

## Required Skills

- **`git-flow`** — the whole plan executes on `feature/s2-field-definition-api-design` (already started); finish via `git flow feature finish` after review/approval, never commit to `develop`.

## Recommended Skills

- **`golang-testing`** — for reasoning about test design (note: plugin source is not standalone-testable; tests are integration via the test instance per the spec).
- **`golang-error-handling`** — for the Task 3 error-type pattern and the Task 8 `toHTTPError` translation.
- **`golang-database`** — for the xorm session/query patterns in Tasks 7–8 (remember: no `builder`, string-chaining only).
- **`migration`** (from the vikunja repo skills) — for the Task 2 migration; the spec's `Sync2`-vs-`partialSync` divergence from upstream is recorded and watched.