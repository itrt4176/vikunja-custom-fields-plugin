# S3 — Task Field Values API: Design Spec

**Date:** 2026-08-30
**Story:** [S3 — Task Field Values API](../../stories/S3-task-field-values-api.md)
**Status:** Approved (pending spec review)

## Summary

S3 delivers the CRUD API for custom field values on tasks — the read/write surface
that makes field definitions (S2) useful. A user with task-level access can read and
write values through the plugin API; values are validated against their field
definitions, cascade-deleted when a task is removed, and never appear for projects
that don't have those fields assigned. When a field definition is deleted, its values
are cascade-deleted; when a definition is changed, values that become incompatible are
absent from the read response but remain stored — recoverable on revert for scalar
type/constraint changes and option reorder/relabel, but destructive (not recovered by
re-adding the option) for option-value-change and option-removal (see Definition
lifecycle).

The API is authored as if it were a native `/api/v2/tasks/{task}/custom-fields`
resource, just served from the plugin route prefix. Every decision — data model, verb
conventions, read/write shapes, authorization, the definition lifecycle — mirrors
upstream Vikunja so that moving the feature into core minimizes the upstreaming cost.
The values API routes port by two ordered mechanical swaps (see Route-base-prefix
portability); the remaining upstreaming work (Echo→huma, `*user.User`→`web.Auth`,
response maps→structs, the `xorm:"-"` computed field on `models.Task`) is enumerated
below as known conversion points, not as a trivial prefix swap. The one structural
constraint (the native task response can't be augmented under yaegi) is recorded in
AC#1's amendment; the values API is a dedicated resource that the frontend (S5) merges
into the task detail view — the assignees/labels pattern, not the inlined-task-body
pattern.

## Context — decisions were grounded in upstream evidence, not assumption

S2's spec established the discipline: every load-bearing fact was checked against the
live `vikunja/` fork source and the contributor docs, not inherited. S3 continues it.
The investigation was run against `itrt4176/vikunja:2.5-plugin-fix-backport` (PR #3549
backport; `xorm` + `xormigrate` in the yaegi symbol table). All citations are
`file:line` under `vikunja/`.

## Independently verified facts (live `vikunja/` fork source + docs + example plugin)

### Yaegi symbol table — what the plugin can and can't use

**Available** (confirmed in `pkg/yaegi_symbols/`): `pkg/db` (`NewSession`), `pkg/log`,
`pkg/models`, `pkg/user`, `pkg/events` (`RegisterListener` at `vikunja_events.go:31`,
the `Listener` interface + `_Listener` wrapper at `:40,:45`), `pkg/plugins`, `viper`,
`xorm.io/xorm`, `src.techknowlogick.com/xormigrate`, echo v5, watermill
(`message.Message` at `watermill.go:33`).

**NOT available:** `pkg/web` (no symbol file exists — confirmed by grep across
`pkg/yaegi_symbols/`). `web.Auth`, `web.CRUDable`, `web.Permissions`, `web.HTTPError`
are all unreachable as named types. Consequence: the plugin cannot call
`DoCreate`/`DoDelete` or implement `web.CRUDable`; handlers are hand-rolled Echo (as
S2's already are), and `CanX` methods take `*user.User` instead of `web.Auth`.

### Can a plugin register an event listener? — YES (the blessed example proves it)

The official example plugin (`examples/plugins/example/main.go:40`) registers a
listener from `Init()`:
```go
events.RegisterListener((&models.TaskCreatedEvent{}).Name(), &TestListener{})
```
The docs (`vikunja-docs/docs/plugin-development.md:278-282`) show the same pattern and
state "Your plugin can listen to any event dispatched by Vikunja" (`:244`). The
`Listener` interface (`pkg/events/listeners.go:22-25`) is:
```go
type Listener interface {
    Handle(msg *message.Message) error
    Name() string
}
```
The callback receives a watermill `*message.Message`; the event payload is JSON-marshaled
in `msg.Payload`, so the listener `json.Unmarshal`s it. `RegisterListener`
(`pkg/events/listeners.go:34`) is exported and registered in the symbol table.

**Startup ordering is favorable:** `plugins.Initialize()` (which calls each plugin's
`Init()`) runs at `pkg/initialize/init.go:127`, *before* `models.RegisterListeners()` +
`events.InitEvents()` at `:155-159`. So a `RegisterListener` call in `Init()` lands in
the `listeners` map before the router is built, and the listener is wired into the
topic = `event.Name()`.

### Can a plugin augment the native task GET response? — NO (settled AC#1)

Both v1 and v2 serialize the `models.Task` struct verbatim with no read-path hook:
- **v1:** `pkg/web/handler/read_one.go:33` — `DoReadOne` then
  `ctx.JSON(http.StatusOK, currentStruct)`.
- **v2:** `pkg/routes/api/v2/tasks.go:107-131` — a fixed `taskReadOneBody{Task,
  MaxPermission}`; `tasksRead` builds it from `handler.DoReadOne` and returns it
  directly. No post-read hook, no middleware, no event-on-read.

The `plugins.Plugin` interface (`pkg/plugins/interfaces.go:25-48`) has exactly:
`Name`, `Version`, `Init`, `Shutdown` (Plugin); `Migrations` (MigrationPlugin);
`RegisterAuthenticatedRoutes` / `RegisterUnauthenticatedRoutes` (the router plugins).
There is **no** `Hooks`, `AfterRead`, `PostRead`, or `AugmentResponse` method. A
plugin can only mount routes under `/api/v1/plugins/`.

**Decision:** values are served from a dedicated plugin endpoint the frontend (S5)
calls alongside the native task fetch and merges into the task detail view. AC#1 is
amended (same precedent as S2's AC#6 amendment — host evidence contradicted an AC; the
AC was revised to match the host).

### The sub-resource storage precedent (settled the values API shape)

Every many-to-one / one-to-many sub-resource on `models.Task` is `xorm:"-"` (not a DB
column — computed at read time), `readOnly:"true"`, and "Read-only here; use the
`<thing>` endpoints to change `<it>`":
- `Assignees []*user.User` (`tasks.go:102`) — "use the task-assignee endpoints"
- `Labels []*Label` (`tasks.go:104`) — "use the label-task endpoints"
- `Attachments []*TaskAttachment` (`tasks.go:122`) — "use the attachment endpoints"
- `Reactions ReactionMap` (`tasks.go:170`) — "Only present when requested via the
  reactions expand option"
- `RelatedTasks RelatedRelationMap` (`tasks.go:119`) — "use the task-relation endpoints"

There is **no** sub-resource in Vikunja that is written inline via the task PUT body.
**Decision:** custom field values follow the assignees/labels pattern — a dedicated
resource with its own CRUD endpoints, `xorm:"-"` readOnly on the task. The plugin's
dedicated endpoints *are* the lasting native surface (they move to
`/api/v2/tasks/{task}/custom-fields/...` by a prefix swap); they are not a throwaway
bridge.

### The read-path enrichment precedent (settled the response shape)

`Reactions ReactionMap` (`tasks.go:170`, `reaction.go:64`) is a
`map[string][]*user.User`, `xorm:"-"`, `readOnly:"true`, populated via `?expand=reactions`.
`task_comments.go:39` is the same shape on comments. The `taskReadOneBody`
(`tasks.go:107-110`) is `models.Task` + `MaxPermission ... readOnly:"true"`, and
`tasksUpdate` takes *that same struct* as its body (`tasks.go:193`) — the convention is
one struct for read and write, with `readOnly:"true"` fields enriching the GET and
tolerated on write. A v2 response body can be a string-keyed `map` outright
(`token_meta.go:39`).

**Decision:** the values read response is a `map[string]…` keyed by `definition_id`
(as a string — JSON-forced), each entry `{value, field}` where `field` carries the full
definition metadata (type, options, field_config, project_ids). The `field` is the
`readOnly` enrichment (like `MaxPermission`); `value` is the scalar. This is the exact
object that will one day be the task's `custom_fields` field — upstreaming the read is
"delete the endpoint, inline the same map into the task GET as an `xorm:"-"` computed
field, like `Reactions`."

### The route-base-prefix portability (settled the route paths)

The plugin route prefix is `/api/v1/plugins/custom-fields` (the group, not the base).
All routes — definitions and values — are portable by two ordered mechanical swaps:

1. `/api/v1/plugins/custom-fields/tasks` → `/api/v2/tasks` (the values path —
   consumes the `custom-fields` group segment + the `tasks` resource segment).
   `/api/v1/plugins/custom-fields/tasks/{task}/custom-fields/{field_id}` →
   `/api/v2/tasks/{task}/custom-fields/{field_id}`.
2. `/api/v1/plugins` → `/api/v2` (the definitions path — the remaining `custom-fields`
   segment becomes the native `custom-fields` resource). Applied after step 1, so it
   only touches the definitions path. `/api/v1/plugins/custom-fields/definitions` →
   `/api/v2/custom-fields/definitions`.

Step 1 runs first and consumes the `custom-fields/tasks` segment on the values path;
step 2 then handles the definitions path (which step 1 did not touch). Both are
mechanical string substitutions, but there are two of them, applied in order, not one.
The values path's `custom-fields` resource segment (the second one, after `{task}`)
survives step 1 unchanged — it is after the `tasks` segment that step 1 replaces.

### Task access checks (settled the authorization approach)

Host `Task.CanRead/CanUpdate/CanDelete/CanWrite` all take `web.Auth`
(`tasks_permissions.go:25-60`). `web.Auth` is `type Auth interface { GetID() int64 }`
(`web.go:60`); `*user.User` has `GetID() int64` (`user.go:185`), so `*user.User`
structurally satisfies `web.Auth`. But `pkg/web` is not in the symbol table, so
calling these host methods with `*user.User` is the one unverified reflect risk —
yaegi *might* convert `*user.User` → `web.Auth` at the call site (the type comes from
the function signature, not a symbol lookup), but it has not been proven.

`models.GetTaskByIDSimple(s, taskID)` (`tasks.go:380`) takes `*xorm.Session` + `int64`
(no `web.Auth`) and resolves `task.ProjectID` — fully reachable, registered at
`vikunja_models.go:174`.

**Decision:** `CanX(s *xorm.Session, u *user.User)` (S2's pattern — `*user.User` not
`web.Auth`, `main.go:222`, an upstream-conversion point). **Approach (a)** preferred:
call host `Task.CanRead`/`CanUpdate` with `*user.User` (structurally satisfies
`web.Auth`). **(b)** fallback: plugin-local xorm queries against `project_users`/
`team_members` (the proven `main.go:207` pattern). Spike 2 (see Spikes) picks a-vs-b.
Either way the `CanX` signature stays `*user.User`; only the method body changes. User
resolved via `user.GetCurrentUserFromDB(s, c)` (example-aligned,
`examples/plugins/example/main.go:67`).

### Archive/move behavior (settled AC#7)

There is **no task-level archive** — `IsArchived` is a Project flag (`project.go:58`).
"Archive a task" maps to marking it `done` (a normal update; `task_id` stable). Move =
normal update changing `project_id`; the task row keeps its `ID` and fires
`TaskUpdatedEvent` (`tasks.go:1608`). Values keyed by `task_id` need no re-parenting.

The move validation (`canDoTask`, `tasks_permissions.go:65-87`) checks only the
**doer's** access to source and destination projects — it never re-checks sub-resources
(assignees, labels, values). The `task_assignees` row survives the move untouched
(`task_id` stable, no relocation); the assignee's *reachability* is gated at read time
by `Project.CanRead`/`CanWrite` on the *current* project, not at move time.

**Decision (AC#7):** values survive by construction (`task_id` stable). Move-to-a-
project-where-a-field-isn't-assigned: **persist-and-don't-surface** — the value stays
stored, the read filter (AC#4) drops it from the response. Recoverable if the task
moves back. No `task.updated` listener, no move-time pruning. This matches the assignee
precedent (per-user access, survives move, gated at read, no move-time pruning).

### Definition-delete precedent (settled the cascade)

Vikunja has two delete flavors for parent entities:
- **Hard-cascade owned rows** — `Team.Delete` (`teams.go:358`) cascades `team_members` +
  `team_projects`; `Project.Delete` (`project.go`) cascades tasks, views, buckets,
  shares, project_users, team_projects, child projects. The dominant precedent.
- **Hard-delete leaving orphans** — `Label.Delete` (`label.go:133`) is
  `s.ID(l.ID).Delete(&Label{})`, leaving dangling `label_tasks`. The S2 survey flagged
  this as "arguably a latent bug."

**Block-on-delete-if-referenced has zero precedent** — no `CanDelete` checks reference
counts anywhere (confirmed by grep; the one `CanDelete` hit is a bucket permission
check, not a referential guard).

**Decision:** definition delete **cascade-deletes values** synchronously, in the
definition's delete transaction (the definition's delete *is* a plugin-owned
transaction, unlike the task-delete which is host-owned). Orphaned values are
unrecoverable dead storage (`definition_id` is a never-reused autoincr PK; the
values→definitions join drops orphans), so orphaning (the label exception) is strictly
worse. S3 modifies S2's `CustomFieldDefinition.Delete` (`main.go:435`) to also delete
from `custom_field_values` + `custom_field_value_options` by `definition_id`, same
transaction. `CanDelete` guard point stays unused (cascade, not block).

### Definition-change precedent (settled the tolerate-on-change policy)

The closest analog is `ProjectView.Update` retying `view_kind` (List↔Kanban):
`project_view.go:640` puts `view_kind` in the updateable `cols` — no guard that the
view has buckets/tasks. The stale state is **healed/tolerated**:
`syncManualKanbanBuckets`/`healBucketIDs`/`addTasksToView` (`project_view.go:640-684`)
re-home orphaned tasks. `resolveBucketID` (`project_view.go:388`) states the philosophy:
*"Rejecting an id the frontend merely echoed back … would lock the view until a reload"*
— tolerate stale references, don't lock the client.

The **saved filter** (`saved_filters.go:198`) is *not* an analog for the change case
(despite being cited in S2): a saved filter is a stored *query string*, not a schema
that stored values conform to. It has no stored children that can go stale. S2 raised
it for the narrow point "definition edits are not blocked"; it does not cover
"incompatible stored values are left in place on edit" because no saved-filter value
exists.

The backend never blocks an edit because referenced data exists (confirmed: the only
block-on-edit is the project-identifier uniqueness check at `project.go:1062-1074`,
which blocks a rename *to a duplicate* via `And("id != ?", project.ID)` — a *collision*
guard, not a *referential* guard; no model restricts a column once data exists).

**Decision:** definition change → **free edit; no block; incompatible values are a
read-path concern (drop-on-read).** A value that fails coercion against the field's
*current* type+constraints is absent from the read response — same mechanism as AC#4's
project-assignment filter (both produce "absent," both non-destructive). Recoverability
is *scoped* (see Recoverability, scoped below): revert recovers the value for scalar
type/constraint changes and select reorder/relabel, but not for select option-value-
change/removal (destructive). The management UI (S9) may warn on destructive edits (the
delete-confirm pattern extended), but that's S9's call.

### The `valid:"required"` semantics (settled the write-path required policy)

`valid:"required"` is govalidator's `Required` — a **value-shape check** (the value,
when present, must be non-empty), typically paired with `xorm:"not null"` +
`minLength:"1"` (`api_tokens.go:44`, `project.go:43`, `teams.go:38`). `reaction.go:51`
pairs it with `maxLength:"20"` only (no `minLength`) — `required` alone is the value-shape
check; the `minLength`/`maxLength` are separate constraints. The point: there is no
cross-field "this field must be present in every payload" rule — no Vikunja model 400s
because a required field was *omitted* from a PUT. `required` is about the value's shape
when set, not "every write must include this field."

**Decision:** the values-write path validates *sent* values' type + constraints
(including `required` as non-empty for a sent value) but does **not** 400 for *omitting*
a required field. "Required" means "a written value must be non-empty," not "the field
can't be cleared." Omission under `PUT` (full-replace) clears; omission under `PATCH`
(merge, the upstream target) leaves unchanged. Neither 400s for omission.

### The autopatch behavior (the upstream target — why (A) stays stale-safe after porting)

**`huma` is not available to the yaegi plugin** (confirmed: no `huma` symbol file in
`pkg/yaegi_symbols/`). The plugin registers routes via Echo
(`RegisterAuthenticatedRoutes(g *echo.Group)`); it has no huma operations, no
`EnableAutoPatch`, and no synthesized `PATCH`. This section describes the *upstream
target* — what happens when the values resource ports to native `/api/v2/...` and is
registered with huma — to confirm that (A) stays stale-safe *after* upstreaming, not
during the plugin phase. The plugin's stale-safety (this phase) stands on the "no (A)
write carries the complete set" basis alone (see Write-path policy); autopatch is not
load-bearing for the plugin.

`EnableAutoPatch` (`pkg/routes/api/v2/huma.go:165`) calls `autopatch.AutoPatch(api)`
which synthesizes a `PATCH` for every resource with GET+PUT. The synthesized `PATCH` does
a **GET → apply merge-patch → PUT** round-trip (`autopatch.go:152` in the huma library):
the server `GET`s the current resource, merges the patch body onto it, and `PUT`s the
merged result. The merged body is a *complete* resource, validated as a whole by
`validateInputBody` (`validation.go:34`, which runs `govalidator.ValidateStruct` on the
full bound body).

Under **(A)** (dedicated values resource with per-field `GET`+`PUT`): autopatch
synthesizes a **per-field** `PATCH /custom-fields/{field_id}` — the merge is on *one
value* (field 3), so field 12 is never in the merged body. No cross-value stale
problem. The bulk collection `POST` (upsert, no `PUT`) gets no autopatch `PATCH`. So
**every (A) write carries either one value or only-the-sent-values** — never the
complete set including a stale field. The stale problem arises only when a write carries
the complete set (a bulk `PUT` replace-all, or values-inlined-in-task-body), which is
exactly the (B) write (A) is designed to avoid.

**The bulk `PUT` (replace-all) is the (B) write** — it sends the complete values set
into a body validated as a whole, tripping on any stale field. No Vikunja *collection*
has a replace-all `PUT`: assignees are `POST` + `POST /bulk` + `DELETE`; labels are the
same; comments are `POST` + `PUT /{id}` + `DELETE`. Collections upsert + per-item-delete;
"replace the whole set" doesn't exist for a collection. (A) chose collection → upsert →
no bulk `PUT`. **(A) excludes the bulk `PUT` by construction, not by ad-hoc ruling-out.**

### No default synthesis (settled the default-on-read question)

Vikunja never synthesizes defaults on read. Every `default` in the codebase is a
DB-level `DEFAULT` on INSERT (`xorm:"... default 0"`, `default false`, `default null`),
applied at insert time, never re-derived on read. Zero hits for
"synthesize/fill/apply default on read" across `pkg/models/`.

**Decision:** the values read path returns what's *stored*, not what *would be* stored.
An unset field has no value row → absent from the map (or `value: null` with the field
present — see Read-path policy). The `field_config.default` is metadata in the `field`
object for consumers that want it; the server does not synthesize it. Consistent with
every Vikunja default.

## Architecture & file layout

**Single `main.go`, `package main`** (S2's established layout, confirmed safest by
`vikunja-docs/docs/plugin-development.md:379`). S3 adds a new layer to S2's internal
layering:

```
main.go
├── Plugin lifecycle:  Name/Version/Init/Shutdown + NewPlugin/NewAuthenticatedRouterPlugin/NewMigrationPlugin
│                      Init() now registers the TaskDeletedEvent listener
├── Models (S2):       CustomFieldDefinition.{Create,ReadAll,Update,Delete} + CustomFieldOption, CustomFieldProject
├── Models (S3):       CustomFieldValue.{CRUD} + CustomFieldValueOption (child table for select-type values)
│                      CustomFieldDefinition.Delete extended to cascade values
├── Permissions:       CustomFieldValue.{CanRead,CanCreate,CanUpdate,CanDelete}(*xorm.Session, *user.User) → task-level access
├── Validation:        validateValue(def, options, raw) — pure, per-type coercion + constraint check
├── Events (S3):       taskDeletedListener — Handle(*message.Message) unmarshal + cascade-delete
├── Handlers:          thin Echo handlers → CanX → model → commit → JSON map (response maps, not structs)
└── Errors:            custom error structs + ErrCode consts + toHTTPError (prefix-matched, S2's yaegi workaround)
```

**`*user.User` instead of `web.Auth`** (S2's pattern, `main.go:222`). Handlers obtain the
user via `user.GetCurrentUserFromDB(s, c)` (example-aligned). `CanX` methods take the
concrete `*user.User`. Upstream-conversion point: swap to `web.Auth`.

## Data model

### `custom_field_values` (expand the existing S1 table)

| Column | xorm tag | Notes |
|---|---|---|
| ID | `bigint autoincr not null unique pk` | |
| CustomFieldDefinitionID | `bigint not null unique(field_task)` | FK-ish; part of the UNIQUE(field, task) composite |
| TaskID | `bigint not null unique(field_task) index` | FK-ish; the task this value belongs to; `task_id` is stable across archive/move |
| Value | `text` | the scalar value as a string (see Per-type encoding); empty for select-type values (which live in the child table) |
| Created | `created not null` | |
| Updated | `updated not null` | |

The `UNIQUE(CustomFieldDefinitionID, TaskID)` composite (`unique(field_task)`) is the
race-safe backstop for the POST-create → 409-if-already-set semantics and the query
index for the per-task `GET`. The `task_id` index speeds the cascade-delete listener
(`WHERE task_id = ?`). S3 adds these to the existing struct (`main.go:51`) and the
existing creation migration (pattern B — still unreleased).

### `custom_field_value_options` (new) — child table for select-type values

The `label_tasks` shape (`label_task.go:33-48`): a real table with its own PK, FKs to
the value and the option, `created`. One row per selected option.

| Column | xorm tag | Notes |
|---|---|---|
| ID | `bigint autoincr not null unique pk` | |
| CustomFieldValueID | `bigint not null index` | FK-ish; the value this option belongs to |
| CustomFieldOptionID | `bigint not null index` | FK-ish; the option row this value selects |
| Created | `created not null` | |

Single-select = one child row (max-1 cardinality enforced in validation). Multi-select =
N child rows. The `custom_field_values.Value` column is empty for select-type values;
the selected options live here. The read path joins
`custom_field_value_options` → `custom_field_options` to resolve the option values.

**Why a child table, not a JSON-array-in-string:** S2 made `custom_field_options`
first-class (CRUD-able, reorderable, own PK, `main.go:62-71`). A select value is an
*association to an option entity* — and entity-associations in Vikunja are always child
tables (`label_tasks`, `task_assignees`). JSON-in-column (`xorm:"json"`) is used only
for *configuration blobs* owned by one entity (`api_tokens.APIPermissions`,
`project_view.BucketConfiguration`) — not for entity associations. The migration-safety
argument (U, settled in design): U→S (all-child → scalar-single) is a pointer-deref
(`SELECT value FROM options WHERE id = option_id`); S→U (scalar-single → all-child) is a
string-search that depends on value-uniqueness. Dereferencing a stable FK is cleaner
than searching by string. And U's orphans are already invisible (the read join drops
them); S's orphaned `Value` string would still be served.

### Per-type value encoding

The `Value` column stores scalars as strings, validated and coerced per type:

| Type | Storage | Validation |
|---|---|---|
| text / textarea | raw string | non-empty if required |
| integer | `strconv.FormatInt` | `strconv.ParseInt` succeeds |
| decimal | `strconv.FormatFloat` | `strconv.ParseFloat` succeeds; min/max if set |
| date | ISO `2006-01-02` | `time.Parse(dateFormat)` |
| datetime | RFC3339 | `time.Parse(time.RFC3339)` |
| select | (child table) | the option value string must be in the field's current options' values |
| multiselect | (child table) | each option value string must be in the field's current options' values |

A select value is written and read as the **option value string** (e.g. `"draft"`),
not the option's numeric ID — the consumer-facing identity of an option is its value,
matching how a select field is rendered (the user picks a label, the value is stored).
The write path resolves the value string to the option's ID internally for storage in
the child table (`custom_field_value_options`); the read path joins
`custom_field_value_options` → `custom_field_options` to resolve the stored option IDs
back to value strings. This keeps the write/read symmetric on the value string and
hides the storage ID from the API consumer.
| checkbox | `"true"`/`"false"` | bool coercion |
| url | URL string | `url.Parse` + scheme present; non-empty if required |

### Wire representation: type-native JSON, not stringified

The read response coerces per type — integer → `int64`, decimal → `float64`, checkbox →
`bool`, multiselect → `[]string` of option values, else → `string`. The write accepts
the native JSON type and coerces to the storage string. This is load-bearing for the
"indistinguishable from native fields" principle: a number field reading back as
`"0"` would be visibly different from a native integer field returning `0`. It also
fits the response-map pattern (interpreted structs serialize as `{}` through `c.JSON`,
so values must be concrete `interface{}`s, which the coercion produces — same root
cause class as S2's `fieldConfigMap` emitting `*fc.Min` as concrete `float64`,
`main.go:481`).

### `is_api_only` derivation

No extra column. Derived from `def.FieldConfig.IsAPIOnly`, surfaced in the read response
via the `field` sub-object (`field.field_config.is_api_only`, already in the `{value,
field}` shape). The API reads/writes the value normally; the frontend (S5) uses the
flag to render display-only.

## Endpoints

### Routes (under `/api/v1/plugins/custom-fields/tasks/{task}/custom-fields`)

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/tasks/{task}/custom-fields` | list the task's values, filtered to project-assigned fields (AC#4); bare JSON map keyed by `definition_id` |
| `POST` | `/tasks/{task}/custom-fields` | bulk write — array body `[{custom_field_definition_id, value}, …]` (AC#2); upsert (create-or-overwrite per item) |
| `GET` | `/tasks/{task}/custom-fields/{field_id}` | read one value |
| `POST` | `/tasks/{task}/custom-fields/{field_id}` | create one → 201; 409 if already set |
| `PUT` | `/tasks/{task}/custom-fields/{field_id}` | full-replace one value's body → 200; 404 if absent |
| `DELETE` | `/tasks/{task}/custom-fields/{field_id}` | clear one value |

**No bulk `PUT`** (replace-all). The bulk `POST` is upsert (listed fields written,
unlisted untouched); per-field `POST`/`PUT`/`DELETE` handle individual values. The bulk
`PUT` is the (B) write — it sends the complete set into a body validated as a whole,
tripping on any stale field. (A) excludes it by construction (collections upsert + per-
item-delete; no Vikunja collection has a replace-all `PUT`). The one-request full-replace
use case (rare) is diff-then-`DELETE`-then-`POST` — accepted as the price of (A).

**Semantic asymmetry between the two `POST`s:** the bulk `POST /custom-fields` is
**upsert** (create-or-overwrite per item — listed fields are written whether or not they
existed), while the per-field `POST /custom-fields/{field_id}` is **create-only** (409 if
the field is already set). This is a deliberate difference: the bulk `POST` is the
"save these values" operation (the client knows the current state and wants to set N
fields); the per-field `POST` is the strict "create this one value, fail if it exists"
operation (for an API consumer who wants create-or-conflict semantics on a single field).
`PUT /custom-fields/{field_id}` is the **replace-only** operation (404 if absent — matches
the comments precedent, `TaskComment.Update` returns `ErrTaskCommentDoesNotExist` when
`updated == 0`). A client that wants "create-or-update a single field" uses the bulk
`POST` with one item (which upserts), not the per-field `PUT` (which 404s on a missing
value). The per-field `POST` (create) and `PUT` (replace) are the strict-CRUD pair;
the bulk `POST` is the permissive save-all. This matches the assignees/labels precedent:
assignees are `POST` (create) + `POST /bulk` (add-many) + `DELETE`, where the bulk add is
not a batch-replace.

`{field_id}` = the `custom_field_definition_id` (the sub-resource is identified by the
associated entity's id, like assignees use `{user}` and labels use `{label}`).

### Read response shape

```json
{
  "3": {
    "value": 0,
    "field": {
      "id": 3, "name": "Priority Score", "type": "integer",
      "description": "...", "field_config": { "required": true, "default": "0", "is_api_only": false },
      "display_order": 1, "project_ids": [5]
    }
  },
  "12": {
    "value": "draft",
    "field": {
      "id": 12, "name": "Status", "type": "select",
      "description": "...", "field_config": { "required": true, "default": "draft", "is_api_only": false },
      "display_order": 3,
      "options": [ { "id": 41, "value": "draft", "label": "Draft", "display_order": 0 }, ... ],
      "project_ids": [5]
    }
  }
}
```

Each entry is `{value, field}`: `value` is the type-native coerced scalar (or `null` if
absent/invalid — see Read-path policy); `field` is the full definition metadata (same
shape as S2's `definitionToMap`). The map is keyed by `definition_id` as a string.

### Write request shapes

**Bulk `POST`** (upsert): `[{ "custom_field_definition_id": 3, "value": 2 }, { "custom_field_definition_id": 12, "value": "draft" }]` (single-select value is the option value string; multiselect value is an array of option value strings, e.g. `"value": ["draft", "published"]`; scalar types use the native JSON type).`

**Per-field `POST`/`PUT`**: `{ "value": 2 }` — just the value body (the `field_id` is in
the URL). `POST` creates (409 if already set); `PUT` replaces (404 if absent).

## Read-path policy

Two independent filters govern the read response, with two different answers:

1. **Does the *field* appear in the response?** Governed by AC#4 — is the field assigned
   to the task's project (global sentinel OR a `custom_field_projects` row for that
   project)? The field's value-validity doesn't enter into it. A field is present iff
   it's assigned. This reuses S2's project-assignment query logic:
   ```go
   applies := s.Table("custom_field_projects").
       Where("custom_field_definition_id = ?", value.CustomFieldDefinitionID).
       And("project_id = ? OR project_id = 0", task.ProjectID).
       Exist(&CustomFieldProject{})
   ```

2. **What goes in the `value` slot?** Governed by validity — the stored value coerced to
   the current type, or `null` if the value is absent or invalid (fails coercion against
   the field's current type+constraints). Not a stale raw (which would surface a `42`
   for a select field). Not a validity flag (unprecedented — `resolveBucketID`'s
   philosophy is silent tolerance, not annotation). Not a synthesized default (Vikunja
   never synthesizes on read).

**The map key is the field-presence axis (AC#4); the `value` field is the value-validity
axis (`null` if absent or invalid); the `field` metadata is always present when the key
is present.** `null` does double duty (unset and invalid both mean "no valid value to
show") — this is the *intentional* tolerate-stale-silently behavior: the broken bit
doesn't get a distinct representation, it looks unset. For a *scalar* field (e.g. an
integer whose `min` constraint was tightened past the stored value), reverting the
constraint re-validates the unchanged stored string and the value reappears — recoverable,
no special handling. For a *select* field whose `draft` option was removed, the child row
is orphaned against a deleted option ID; re-adding `draft` gets a fresh ID, so the orphaned
row does **not** reappear (see the `setOptions` re-creation interaction — that's the
destructive edge of the change policy, surfaced by S9's warning).

A field with `value: null` and the field present means "this field belongs on this task
(AC#4) but has no valid value." A field absent from the map means "this field is not
assigned to this task's project (AC#4)." These are distinct states with distinct
representations — the key-presence axis (AC#4) is separate from the value axis.

## Authorization (AC#5)

```
CustomFieldValue.CanRead(s, u)    → task.CanRead(s, u)    or plugin-local equivalent
CustomFieldValue.CanCreate(s, u)  → task.CanUpdate(s, u)  or plugin-local equivalent
CustomFieldValue.CanUpdate(s, u)  → task.CanUpdate(s, u)  or plugin-local equivalent
CustomFieldValue.CanDelete(s, u)  → task.CanUpdate(s, u)  or plugin-local equivalent
```

Read → `CanRead`; write (create/update/delete) → `CanUpdate` (mirroring
`tasks_permissions.go` where `CanCreate`/`CanUpdate`/`CanDelete` all delegate to
`canDoTask` → `Project.CanWrite`).

**`CanRead` return-arity:** the host's `task.CanRead` (`tasks_permissions.go:42`) returns
`(canRead bool, maxPermission int, err error)` — **three** values — while the plugin's
`CustomFieldValue.CanRead` (following S2's `*xorm.Session, *user.User` pattern) returns
`(bool, error)`. The delegation discards the middle value:
```go
func (v *CustomFieldValue) CanRead(s *xorm.Session, u *user.User) (bool, error) {
    t := &models.Task{ID: v.TaskID}
    ok, _, err := t.CanRead(s, u) // approach (a): discard maxPermission
    return ok, err
}
```
`CanUpdate`/`CanCreate`/`CanDelete` are both `(bool, error)` in the host and match the
plugin's arity directly; only `CanRead` has the extra `maxPermission` return.

`task.ProjectID` resolved via `models.GetTaskByIDSimple(s, id)` (no `web.Auth`,
reachable). User via `user.GetCurrentUserFromDB(s, c)`. Spike 2 confirms whether approach
(a) (host `Task.CanRead`/`CanUpdate` with `*user.User`) works under yaegi; if not,
approach (b) (plugin-local xorm against `project_users`/`team_members`) is the fallback.
Either way, the `CanX` signature is `*user.User` (S2's pattern).

## Validation (AC#3)

A pure `validateValue(def *CustomFieldDefinition, options []CustomFieldOption, raw
interface{}) (storageString, error)` mirroring S2's `validateDefinition` — no DB access,
called before every write. For scalar types, returns the coerced storage string. For
select-type values, returns the validated option **value string(s)** — the write path
resolves the value string to the option ID for child-table storage. The validation
checks the value string against the field's current options' values.

- **integer/decimal:** `strconv.ParseInt`/`ParseFloat`; min/max from `field_config`.
- **date/datetime:** `time.Parse`; format per type.
- **select:** the option value string must be in the field's current options' values.
- **multiselect:** each option value string must be in the field's current options' values.
- **checkbox:** bool coercion.
- **url:** `url.Parse` + scheme present.
- **required:** a *sent* value for a required field must be non-empty (value-shape
  check, matching `valid:"required"` precedent). Omission is not a validation error
  (see Write-path policy).

## The definition lifecycle (AC#8, the deferred S2→S3 seam)

### Delete → cascade (synchronous)

S3 modifies S2's `CustomFieldDefinition.Delete` (`main.go:435`) to cascade-delete values
and their child rows, in the same transaction. The cascade is two-step (no hard FK
cascade — the `label_tasks` shape uses no DB-level constraints):
1. Delete `custom_field_value_options` where `custom_field_value_id IN (SELECT id FROM
   custom_field_values WHERE custom_field_definition_id = ?)` — the child rows reference
   the value, not the definition, so they're deleted via a subquery on the value IDs.
2. Delete `custom_field_values` where `custom_field_definition_id = ?`.

The definition's delete *is* a plugin-owned transaction (unlike the task-delete, which is
host-owned and can only be reached via the async listener). The team/project cascade
precedent (cascade owned rows in one session). Orphaned values are unrecoverable dead
storage, so orphaning (the label exception) is strictly worse. `CanDelete` guard point
stays unused — cascade, not block (block-on-delete-if-referenced has zero precedent).

### Change → free edit, drop-on-read, no block

The `ProjectView.view_kind` retype precedent (`project_view.go:640`): edits aren't
blocked when dependent data exists. The `resolveBucketID` philosophy (`:388`): tolerate
stale stored values rather than lock the client. A value that fails coercion against the
field's *current* type+constraints is absent from the read response — same mechanism as
the project-assignment filter (AC#4), both produce "absent," both non-destructive.

**Recoverability, scoped:** revert recovers the value for *scalar* type/constraint
changes (the stored string is unchanged; reverting the constraint re-validates it) and
for *select* option reorder/relabel (the `setOptions` fix preserves option IDs, so the
child row's reference stays valid). Revert does **not** recover the value for *select
option-value-change* or *option-removal*: the child row is orphaned against a deleted
option ID, and re-adding the option gets a fresh autoincr ID, so the dead child row still
references the old ID and stays dropped on read. These two edits are the *destructive*
edge of the otherwise-tolerant change policy — accepted because the alternative (blocking
the edit) has no precedent, and because S9's management-UI warning (AC#9) surfaces the
consequence to the manager before they commit.

### The `setOptions` re-creation interaction (S3 modifies S2's `setOptions`)

S2's `setOptions` (`main.go:299-313`) deletes ALL option rows for a definition and
re-inserts them with **new auto-increment IDs** (it zeroes `options[i].ID = 0` before
insert). The child table `custom_field_value_options` stores `CustomFieldOptionID` — so
when any definition Update touches options (even a display_order-only reorder), every
option ID changes, and every `custom_field_value_options` row referencing the old IDs
becomes orphaned → the read path drops all stored select values → all stored select
values silently vanish on a benign reorder. This is the most consequential
cross-story interaction, and it makes the child-table design **worse** than the
JSON-in-string alternative on option edits (with JSON-in-string, the stored value is
self-contained — reordering options doesn't break it).

**S3 fix:** modify `setOptions` to **preserve existing option IDs when the option value
is unchanged** — update-in-place for existing options (reorder/relabel), insert for new
options, delete for removed options — not delete-all-reinsert-all. This keeps the
child-table design sound: reordering/relabeling preserves option IDs → stored select
values survive; only editing a value's underlying option, or adding/removing an option,
orphans the values referencing the changed/removed option (correct — those values no
longer have a valid target). The change is a **code** change in `main.go` (S2's file,
which S3 may modify because the plugin hasn't shipped — the same unreleased-feature
freedom that lets S3 modify S2's migration, applied here to S2's `setOptions` logic);
it is **not** a migration change (no schema is affected — the child table's option-id
reference is unchanged; only the *stability* of the option ids changes). The existing
S2 behavior is the regression guard.

### The recoverability asymmetry (the load-bearing reason)

- *Delete* removes the parent → children are **unrecoverable** → cascade (team/project
  precedent, no dead storage).
- *Change* keeps the parent → children are **mostly recoverable** (revert the edit) →
  tolerate stale at read (ProjectView/resolveBucketID, don't lock, don't destroy). The
  exception: select option-value-change and option-removal orphan the child row against
  a deleted option ID, and re-adding the option gets a fresh ID, so revert does not
  recover those — they are destructive (see `setOptions` re-creation interaction). This
  is the deliberate price of the update-in-place `setOptions` design (which avoids the
  far worse outcome of orphaning *all* select values on any option edit).

Rejected alternatives: block-on-edit (unprecedented — no Vikunja model blocks an edit
because referenced data exists); write-time purge of all values on any definition change
(destroys recoverable data for the scalar/reorder/relabel cases that don't need it,
against the tolerate-stale philosophy); *unconditional* orphaning (the label pattern,
buggy, and — before the `setOptions` fix — would have orphaned *all* select values on
every option edit). The chosen design orphans *only* on option-value-change/removal
(where the orphan is correct — the value no longer has a valid target), not on every
edit. Each rejected alternative is worse on its axis.

## The write-path policy

Under (A) (dedicated values resource, assignees/labels pattern), no write carries the
complete set:
- **Bulk `POST`** (upsert): sends only the fields being changed; unlisted fields stay
  stored. A stale field 12 is omitted → never validated → stays stored, reads `null`.
- **Per-field `POST`/`PUT`/`DELETE`**: carries one value, validated alone. No cross-field.
- **No bulk `PUT`** (replace-all): the one operation that would carry the complete set,
  excluded by construction (it's the (B) write).

So the "edit field 3 while field 12 is stale" scenario is **stale-safe**: no (A) write
forces a decision on field 12. The stale value stays stored and reads `null` (its
recoverability depends on what made it stale — see Recoverability, scoped). The point
here is the *write* safety: editing field 3 never forces field 12 into validation, so a
stale 12 doesn't block the edit. Under (B) (inline-in-task-body), the task `PUT`/`PATCH`
would carry all values into full-body validation, tripping on stale 12 — a native
property of full-body validation, not a bridge artifact. (A) avoids it by design.

`required` is a value-shape constraint (a *sent* value must be non-empty), not a
cross-field present-rule (no precedent for "every write must include this field").
Omission under `PUT` clears (full-replace); omission under `PATCH` leaves unchanged
(merge). Neither 400s for omitting a required field. The server does not synthesize the
default on write (the default is metadata; the consumer may optionally use it).

## Cascade-delete on task deletion (AC#6)

### Registration (example-backed, `examples/plugins/example/main.go:40`)

In `Init()`:
```go
func (p *CustomFieldsPlugin) Init() error {
    whitelist = loadWhitelist()
    events.RegisterListener((&models.TaskDeletedEvent{}).Name(), &taskDeletedListener{})
    log.Infof("[custom-fields] plugin v0.1.0 initialized")
    return nil
}
```

`TaskDeletedEvent` (`pkg/models/events.go:60-69`):
```go
type TaskDeletedEvent struct {
    Task *Task      `json:"task"`
    Doer *user.User `json:"doer"`
}
func (t *TaskDeletedEvent) Name() string { return "task.deleted" }
```
Dispatched by the host at `pkg/models/tasks.go:2072` inside `Task.Delete` via
`events.DispatchOnCommit(s, &TaskDeletedEvent{...})`. The plugin only listens — it never
publishes (the yaegi-marshal failure that sank S2's plugin-defined events doesn't apply
to *listening*, which the example plugin proves works).

### Listener

```go
type taskDeletedListener struct{}

func (l *taskDeletedListener) Handle(msg *message.Message) error {
    var evt models.TaskDeletedEvent
    if err := json.Unmarshal(msg.Payload, &evt); err != nil {
        return err
    }
    s := db.NewSession()
    defer s.Close()
    // Delete child rows first (no hard FK cascade — the label_tasks shape uses
    // no DB-level constraints), then the value rows themselves.
    if _, err := s.Table("custom_field_value_options").
        Where("custom_field_value_id IN (SELECT id FROM custom_field_values WHERE task_id = ?)", evt.Task.ID).
        Delete(&CustomFieldValueOption{}); err != nil {
        return fmt.Errorf("custom-fields: cascade-delete value-options for task %d: %w", evt.Task.ID, err)
    }
    if _, err := s.Table("custom_field_values").
        Where("task_id = ?", evt.Task.ID).
        Delete(&CustomFieldValue{}); err != nil {
        return fmt.Errorf("custom-fields: cascade-delete values for task %d: %w", evt.Task.ID, err)
    }
    return s.Commit()
}
func (l *taskDeletedListener) Name() string { return "custom-fields-task-deleted" }
```

**Async-after-commit:** the listener fires in a watermill goroutine (5 retries,
`pkg/events/events.go:121-131`) *after* the task-delete transaction has committed. The
task is already gone; the listener opens its own session and deletes the now-orphaned
value rows by `task_id`.

**Benign-orphan-on-failure:** if the listener's delete fails after 5 retries, the rows
leak to the poison queue (`events.go:88-105`). But leaked rows are **unreachable** — the
read endpoint resolves the task via `models.GetTaskByIDSimple` first; a deleted task
returns not-found before any values are surfaced. So a poison-queued orphan can never be
served. The leak is a storage cost, not a data-integrity or exposure risk. The
soft-delete→hard-delete cron does not re-dispatch `TaskDeletedEvent` (`tasks.go:2082-2084`
comment), so the listener must catch the first event. This is acceptable — the "benign
orphan" class S2 invoked.

## The S9 warning (deferred to S9, not S3)

The management UI (S9) should warn when a definition edit would invalidate existing
values. This is **S9's concern**, not S3's: the rendering is S9 (the management UI), and
the backend query that powers it (a count of values that would be orphaned) is
definitions-API-shaped but touches S3's values table. S3's only obligation is to ensure
the values table is queryable by `definition_id` — which the `UNIQUE(definition_id,
task_id)` composite index and the `definition_id` index already provide. S9 adds the
count/preview endpoint and the UI warning; S3 enables it via the schema. See S9 AC#9.

## Migration

**Modify the existing S1 creation migration in place** (pattern B — unreleased feature,
`project_views` precedent; S2's identical decision, `main.go:771`). No new migration, no
new ID.

S3's changes to the existing `Migrations()` block are **additive only**:
1. `custom_field_values` gets the `UNIQUE(custom_field_definition_id, task_id)` composite
   index + a `task_id` query index (xorm struct tags on the existing struct).
2. A new fifth table: `custom_field_value_options` — `Sync2`'d alongside the existing
   four, in the same `Migrate`. The `Rollback` drops it (in dependency order:
   value-options before values before definitions).

Same explicit-table form (`tx.Table(name).Sync2(&T{})`) — required for yaegi structs
(PR #3549). Same `Sync2`-not-`partialSync` rationale (S2's watched point: our tables have
no indices beyond what the struct tags declare; if `Sync2` ever drops an index we need,
switch to a column-add fallback). The `Value` column stays `xorm:"text"` (the per-type
encoding is a model-layer concern, not a schema change). No data migration (there's no
data). The append-only switch fires when the plugin runs in production — still not the
case.

## Spikes (Task 0, the gate — same de-risk pattern as S2)

Two throwaway `main.go` replacements, run against the test instance, discarded once the
mechanism is chosen (`git diff --exit-code main.go` clean afterward).

1. **Listener receive-and-cascade spike** (gates AC#6's end-to-end). The example plugin
   + docs prove a plugin *registers* a listener and the host *delivers* the
   `*message.Message`. What's unverified is the receive-and-unmarshal-and-delete tail:
   does `json.Unmarshal(msg.Payload, &models.TaskDeletedEvent{})` populate `evt.Task.ID`
   through yaegi, and does the subsequent `s.Table("custom_field_values").Where("task_id
   = ?").Delete(...)` run clean? Throwaway plugin: register a `task.deleted` listener
   that logs `evt.Task.ID` and deletes a seeded `custom_field_values` row; delete a task
   via the API; confirm the row is gone and the log shows the right ID. Fallback ladder:
   unmarshal into a local `struct{ Task struct{ ID int64 } }` → unmarshal into
   `map[string]interface{}` and walk → any-other-viable-path. Outcome selects the
   listener body shape.

2. **Task access via `*user.User` spike** (gates the a-vs-b authorization decision). The
   single biggest open risk: can a plugin call `models.Task{ID}.CanRead(s, u)` /
   `CanUpdate(s, u)` with a `*user.User` where the host signature wants `web.Auth`?
   `*user.User` structurally satisfies `web.Auth` (`GetID()`, `web.go:60`), but whether
   yaegi's reflect conversion accepts it at the call site is unverified. Throwaway
   plugin: a route that loads a task, gets the user via
   `user.GetCurrentUserFromDB(s, c)`, calls `t.CanRead(s, u)`, returns the bool; call it
   as a project member and a non-member, confirm `true`/`false`. **Outcome bifurcates
   the authorization approach:** if it works → (a), reuse host `Task.CanRead`/
   `CanUpdate` (most faithful, least code); if it fails → (b), plugin-local xorm
   queries. Either outcome, the `CanX` signature stays `*user.User`; only the method
   body changes.

**Not spiked:** the values-model CRUD itself (insert/update/delete/select on
`custom_field_values` + the `custom_field_value_options` child table) — the same xorm
string-chaining S2 already proved works on interpreted structs (`s.Insert`,
`ReadOne`'s `s.Find(&slice)`, `setOptions`' `s.Table().Where().Delete().Insert(&slice)`).
The child-table join is a new SQL shape, verified in the real task's integration test.

## Testing strategy

Integration via the existing test instance (`compose.test.yml`, SQLite) — same as S2
(the plugin source can't be `go test`ed standalone; imports resolve only inside the
vikunja module). No unit tests of plugin source.

### Acceptance-criteria verification (via the test instance + curl + JWTs)

| AC | How verified |
|---|---|
| 1. Values returned keyed by definition id | `GET /tasks/{id}/custom-fields` → map keyed by definition id; field metadata present; value coerced to native type. |
| 2. Write values in a single request | `POST /tasks/{id}/custom-fields` with `[{field_id, value}, …]` → 200/201; values persisted; verify via `sqlite3 db/vikunja.db`. |
| 3. Validation | Invalid value (non-numeric for integer, option not in list for select, bad date) → 400; valid value → persisted. |
| 4. Fields not assigned to the task's project absent | Create a field assigned to project A; `GET` values for a task in project B (field not assigned) → field absent from the map. |
| 5. Non-members cannot read/write | Same calls with a non-member's JWT → 403 on all verbs. |
| 6. Task delete cascades values | `DELETE /api/v1/tasks/{id}` (native) → `custom_field_values` + `custom_field_value_options` rows for that task gone (verify via `sqlite3`; the listener fires async — wait/poll). **Use a select-type field** so both a value row *and* its child `custom_field_value_options` row exist to verify — a scalar-only seed would pass vacuously for the child-row cascade leg. |
| 7. Archive/move preserve values | Mark a task done (archive analog) → values intact; move task to another project → values intact (verify via `sqlite3`). |
| 8. Definition delete cascades values | `DELETE .../definitions/{id}` (S2) → `custom_field_values` + `custom_field_value_options` rows for that definition gone (verify via `sqlite3`). **Seed a select-type field's value (a `custom_field_values` row *and* its `custom_field_value_options` child row) before the delete** so the two-step cascade is falsifiable: a scalar-only seed would have no child row, so the child-row cascade leg would pass vacuously (the same falsifiability gap S2 noted in its own AC#4 — no values existed). |

**Test-instance seeding additions:** S3 needs a `custom_field_values` row *and* (for
select-type fields) a `custom_field_value_options` child row seeded before the
definition-delete test (AC#8) and before the task-delete test (AC#6) — use a select-type
field so the two-step cascade has a child row to delete. Add a seed step to
`run-test-env.sh` or seed via the values API in the test flow. Per `CLAUDE.local.md`,
this is a harness-modification point.

## Git workflow

Per `CLAUDE.local.md`: **git-flow mandatory** (not substitutable — never `git branch` +
`git checkout` for `git flow feature start`). Branch `feature/s3-task-field-values-api`
off `develop`, Conventional Commits. The spec + plan are committed to the feature
branch. **Worktree decision raised at the implementation handoff**, not now.

The implementation plan (next step, via the writing-plans skill) details the
branch/commit structure and includes **Required Skills + Recommended Skills lists per
task** (not one list for the whole plan — a correction from S2 where a single plan-level
list stood in).

## Out of scope

- Any frontend changes — S5 (the task detail view fetches and renders values from the
  plugin endpoint).
- The management UI — S9 (including the definition-edit-impact warning; S3 enables it
  via the schema, S9 builds the query + the UI).
- Filtering or searching tasks by custom field value.
- Bulk import/export of custom field values.
- The bulk `PUT` (replace-all) — the (B) write; (A) excludes it by construction.
- Inlining values into the native task `PUT`/`PATCH` body — the (B) design; rejected in
  favor of (A) (assignees/labels: dedicated endpoints are the lasting surface).
- Default synthesis on read or write — the `field_config.default` is metadata; the
  consumer may optionally use it.
- A validity flag on incompatible values — the design is silent tolerance
  (`resolveBucketID` philosophy), not annotation.
- How production-era append-migrations translate upstream (TBD when the feature
  upstreams).
- Automated unit tests of the plugin source (infeasible standalone; covered by
  integration via the test instance).
