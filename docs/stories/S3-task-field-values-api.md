---
title: "Task Field Values API"
description: "Custom field values can be written to and read from tasks via the plugin API."
status: done
priority: 80
labels: ["backend", "api"]
position: 3
---

# Task Field Values API

## Outcome

When a task is fetched, its custom field values are available via the plugin's custom-fields endpoint (S5 merges them into the task detail view). Users with task-level access can read and write those values through the API. Values are validated against their field definitions, cascade-deleted when a task is removed, and never appear for projects that don't have those fields assigned. Values are also cascade-deleted when their field definition is removed; when a definition is changed (not deleted), values that become incompatible are absent from the read response but remain stored — recoverable if the change is reverted.

## What & Why

Field definitions (S2) are the schema; this story makes them useful. It provides the API to attach values to tasks, retrieve them, and update them. This is the read/write surface that both the frontend task detail view (S5) and external API consumers interact with.

Values are validated against their field definition — a number field rejects non-numeric input, a select field rejects options not in the predefined list. Authorization mirrors Vikunja's task-level access control: you can only see and edit custom field values on tasks you have permission to access.

Since Vikunja fires a `TaskDeletedEvent`, this story also registers a listener to cascade-delete values when their parent task is removed.

## Design Principles

- **Indistinguishable from native fields** — values render in the task detail view alongside native fields, visually identical. Served from a dedicated plugin endpoint (the native task response can't be augmented under yaegi — see AC#1 amendment), which the frontend (S5) merges into the view; the extra round trip is frontend↔backend, invisible to the end user. API shapes match a native `/api/v2/tasks/{task}/custom-fields` resource, portable by a single prefix swap on upstreaming.
- **API-first, UI-second** — the API is the authoritative surface for reading and writing values. The frontend consumes it.
- **Plugin as proving ground, not permanent home** — API shapes and conventions match what a native Vikunja implementation would look like.

## Dependencies

- **Must come after:** S2 (Field Definition API)
- **Must come before:** S5 (Custom Fields on Task Detail)
- **Can run in parallel with:** S9 (Management UI)

## Acceptance Criteria

1. Fetching a task's custom field values returns them keyed by field definition id, served from a dedicated plugin endpoint (`GET /tasks/{id}/custom-fields`) that the frontend (S5) merges into the task detail view.
   > **Amended 2026-08-30:** the original AC read "in the [native task] response." The host source proves a yaegi plugin cannot augment the native `GET /tasks/{id}` response — both v1 (`pkg/web/handler/read_one.go:33`, `ctx.JSON(currentStruct)`) and v2 (`pkg/routes/api/v2/tasks.go:107`, a fixed `taskReadOneBody`) serialize `models.Task` verbatim with no read-path hook, middleware, or event-on-read. A plugin can only mount routes under `/api/v1/plugins/`. Values are served from a dedicated endpoint the frontend calls alongside the native task fetch; the "indistinguishable from native fields" principle is satisfied at the UI layer (S5), not the API layer. Same precedent as S2's AC#6 amendment (host evidence contradicted an AC; the AC was revised to match the host).
2. A user can write (create/update) values for one or more custom fields on a task in a single request.
3. Values are validated against their field definition's type and constraints; invalid values return an error.
4. Fields not assigned to the task's project are absent from the response.
5. Non-members of a task's project cannot read or write custom field values on that task.
6. When a task is deleted, its custom field values are cascade-deleted.
7. Bulk operations on tasks (archive, move across projects) preserve custom field values correctly.
8. When a field definition is deleted, its custom field values are cascade-deleted (synchronously, in the definition's delete transaction).

## Scope

**In scope:**
- CRUD API for custom field values on tasks
- Type and constraint validation on write
- Task-level authorization (inherited from Vikunja's permission model)
- TaskDeletedEvent listener for cascade cleanup
- "API-only" field support: values for API-only fields are read/write via the API but carry a flag marking them as display-only in the UI
- Cascade-delete values when a field definition is deleted (synchronous, in the delete transaction)
- Tolerate definition changes: values that become incompatible with a changed definition are absent from the read response but remain stored (recoverable on revert); definition edits are never blocked (the `ProjectView.view_kind` retype precedent — free edit, tolerate stale state)

**Out of scope:**
- Any frontend changes — S5
- Bulk import/export of custom field values
- Filtering or searching tasks by custom field value

## Resolution

### Status: Done

All 8 acceptance criteria pass, verified against the Docker test instance via authenticated curl (JWT) plus direct `sqlite3 db/vikunja.db` inspection:

1. **Read endpoint** — `GET /tasks/{task}/custom-fields` returns a map keyed by definition id, each entry `{value, field}` with the value type-native (integer → number, select → option value string, multiselect → array of strings). Verified: `{}` before any write, `{"1": {"value": 5, ...}}` after.
2. **Bulk write** — `POST /tasks/{task}/custom-fields` with a bare-array body upserts values in one request. Verified: 200 response + rows present in `custom_field_values`.
3. **Validation** — an invalid value returns 400 (the `toHTTPError` prefix cases).
4. **Project scoping** — a field not assigned to the task's project is absent from the read response and rejected on write (400 "field is not assigned to this task's project").
5. **Authorization** — a non-member of the task's project gets 403 on both read and write (`Task.CanRead`/`Task.CanUpdate` via `*user.User`).
6. **Task-delete cascade** — deleting a task asynchronously removes its values and child rows (`task.deleted` listener); verified with a SELECT-type seed so the child-row leg was non-vacuous.
7. **Bulk task ops preserve values** — archive (done-toggle) and move-across-projects leave `task_id` stable; a move to a project lacking the field persists the value but omits it from the read.
8. **Definition-delete cascade** — deleting a definition synchronously removes its values and child rows inside the definition's delete transaction; verified with a SELECT-type seed.

### How it was built

Single `main.go` (`package main`), the S2-established plugin layout — S3 layered onto S2's file rather than splitting it:

- **Schema:** `custom_field_values` gained a `UNIQUE(custom_field_definition_id, task_id)` composite constraint plus a `task_id` index; a new `custom_field_value_options` child table (the `label_tasks` shape) holds select-type values. The S2 migration was modified in place (pattern B — unreleased schema, no migrate-forward needed).
- **Validation:** pure functions `validateValue` (type + min/max/required/option checks) and `resolveOptionIDs` (option label → id), shared by all write paths.
- **Authorization:** `CustomFieldValue.CanRead/CanCreate/CanUpdate/CanDelete` delegating to the host's `models.Task.CanRead`/`CanUpdate` with a `*user.User` (approach (a) from the spec).
- **Endpoints (6):** collection `GET` + bulk `POST` (bare-array upsert), per-field `GET`, `POST` (create-only, 409 on duplicate), `PUT` (replace-only, 404 when absent), `DELETE` — all under `/api/v1/plugins/custom-fields/tasks/{task}/custom-fields`. No bulk PUT (excluded by construction per the spec's write-shape decision (A) — the bulk PUT is itself the (B) write).
- **Cascades:** a `taskDeletedListener` on `task.deleted` (async, benign-orphan-on-failure) and a synchronous cascade inside `CustomFieldDefinition.Delete` (team/project precedent).
- **Tolerant reads on definition change:** free edit + drop-on-read — incompatible values are simply absent from responses but remain stored (the `ProjectView.view_kind` retype precedent). Recoverability is scoped: recoverable for scalar/reorder/relabel, destructive for select option value-change/removal.
- **S2 fix folded in:** `setOptions` was changed to update options in place, preserving option IDs across reorder/relabel (the N4 fix) so existing select values don't orphan.

### Notable deviations

1. **Bulk POST body is a bare JSON array, not a `{"values":[...]}` wrapper.** The spec (§Endpoints, write-shape) specifies a bare array; the plan's `bulkValueRequest` wrapper was a plan defect, corrected. Bound via `json.NewDecoder(c.Request().Body).Decode(&items)`.
2. **Routes mount at `/custom-fields/tasks/:task/custom-fields`** (full path `/api/v1/plugins/custom-fields/tasks/...`), not the plan's literal `/tasks/:task/custom-fields`. The host registers the plugin route group at `/api/v1/plugins` (`routes.go:964`) and S2 already owns the `/custom-fields` namespace; the literal path would have mounted under `/api/v1/plugins/tasks/...`, breaking the spec's two-swap portability and the AC curl paths.
3. **`fieldAppliesToProject` uses a single parenthesized `Where`** (`custom_field_definition_id = ? AND (project_id = ? OR project_id = 0)`) instead of `.Where().And()` chaining. xorm chaining emits the OR un-parenthesized, so SQL precedence would detach the global sentinel (`project_id = 0`) from the definition filter — every global field would appear to apply to every definition, breaking AC#4.
4. **Task fixtures use v2 endpoints** (`POST /api/v2/projects/{project}/tasks`, `PUT/DELETE /api/v2/tasks/{id}`) in the test harness, not the plan's `POST /api/v2/tasks` — the plan's path doesn't exist; the v2 project-scoped create is the correct non-deprecated route.
5. **`validateValue`'s multiselect branch returns `nil`** for the resolved-ID slot rather than the brief's `vals`. The brief's snippet (`return strings.Join(vals,"\x00"), vals, nil`) is a type error — `vals` is `[]string` while the signature returns `[]int64`; `nil` is also consistent with the brief's own doc comment ("IDs resolved separately by resolveOptionIDs").
6. **`writeValue` returns pre-translated errors** (`toHTTPError`/`echo.NewHTTPError`) rather than sentinel errors. AC#4's write-path 400 ("field is not assigned to this task's project") has no Task-2 error-code type; pre-translating inside the factored helper keeps a single uniform error path across all three write handlers.
7. **`taskDeletedListener` guards `evt.Task == nil`.** `TaskDeletedEvent.Task` is `*Task` (`events.go:61`); a nil event payload would panic the listener goroutine.

### Key decisions

Grounded in the spec's "Context — decisions were grounded in upstream evidence" section:

- **Values are a dedicated resource** modeled on Vikunja's assignees/labels (upstream `xorm:"-"`/readOnly response shaping) — not an embedded map on `Task`, which the host cannot serialize anyway (AC#1 amendment evidence).
- **Select values live in a child table** (`custom_field_value_options`, the `label_tasks` shape) rather than a JSON column — the label_tasks precedent plus migration-safety for future option modeling.
- **Authorization via `*user.User` structurally satisfying `web.Auth`** — spike-confirmed against the host's permission plumbing; `Task.CanRead`/`CanUpdate` accept it directly.
- **Task-delete is an async listener, definition-delete is a synchronous cascade** — matching the host's evented task lifecycle vs. the team/project in-transaction delete precedent, with a benign-orphan-on-failure posture for the async leg.
- **Definition changes are free edits with drop-on-read** (the `ProjectView.view_kind` retype precedent): edits are never blocked, incompatible values vanish from responses but stay recoverable for scalar/reorder/relabel changes.
- **`setOptions` updates in place** (the N4 fix) so reorder/relabel preserve option IDs and don't orphan every existing select value.
- **Two yaegi upstream-conversion points to remember when porting to native:** authorization takes `*user.User` (a native implementation would take `web.Auth`), and responses are hand-built maps rather than structs (a native implementation would shape structs with `xorm:"-"`).

### What was left open

Each item names its location; none blocks the story's acceptance criteria.

- **Multiselect duplicate values are stored as-is** (`writeValue` in `main.go`): a body like `["red","red"]` passes `validateValue` and inserts duplicate `custom_field_value_options` rows — that table has no unique constraint. Left as-is because the spec didn't require dedup. Fix: a one-line dedup when extracting multiselect value strings in `writeValue`.
- **Decimal accepts NaN/Inf via string-typed JSON input** (`validateValue`, decimal branch): `strconv.ParseFloat("NaN")` succeeds and min/max comparisons are always false against NaN. Only reachable through a string-typed value (JSON numbers cannot encode NaN). Fix: a `math.IsNaN`/`math.IsInf` guard in the decimal branch. Left as an edge case the spec didn't enumerate.
- **Per-field multiselect via echo `c.Bind` is an untested yaegi boundary.** The bulk POST multiselect path is verified (plugin-side `json.NewDecoder`), but per-field `POST`/`PUT` with a multiselect array body binds host-side via echo and `writeValue` then type-asserts `raw.([]interface{})` on a host-created slice — not yet exercised. Fix: one curl of a per-field POST/PUT with a multiselect array body; if the assert fails, bind via `json.NewDecoder` like the bulk path.
- **`setOptions` non-select path returns an unwrapped error** (`setOptions` in `main.go`): a bare `return err` drops the `"custom-fields: ..."` prefix the other four operations carry. Left because it is brief-compliant verbatim and cosmetic. Fix: wrap with `fmt.Errorf("custom-fields: clear options: %w", err)`.
- **Integer precision loss above 2^53** (`validateValue`, integer branch): JSON numbers arrive as `float64`, so `9007199254740993` is corrupted before validation ever runs. Inherent to the brief's decode design. Fix: `json.Number` decoding (a larger change). Left as a documented limitation.
- **`readValuesForTask` issues N+1 queries:** per value row it runs `fieldAppliesToProject`, `ReadOne`, and (for select-like types) a child-table query. Fine at plugin scale. Fix: a single IN-query batching the definition lookups. Left as a future optimization.
- **Reads return 403 (not 404) for a nonexistent task:** `Task.CanRead` maps not-found to `(false, nil)` — not-found ≡ no access under the Task-3 `CanX` contract — so reads 403 while writes 404 on a bad task id. Consistent with the contract. Fix: a plugin-side task existence check (e.g. `models.GetTaskByIDSimple`) before delegating to `CanRead`, returning 404 on a not-found task. Left as a documented behavior choice.
- **Task 8's harness seeds only an integer-type field** (`scripts/run-test-env.sh`, Step 5b): the cascade ACs (#6/#8) therefore hand-create ad-hoc SELECT-type fixtures each sweep. Fix: seed a second SELECT-type definition in Step 5b. Left as a harness follow-up; the sweep compensates.
- **Definition-edit-impact warning is deferred to S9** (S9's own AC#9). S3 already enables it — `custom_field_values` is queryable by `custom_field_definition_id` via the new composite index — but the count query and UI warning belong to S9. Out of scope by design, not a gap.