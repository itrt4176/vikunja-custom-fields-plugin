---
title: "Task Field Values API"
description: "Custom field values can be written to and read from tasks via the plugin API."
status: pending
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