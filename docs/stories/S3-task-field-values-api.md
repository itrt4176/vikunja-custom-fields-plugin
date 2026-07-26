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

When a task is fetched, its custom field values are included in the response. Users with task-level access can read and write those values through the API. Values are validated against their field definitions, cascade-deleted when a task is removed, and never appear for projects that don't have those fields assigned.

## What & Why

Field definitions (S2) are the schema; this story makes them useful. It provides the API to attach values to tasks, retrieve them, and update them. This is the read/write surface that both the frontend task detail view (S5) and external API consumers interact with.

Values are validated against their field definition — a number field rejects non-numeric input, a select field rejects options not in the predefined list. Authorization mirrors Vikunja's task-level access control: you can only see and edit custom field values on tasks you have permission to access.

Since Vikunja fires a `TaskDeletedEvent`, this story also registers a listener to cascade-delete values when their parent task is removed.

## Design Principles

- **Indistinguishable from native fields** — custom field values are returned as part of the task resource, not as a separate endpoint requiring extra round trips.
- **API-first, UI-second** — the API is the authoritative surface for reading and writing values. The frontend consumes it.
- **Plugin as proving ground, not permanent home** — API shapes and conventions match what a native Vikunja implementation would look like.

## Dependencies

- **Must come after:** S2 (Field Definition API)
- **Must come before:** S5 (Custom Fields on Task Detail)
- **Can run in parallel with:** S4 (Config File)

## Acceptance Criteria

1. Fetching a task includes its custom field values in the response, keyed by field definition.
2. A user can write (create/update) values for one or more custom fields on a task in a single request.
3. Values are validated against their field definition's type and constraints; invalid values return an error.
4. Fields not assigned to the task's project are absent from the response.
5. Non-members of a task's project cannot read or write custom field values on that task.
6. When a task is deleted, its custom field values are cascade-deleted.
7. Bulk operations on tasks (archive, move across projects) preserve custom field values correctly.

## Scope

**In scope:**
- CRUD API for custom field values on tasks
- Type and constraint validation on write
- Task-level authorization (inherited from Vikunja's permission model)
- TaskDeletedEvent listener for cascade cleanup
- "API-only" field support: values for API-only fields are read/write via the API but carry a flag marking them as display-only in the UI

**Out of scope:**
- Any frontend changes — S5
- Bulk import/export of custom field values
- Filtering or searching tasks by custom field value
- Handling values when a field definition is deleted (the value becomes orphaned; cleanup can be addressed later)