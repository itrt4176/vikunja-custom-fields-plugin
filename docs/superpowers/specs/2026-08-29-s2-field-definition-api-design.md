# S2 — Field Definition API: Design Spec

**Date:** 2026-08-29
**Story:** [S2 — Field Definition API](../../stories/S2-field-definition-api.md)
**Status:** Approved (pending spec review)

## Summary

S2 delivers the CRUD API for custom field definitions on top of the tables and
whitelist from S1/S8. A whitelisted user can create, list, read, update, and
delete field definitions — each with a name, type, scalar constraints, optional
select options, and assignment to one or more projects (or all projects).
Non-whitelisted users get 403 on every operation.

The API is authored as if it were a native `/api/v2/custom-fields` resource, just
served from the plugin route prefix. Every decision — data model, verb
conventions, migration, error shapes — mirrors upstream Vikunja so that moving
the feature into core is mechanical, not a redesign. The two stories that fork
off S2 (S3 task field values, S9 management UI) consume this same API contract.

## Context — decisions were grounded in upstream evidence, not assumption

This project restarted once because an inherited assumption was wrong (the entire
admin feature, not just its UI, is Pro-licensed). The defense is the same as S1's:
every load-bearing fact here was checked against the live `vikunja/` fork source
and the contributor docs in `vikunja-docs/docs/`, not inherited. Where a choice
had an upstream analog, the analog was found and followed; where it didn't, the
absence of precedent is recorded explicitly.

The five load-bearing decisions (constraint storage, project assignment, name
uniqueness, delete semantics, update-mutation policy) and the two architectural
constraints (no `pkg/web/handler`, no `xorm.io/builder` in the yaegi symbol
table) are each documented below with the evidence that settled them.

## Independently verified facts (live `vikunja/` fork source + docs)

### Yaegi symbol table (`pkg/yaegi_symbols/`) — what the plugin can and can't use

Verified against the backported test image (PR #3549 registered `xorm` and
`xormigrate`, overturning the S1 spec's "MigrationPlugin blocked" note — S1's
committed code already uses `NewMigrationPlugin` + `*xormigrate.Migration`).

**Available:** `code.vikunja.io/api/pkg/db` (`NewSession`), `pkg/log`, `pkg/models`,
`pkg/user`, `pkg/events`, `pkg/plugins`, `viper`, `xorm.io/xorm`,
`src.techknowlogick.com/xormigrate`, echo v5, watermill. (`config` is also now
available via `vikunja_config.go`, though S8 uses `viper` directly.)

**NOT available:**
- `pkg/web/handler` (the generic CRUD plumbing). Confirmed: no `web` symbol file
  exists. **Consequence:** the plugin cannot reuse `DoCreate`/`DoDelete`/etc.
  Handlers are hand-rolled Echo, as S1's `healthHandler`/`managerHandler` already
  are.
- `xorm.io/builder`. Confirmed: no `builder` registration anywhere in
  `yaegi_symbols/`. **Consequence:** queries use xorm string-chaining
  (`s.Where(...).And(...).Or(...)`, `.In()`), never `builder.Eq/Or/In`. We mirror
  the *logic* of upstream idioms like `NotificationProjectFilter`, not their
  builder *syntax*.

### Project-assignment precedent (settled the assignment model)

Vikunja never uses an `is_global` boolean. The convention for "all projects vs
specific" is **sentinel `ProjectID` values**:

- `pkg/notifications/database.go:43` — `ProjectID int64` where `0` = account-scoped
  (always visible), `>0` = one project (gated by read access), `-1` = unresolved.
  The visibility query (`notifications_permissions.go:60-93`) is
  `builder.Or(Eq{"project_id": 0}, accessibleProjectIDsSubquery(...))`.
- `ParentProjectID == 0` = top-level project (`project.go:52,627`).
- `TaskCollection.ProjectID = 0` = "tasks from all projects" (`saved_filters.go:65`).
- M2M join tables (`team_projects`, `users_projects`, `task_assignees`,
  `label_tasks`) share a shape: autoincr PK, two `bigint INDEX not null` FKs,
  `created`/`updated`, optional `Permission`. Dedup is enforced in code, not by a
  composite unique index. Hard FK constraints are not used anywhere (S1's spec
  already calls its FK "FK-ish; no hard constraint (portable)").

**Decision:** a `custom_field_projects` M2M table in the `team_projects` shape,
where a sentinel `project_id = 0` row means "all projects" and specific rows mean
those projects. No `is_global` column. (Rejected: an `is_global` bool, which has
no precedent anywhere in `pkg/models`/`pkg/web` and would be stripped at
upstreaming time.)

### Name-uniqueness precedent (settled the uniqueness rule)

Vikunja's house style is **no uniqueness on human-readable names — duplicates
allowed.** Every reusable named entity — labels (`label.go:34`), teams
(`teams.go:38`), saved filters (`saved_filters.go:39`), projects (`project.go:43`)
— has a `varchar(250) not null` title/name with no DB unique index and no
code-level duplicate check. The only uniqueness Vikunja ever enforces on a string
field is `users.username` (DB unique index) and project `Identifier` (code-level
`Where("identifier = ?").And("id != ?").Exist()` check, `project.go:1062-1074`,
global scope, returns `ErrProjectIdentifierIsNotUnique` HTTP 400).

**Decision:** no name uniqueness at the API level — match labels/teams. The name
is a display label; field *values* join by `definition_id` (the PK), never by
name, so the name plays no role in data integrity. Reject only empty names
(`ErrTeamNameCannotBeEmpty` precedent). No soft UI warning either (the confusing-
duplicates worry is a management-time concern; the sole management-UI user is
already aware). If a future story turns out to need the name as a key, *that
story* is the one that's wrong and is fixed there, not by pre-building uniqueness.

### Delete-semantics precedent (settled the delete + update policy)

Vikunja has two distinct non-destructive mechanisms, used for different things:

- **Soft-delete (xorm `deleted` tag + `deleted_at` + 30-day cron purge):** used on
  exactly one entity, Task (`tasks.go:142`, `task_delete_cron.go`). Soft-delete is
  reserved for *content*; it pays a query-filtering tax (`taskNotDeletedCond`
  sprinkled across raw/join queries because the xorm auto-filter leaks there).
- **Archive (`IsArchived` bool + `CheckIsArchived` precondition error):** used on
  Project (`project.go:59`, HTTP 412). A reversible read-only flag, not a delete.

No reusable named entity (label, team, user) is soft-deleted or archived. They
hard-delete, with two flavors:
- Team/Project: **hard-cascade** — `Delete()` manually removes referenced rows in
  one session (`teams.go:358` deletes `team_members`+`team_projects`;
  `project.go:1451` recursively cascades tasks+shares+children).
- Label: **hard-delete leaving orphans** — `Label.Delete` is
  `s.ID(l.ID).Delete(&Label{})`, leaving dangling `label_tasks` rows
  (`label.go:133`). The survey flagged this as arguably a latent bug.

**Block-delete-if-referenced has zero precedent** — no `CanDelete` checks
reference counts anywhere.

**Decision:** S2's delete **hard-cascades the definition's own rows** (definition
+ options + assignment M2M) in one transaction — the team-style manual cascade of
owned rows. It does **not** touch the `custom_field_values` table — that's S3's
table and S3's lifecycle decision. S2 dispatches `FieldDefinitionDeletedEvent` on
commit as S3's seam to wire value cleanup later. The production data-loss concern
(soft-delete was originally considered for this) is protected *by not deleting
values in S2*, not by soft-deleting the definition — soft-deleting the definition
alone is incoherent unless values are also soft-deleted, which is S3's call. The
interim orphan window is benign: nothing reads values until S3 (and S5) exist.

**Update-mutation precedent:** S2's update **allows all changes** (type,
constraints, options) without checking the values table — consistent with "S2
doesn't own/manage values," and with how Vikunja lets you freely edit a saved
filter's criteria even though tasks that used to match no longer do. Existing
values that no longer match become S3's read-path concern. S2 is designed for
future tightening: it dispatches `FieldDefinitionUpdatedEvent` (old+new state) and
leaves `CanUpdate`/`CanDelete` as clean guard-insertion points so S3 can later add
a "block-if-values-exist" check without a rewrite.

### JSON-column precedent (settled `field_config` storage)

Upstream has a native xorm `json` column type and uses it for exactly our use
case: `pkg/models/api_tokens.go:51` stores `APIPermissions APIPermissions` as
`xorm:"json not null permissions"` — a real Go struct type, with xorm handling
marshal/unmarshal. xorm's `Json` schema type maps to **TEXT under SQLite**
(`dialects/sqlite3.go:226`) and native JSON/JSONB under MySQL/Postgres
(`mysql.go:301`), so the dialect difference is abstracted cleanly.

**Decision:** `field_config` is a typed Go struct (`FieldConfig`), tagged
`xorm:"json null"`, not a `text` field with manual `encoding/json`. Matches
`api_tokens.APIPermissions`. Whether xorm's json reflection works on a
yaegi-interpreted struct is the one residual risk, gated by a spike (below).

### Verb precedent (settled PUT vs PATCH)

Every CRUD resource in v2 — labels, teams, projects, saved_filters — uses
`Method: http.MethodPut` for the authored update endpoint; PATCH does not appear on
any of them. PATCH is **auto-generated**: `EnableAutoPatch` (`huma.go:164`,
called once in `registry.go:44`) synthesizes a PATCH for every resource that has
GET+PUT. The synthesized PATCH is JSON-merge-style (omitted=untouched,
present=replaced), implemented by `huma/v2/autopatch` re-dispatching an internal
GET+PUT and with explicit null-handling machinery (`MergePatchNullabilityExtension`,
`replaceNulls`/`restoreNulls`) because JSON merge-patch cannot represent "set to
null" vs "leave alone" without it.

Plugins cannot use huma or call `EnableAutoPatch` (plugins hand-roll Echo under
`/api/v1/plugins/`; no huma symbol is available). Echo v5's binding is plain
`json.Unmarshal` into a struct (`binder_generic.go`) — it leaves no "was this key
present?" information, so a hand-rolled PATCH built on `c.Bind()` cannot
correctly implement merge-patch semantics (it would treat "omit options" the same
as "set options to empty"). Doing it correctly would mean re-implementing a slice
of `autopatch.go` (present-key detection via `map[string]json.RawMessage`,
nested-`field_config` merge, null-vs-omitted-vs-empty-array) — the edge cases are
the core semantic, not a small tail.

**Decision:** **PUT only** for the update endpoint — the authored, authoritative
full-replace verb, matching every native v2 resource. The client sends the full
definition (it has it from GET); omitted fields take zero values; empty/omitted
`project_ids` ⟹ global. PATCH is **deferred to upstreaming**, where
`EnableAutoPatch` generates it correctly and for free against huma. No hand-rolled
partial engine to get wrong or maintain; no edge-case tail inherited by S9/S5.
(The original PATCH proposal was rejected: PATCH is not the authored upstream verb
for any CRUD resource, and hand-rolling it fights the harness's binding model.)

### Migration-granularity precedent (settled new-migration vs modify-existing)

Upstream uses both patterns, decided by one question: *has the creation migration
shipped in a release with data to protect?*

- **Released tables → append-only (pattern A):** ~50 migrations add a column to a
  pre-existing table via a *new* dated file + `partialSync`. `tasks` got
  `deleted_at` as a new migration (`20260707094311.go`) because `tasks` shipped
  years ago. The migration skill warns plain `Sync` on a released table "destroyed
  the `users` and `tasks` indexes of every upgraded install in v2.4.0."
- **Unreleased, in-development features → edit the creation migration in place
  (pattern B):** direct git-verified precedent is `project_views`
  (`20240313230538.go`). Created Mar 13 2024; commit `ee228106f` edited it to add
  backfill; commit `a9020e976` edited it *again* to add `BucketConfigurationMode`
  + `BucketConfiguration` to the creation struct. Same migration ID throughout.
  `git tag --contains a9020e976` confirms it first shipped in v0.24.0 (Jul 2024),
  ~3.5 months later — so no DB could have run it before the edits.

S1's migration `20260829160000-create-custom-field-tables` has shipped nowhere and
the harness wipes fresh each startup. We are unambiguously in pattern-B territory,
and (per the user's framing) the whole custom-fields feature is **one upstream
PR**, which gets **one migration** in its final shape.

**Decision:** **modify the existing S1 migration in place** — no new migration, no
new file, no new ID. Its `Migrate` now `Sync2`s the expanded `CustomFieldDefinition`
plus the two new structs. The append-only switch fires when the **plugin runs in
production** (the first moment there's production data a migration could destroy),
not at upstream time. How that production-era mapping translates upstream is TBD
and out of scope.

## Architecture & file layout

**Single `main.go`, `package main`**, plugin repo root (mounted live at
`/app/vikunja/plugins/custom-fields`). Multi-file is structurally possible (yaegi
loads the plugin *directory*, `manager.go:125`), but single-file is the safest path
and is explicitly recommended by `vikunja-docs/docs/plugin-development.md`:
> Yaegi evaluates `.go` files individually, so multi-file plugins may hit
> order-dependency issues. Keeping your plugin in a single `main.go` is safest.

S2 keeps the single file but introduces internal layering (Approach B) to mirror
Vikunja's model/permissions split, so the model + permissions methods literally
*are* a native Vikunja model's methods and upstreaming is a mechanical move
(register the same methods with the real `pkg/web/handler`, swap the route
prefix):

```
main.go
├── Plugin lifecycle:  Name/Version/Init/Shutdown + NewPlugin/NewAuthenticatedRouterPlugin/NewMigrationPlugin
├── Models (CRUDable-shaped): CustomFieldDefinition.{Create,ReadAll,Update,Delete}(*xorm.Session, *user.User) error
│                              + CustomFieldOption, CustomFieldProject structs
├── Permissions:       CustomFieldDefinition.{CanCreate,CanRead,CanUpdate,CanDelete}(*xorm.Session, *user.User) (bool,error) → wrap IsManager
├── Validation:        type/constraint/option/assignment checks (pure funcs)
├── Events:            FieldDefinition{Created,Updated,Deleted}Event + DispatchOnCommit
├── Handlers:          thin Echo handlers → CanX → model method → commit → dispatch
└── Errors:            custom error structs + ErrCode consts + HTTPError via echo.NewHTTPError
```

**`*user.User` instead of `web.Auth`:** Vikunja's `CanX` signatures take
`web.Auth`, but `web` is not in the symbol table. Handlers obtain the user via
`user.GetCurrentUser(c)` (as S1's `managerHandler` does), and the `CanX` methods
take the concrete `*user.User` (which *is* available). Same effective behavior.
This is the one spot we deviate from the exact native signature — recorded as a
mechanical upstream-conversion point (swap `*user.User` → `web.Auth`).

## Data model

### `custom_field_definitions` (expand the existing S1 table)

| Column | xorm tag | Notes |
|---|---|---|
| ID | `bigint autoincr not null unique pk` | |
| Name | `varchar(255) not null` | display label; not unique; non-empty enforced |
| Type | `varchar(50) not null` | one of: text, textarea, integer, decimal, date, datetime, select, multiselect, checkbox, url |
| Description | `varchar(500) null` | optional, for the management UI |
| FieldConfig | `FieldConfig` | `xorm:"json null"` — typed struct, xorm-serialized (TEXT under sqlite, JSON under mysql/postgres). **Spike-dependent tag.** |
| DisplayOrder | `int not null default 0` | rendering/management ordering |
| Created | `created not null` | |
| Updated | `updated not null` | |

```go
// FieldConfig holds a field definition's scalar constraints. xorm serializes it
// to a JSON column (TEXT under sqlite, JSON/JSONB under mysql/postgres) — the
// same xorm:"json" mechanism api_tokens.APIPermissions uses.
type FieldConfig struct {
    Required  bool      `json:"required,omitempty"`
    Default   string    `json:"default,omitempty"`
    Min       *float64  `json:"min,omitempty"` // integer/decimal range; pointer so 0 ≠ unset
    Max       *float64  `json:"max,omitempty"`
    IsAPIOnly bool      `json:"is_api_only,omitempty"` // PRD stretch; S3 owns behavior
}
```

### `custom_field_options` (new) — for select/multiselect

| Column | xorm tag | Notes |
|---|---|---|
| ID | `bigint autoincr not null unique pk` | |
| CustomFieldDefinitionID | `bigint not null index` | FK-ish, no hard constraint (portable) |
| Value | `varchar(255) not null` | the stored option value; unique within a field |
| Label | `varchar(255) null` | optional display label; if empty, Value is shown |
| DisplayOrder | `int not null default 0` | option ordering within the field |
| Created | `created not null` | |
| Updated | `updated not null` | |

First-class rows: queryable, reorderable, individually CRUD-able by S9, clean for
S3 to validate select values against. Empty for non-select types.

### `custom_field_projects` (new) — assignment M2M

| Column | xorm tag | Notes |
|---|---|---|
| ID | `bigint autoincr not null unique pk` | |
| CustomFieldDefinitionID | `bigint not null index` | FK-ish |
| ProjectID | `bigint not null index` | `0` = all projects (sentinel); `>0` = specific project |
| Created | `created not null` | |

The `team_projects` shape. A sentinel `project_id = 0` row means "all projects";
specific rows mean those projects. The handler enforces mutual exclusivity: a
field has *either* the 0-row *or* ≥1 specific rows, never both (the one validation
rule that keeps the two states clean — the same code-level dedup `team_projects`
uses). "Applies to project X?" mirrors the `NotificationProjectFilter` logic (not
its builder syntax):

```go
has, _ := s.Table("custom_field_projects").
    Where("custom_field_definition_id = ?", id).
    And("project_id = ? OR project_id = 0", projectID).
    Exist(&CustomFieldProject{})
```

### Deliberately absent
- No `is_global` column — "all projects" is the sentinel row (notifications idiom).
- No `deleted_at` / soft-delete — deferred to S3's values-lifecycle decision.
- No name unique index — duplicates allowed; only non-empty enforced.

## Endpoints, validation, authorization

### Routes (under the existing `/api/v1/plugins/custom-fields/...` group)

| Method | Path | Handler → model |
|---|---|---|
| POST | `/custom-fields/definitions` | Create |
| GET | `/custom-fields/definitions` | ReadAll (optional `?project_id=` filter) |
| GET | `/custom-fields/definitions/{id}` | ReadOne |
| PUT | `/custom-fields/definitions/{id}` | Update (full-replace) |
| DELETE | `/custom-fields/definitions/{id}` | Delete |

Plugin routes live under `/api/v1/plugins/` (no v2 plugin mechanism exists, per
the PRD constraint), but the verbs are the modern v2 shape (POST=create,
PUT=update, DELETE=delete) because the API is authored "as if native
`/api/v2/custom-fields`" and will move to v2 on upstreaming. PATCH is deferred.

### Request/response shapes (snake_case JSON, Vikunja envelope)

Create/Update body:
```json
{
  "name": "Cost Center",
  "type": "select",
  "description": "Optional",
  "field_config": { "required": true, "default": "draft" },
  "display_order": 3,
  "options": [
    { "value": "draft", "label": "Draft", "display_order": 0 },
    { "value": "final", "label": "Final", "display_order": 1 }
  ],
  "project_ids": [5, 7]
}
```
- `options` accepted only for select/multiselect (rejected otherwise).
- `project_ids` is the assignment: `[]` or omitted ⟹ **global** (sentinel row
  `project_id=0`); non-empty ⟹ specific projects. This encoding makes
  mutual-exclusivity trivial: empty = global, non-empty = specific. The handler
  never receives both a "global" flag and specific IDs.
- Under PUT (full-replace), omitted fields take zero values and empty/omitted
  `project_ids` ⟹ global — same encoding as POST.

Response (single, on create/read/update): the full definition with its resolved
relations (`field_config`, `options`, `project_ids` — `[]` for global). ReadAll
returns a bare array (Vikunja's list convention — confirm against a native list
endpoint in the spike; flagged as the one shape to verify).

### Authorization (the permissions layer)

Every handler's first action is the `CanX` gate, which wraps `IsManager`:
```go
func (d *CustomFieldDefinition) CanCreate(s *xorm.Session, u *user.User) (bool, error) {
    return IsManager(u.Username), nil
}
// CanRead/CanUpdate/CanDelete identical — all management ops are whitelist-gated.
```
- **AC#5:** non-whitelisted users get 403 on any operation. `CanX` returns false →
  handler returns `echo.NewHTTPError(http.StatusForbidden, ...)`.
- S8's temporary `managerHandler` verification route is removed once S2 exercises
  `IsManager` on the real endpoints.

### Validation (AC#6) — pure functions, run in Create/Update

1. **Type valid** — one of the 10 supported types.
2. **Name non-empty** — `ErrCustomFieldNameEmpty` (HTTP 400), the
   `ErrTeamNameCannotBeEmpty` precedent. **No uniqueness check.**
3. **Options ↔ type** — `options` non-empty only for select/multiselect; a `text`
   field with options is rejected (`ErrCustomFieldOptionsForNonSelect`). For
   select/multiselect, option `value` non-empty and unique within the field.
4. **Constraints ↔ type** — `min`/`max` only for integer/decimal (rejected
   otherwise, `ErrCustomFieldConstraintForType`); `min <= max` when both set.
5. **Project assignment** — every `project_id` in the list must reference an
   existing project (existence check against the `projects` table via the
   `models` symbol); the global sentinel needs no such check.

### Error handling

Custom errors follow the `custom-errors.md` recipe (struct + `IsErrXxx` +
`Error()` + unique `ErrCodeXxx` const + `HTTPError()`). Since `web.HTTPError`
requires the unavailable `web`, handlers return `echo.NewHTTPError(code, message)`
instead — same HTTP outcome, recorded as an upstream-conversion point (swap to
`HTTPError()`/`ErrCode` when native). Error codes reserve a plugin-local
**9000s range** (no plugin error-code convention exists upstream; recorded so it
doesn't collide).

### The S3 seams (built now, per "maximally accommodating future S3 decisions")

- **Events dispatched on commit:** `FieldDefinitionCreatedEvent`,
  `FieldDefinitionUpdatedEvent` (old+new state), `FieldDefinitionDeletedEvent` —
  via `events.DispatchOnCommit(s, ...)` (events is in the symbol table). S3 wires
  listeners; S2 registers none.
- **Guard insertion points:** `CanUpdate` and `CanDelete` are where a future
  "block if values exist" check slots in — the model method is the natural place,
  so S3 adds it without touching handlers. A one-line comment marks the point.

## Migration

**Modify the existing S1 migration in place** (pattern B — unreleased feature,
project_views precedent). No new migration, no new ID.

```go
&xormigrate.Migration{
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
}
```

- Explicit-table form `tx.Table(name).Sync2(&T{})` — required for yaegi structs
  (PR #3501; `TableName()` is invisible to xorm through yaegi's anonymous reflect
  structs).
- `Sync2` auto-adds the new `description`/`field_config`/`display_order` columns
  to the existing definitions table; it is idempotent on S1's existing columns and
  additive on the new ones. (Upstream uses `partialSync` to avoid dropping
  unrelated indices on released tables — a risk the migration skill flags. We use
  `Sync2` because it's the verified-working mechanism from S1 and our tables have
  no indices beyond what the struct tags declare; recorded as a watched point — if
  `Sync2` ever drops an index we need, switch to a column-add fallback.)
- **`field_config` tag is spike-dependent** — `xorm:"json null"` if the spike
  confirms json reflection under yaegi, else `text null` with manual marshaling in
  the model methods (rung 4 of the spike ladder). The spec records both variants.
- **No data-migration logic** — there is no DB to evolve (the premise of pattern
  B); the migration is purely structural `Sync2`.
- **Append-only switch:** once the plugin runs in production (first production
  data to protect), every further schema change becomes a new dated append
  migration (pattern A, the deleted_at precedent) and this creation migration
  becomes immutable. How that translates upstream is TBD and out of scope.

## Events

```go
type FieldDefinitionCreatedEvent struct{ Definition *CustomFieldDefinition }
func (FieldDefinitionCreatedEvent) Name() string { return "customfieldef.created" }

type FieldDefinitionUpdatedEvent struct {
    Old *CustomFieldDefinition
    New *CustomFieldDefinition
}
func (FieldDefinitionUpdatedEvent) Name() string { return "customfieldef.updated" }

type FieldDefinitionDeletedEvent struct{ DefinitionID int64 }
func (FieldDefinitionDeletedEvent) Name() string { return "customfieldef.deleted" }
```

- Naming follows `entity.action`, most-general-left (`events-and-listeners.md`).
- `Updated` carries **old + new** so S3 (or a future revalidation/blocking
  listener) can diff what changed without a separate read.
- `Deleted` carries the ID (not the struct — the row's gone) so S3's listener can
  cascade values by `definition_id`.
- Dispatched on commit via `events.DispatchOnCommit` — listeners run after the
  transaction commits, matching `TaskDeletedEvent`/`ProjectDeletedEvent`. S2
  registers no listeners; it only dispatches.

## Testing strategy

The plugin source can't be `go test`ed standalone (imports resolve only inside
the vikunja module). Testing is **integration via the existing test instance**
(`compose.test.yml`, SQLite). No unit tests of plugin source.

### Task 0 — two spikes (the gate, same de-risk pattern as S1)

1. **`xorm:"json"` reflection spike** — throwaway plugin: a struct with a
   `FieldConfig \`xorm:"json null"\`` field, `tx.Table(name).Sync2(&T{})`, then
   `Insert` + `Get` round-trip against sqlite. Walks the fallback ladder:
   1. Direct `xorm:"json"` on the typed struct.
   2. Register the type with yaegi's symbol table — **confirmed dead end** (can't
      extend host symbols from a plugin); recorded so it isn't retried.
   3. Custom `convert.Conversion` on a `[]byte`/`string`-backed field, if
      reachable in the symbol table.
   4. Manual `[]byte`/`string` column + `encoding/json` in the model methods —
      the `text` fallback, but keep `FieldConfig` as the typed struct (preserves
      API + model shape; only storage mechanism differs — the S1 raw-DDL analog).
   5. **Any other viable path found while investigating the above** — the spike
      agent is explicitly instructed to pursue any workable solution it uncovers
      that isn't on this ladder and report it with evidence. The ladder is a
      starting set, not a permission gate: a path not listed is not forbidden.
   Outcome selects the `field_config` storage mechanism and migration tag.
   Discard after deciding.

2. **Session-arg-method spike** — throwaway plugin: a method
   `func (d *T) Create(s *xorm.Engine, u *user.User) error` on a yaegi struct,
   called through a route. Confirms methods-with-session-args work on yaegi
   structs (S1 verified `TableName()`; session-args are the new reflect path).
   Fallback ladder: method-on-struct → free functions
   `func Create(s *xorm.Engine, d *T, u *user.User)` → any-other-viable-path →
   handler-inline logic. Outcome selects whether the model layer is
   methods-on-struct or free functions. Discard after deciding.

Both spikes run against the test instance and are discarded once the mechanism is
chosen — de-risking the two new reflect paths before the real S2 code.

### Acceptance-criteria verification (via the test instance + curl + JWTs S8 produces)

| AC | How verified |
|---|---|
| 1. Create (name, type, constraints, project assignment) | `POST .../definitions` with a whitelisted user's JWT → 201, response has id + relations. |
| 2. List, optionally filtered by project | `GET .../definitions` → array; `?project_id=5` → filtered. Global field appears for every project_id filter. |
| 3. Update properties | `PUT .../definitions/{id}` → 200, GET returns updated state. |
| 4. Delete | `DELETE .../definitions/{id}` → 204; definition + its options + assignment rows gone (verify via `sqlite3 db/vikunja.db`); values table untouched. |
| 5. Non-whitelisted → 403 | Same calls with the second (non-whitelisted) user's JWT (S8 seeds two users) → 403 on all five verbs. |
| 6. Validation | Empty name → 400; bad type → 400; options on a non-select → 400; min>max → 400; duplicate option value → 400; nonexistent project_id → 400. |
| 7. Response shapes (snake_case, consistent envelope) | Inspect JSON; snake_case keys; list is a bare array (confirm against a native list endpoint in the spike). |

**Test-instance seeding additions:** S2 may need projects seeded for the
assignment-validation AC (via the testing-token seed endpoint). Add a seed step to
`run-test-env.sh` if the existing seed data lacks projects — a harness-modification
point per CLAUDE.local.md.

## Git workflow

Per `CLAUDE.local.md`: git-flow, branch `feature/s2-field-definition-api-design`
off `develop`, Conventional Commits. A worktree decision is raised before any
development work. The implementation plan (next step, via the writing-plans skill)
details the branch/commit structure and includes Required/Recommended skills lists
per `CLAUDE.local.md`.

## Out of scope

- Field *values* on tasks — S3 (S2 dispatches events as S3's seam; S2 never
  touches the values table).
- The management UI — S9.
- Any frontend task-detail changes — S5.
- Soft-delete / archive of definitions — deferred to S3's values-lifecycle
  decision.
- "API-only" field *behavior* — the flag lives on the definition
  (`field_config.is_api_only`); S3 owns the behavior.
- PATCH — auto-generated at upstreaming via `EnableAutoPatch`.
- Bulk operations on definitions.
- Automated unit tests of the plugin source (infeasible standalone; covered by
  integration via the test instance).
- How production-era append-migrations translate upstream (TBD when the feature
  upstreams).