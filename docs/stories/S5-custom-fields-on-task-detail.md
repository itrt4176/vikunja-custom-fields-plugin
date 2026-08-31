---
title: "Custom Fields on Task Detail"
description: "Custom fields render on the task detail view alongside native fields, visually and behaviorally indistinguishable."
status: pending
priority: 95
labels: ["frontend"]
position: 5
---

# Custom Fields on Task Detail

## Outcome

When a user opens any task in Vikunja, the custom fields assigned to that task's project appear in the task detail view — mixed in with native fields, visually identical, using the same interaction patterns. The task detail view fetches custom field values from the plugin's custom-fields endpoint (S3), which includes the field definitions in the response, and renders each field according to its type. Editing a custom field value and saving persists it through the custom-fields API (S3), not through the native task save body (custom field values are a separate resource, following the assignees/labels precedent — they are `xorm:"-"` readOnly on the task, managed through dedicated endpoints). Fields that aren't assigned to the project simply aren't shown.

## What & Why

This is where custom fields become real to the user. The APIs built in S2 and S3 are invisible infrastructure; this story makes them tangible. The frontend task detail view fetches custom field values from the plugin's custom-fields endpoint (S3), which returns each value alongside its field definition (type, constraints, options) in a map keyed by field definition id, then renders each field according to its type — a text input for a text field, a date picker for a date field, a dropdown for a select field, and so on.

The rendering is data-driven: no field type is hardcoded. The frontend reads the field definition (type, constraints, options) and renders the appropriate input. When the field type is "API-only," the value is displayed but the input is disabled.

## Design Principles

- **Indistinguishable from native fields** — this is the story that directly delivers that principle. A user should not perceive any difference between a built-in field and a custom field. Same visual treatment, same interaction patterns.
- **Centrally governed** — the user sees the fields they're given. There is no UI for users to add, remove, or reconfigure fields on their tasks.
- **Plugin as proving ground, not permanent home** — the frontend integration is designed as if custom fields were a native API resource, consuming it from the plugin prefix only because the plugin system requires it.

## Dependencies

- **Must come after:** S3 (Task Field Values API)
- **Must come before:** S7 (Build, Deploy & Document)
- **Can run in parallel with:** S9 (Management UI)

## Acceptance Criteria

1. Opening a task in a project with assigned custom fields shows those fields in the task detail view.
2. Each custom field renders an appropriate input based on its type (text, number, date, select, checkbox, URL).
3. Editing a custom field value and saving persists the new value through the custom-fields API (S3), not through the native task save body.
4. Changing a custom field value and navigating away without saving discards the change.
5. Fields marked "API-only" display their value but are not editable through the UI.
6. Tasks in projects without custom fields show no custom field section — the task detail view is unchanged from stock Vikunja.
7. The custom field section has no visual distinction from native fields — same styling, same layout rhythm.

## Scope

**In scope:**
- Modifications to the task detail (modal) view to fetch and render custom fields
- Data-driven input rendering by field type (text, multiline text, number, decimal, date, datetime, select, multi-select, checkbox, URL)
- Read-only rendering for "API-only" fields
- Save/discard of custom field values through the custom-fields API (S3): the task detail view persists value edits through the values endpoints (per-field PUT for single edits, bulk POST for multi-field saves), not through the native task save body (custom field values are a separate resource, following the assignees/labels precedent)
- Rendering values from the S3 response shape: a map keyed by field definition id, each entry `{value, field}` where `field` carries the full definition (type, options, field_config) for data-driven rendering
- Rendering `value: null` (no valid value — unset, or incompatible with a changed definition) as an empty field; the API does not synthesize defaults (`field_config.default` is metadata the consumer may optionally use, not a server-side fallback)

**Out of scope:**
- The quick-add magic input for task creation (per PRD: "Quick Add Magic support for custom fields" is out of scope)
- Custom fields in list/kanban/table/gantt views — task detail only
- The management UI for field definitions — S9
- Validation error display style (errors are shown, but exact placement is left to implementation)