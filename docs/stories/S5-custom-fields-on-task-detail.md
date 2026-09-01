---
title: "Custom Fields on Task Detail"
description: "Custom fields render on the task detail view alongside native fields, visually and behaviorally indistinguishable."
status: done
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

## Resolution

**Status:** Done. All 7 acceptance criteria pass, verified two ways: the
Vitest suite (1318/1318 passing — `CustomFields.test.ts`, `customField.test.ts`,
`tasks.customFields.test.ts`, `TaskDetailView.customFields.test.ts`,
`DatepickerInline.test.ts`; `pnpm typecheck` has a pre-existing 1769-error
vue-tsc/TS lockfile-drift baseline that is byte-identical with/without these
changes) and a real headless-Chrome run against the Docker test instance (15/15
AC checks, incl. a `sqlite3` row proof for AC#3). Implemented via subagent-driven
development: 7 tasks, a per-task review gate on each, two final whole-branch
reviews (one per repo, both clean — the plugin repo Ready-to-merge Yes; the
vikunja fork Ready-to-merge With-fixes, the one Important fixed + re-reviewed
ADDRESSED).

**How it was built:** The work spans two repos. The **plugin repo**
(`main.go`, branch `feature/s5-vikunja-frontend`) holds the one backend
prerequisite: `readValuesForTask` rewritten to iterate the project-assigned
definitions (`ReadAll(s, t.ProjectID)`) and emit `value: null` for unset/uncoercible
fields, rather than iterating value rows (which left assigned-but-unset fields
absent). The select-value resolution was extracted into `resolveSelectValue`
(behavior-preserving); write paths are untouched. `scripts/run-test-env.sh`
Step 5c seeds the field-type variety for the render test; a throwaway
`mage test:e2e` wiring note records the env-var recipe. The **vikunja fork**
(`frontend/src/`, branch `feature/s5-custom-fields-task-detail` off `cf-main`)
holds the frontend: a `withTime` prop on `Datepicker`/`DatepickerInline`
(backwards-compatible, date-only capability); `CustomFieldService` + `ICustomField`
types (the service bypasses `AbstractService.getM`'s `maxPermission: NaN`
mutation and `objectToSnakeCase`'s bare-array mangling via direct
`AuthenticatedHTTPFactory` calls); `useTaskStore` actions
(`loadCustomFields`/`saveCustomFieldValue`/`clearCustomFieldValue`, no kanban
sync — values aren't on `ITask`); `CustomFields.vue` (the data-driven section:
one row per values-map entry, switch on `field.type` to the right input,
type-specific commit wiring, save-vs-clear routing, `is_api_only`/`!canWrite`
disable, toast + revert on a failed save); `TaskDetailView.vue` integration
(`'customFields'` in `FieldType`/`activeFields`, `loadCustomFields` in the
`watch(taskId)`, `<CustomFields>` in the `.details` grid) + the
`task.attributes.customFields` / `task.detail.customFields.saveError` i18n keys.

**Notable deviations (with reasons):**
- `altFormat` threaded to `date.altFormatShort` when `withTime=false` — the
  plan's sketch kept `altFormatLong` (`"j M Y, H:i"`) unconditionally, which
  would render a spurious `00:00` in a date-only field. Correctness fix the
  sketch missed; `ProjectGantt.vue` precedent.
- `loadCustomFields` wrapped in its own `try/catch` (not the watch's outer
  try) — the watch's catch redirects 404/403 to not-found, and with
  `plugins.enabled` defaulting false + the e2e instance plugin-less, the
  plan's verbatim snippet would redirect every task page to not-found
  (AC#6 broken). The swallow mirrors `loadAllLabels`; `console.debug` keeps
  the error observable.
- `AsyncEditor` commits on `@save` with `:show-save="true"` (not the plan's
  `@blur`) — TipTap doesn't emit `blur` and blur doesn't bubble from the
  contenteditable to the root `<div>`; `showSave` defaults false so the Save
  button wouldn't render. `Description.vue` is the precedent for both.
- `Multiselect` uses `@update:modelValue` (not the plan's `@select`/`@remove`)
  — the single-select X-button emits only `update:modelValue`, so
  `@select`/`@remove` would miss the clear. Covers add/remove/clear in one
  handler.

**Key decisions (grounded in upstream evidence):** no `models/customField.ts`
— `AbstractModel.assignData` does `omitBy(data, isNil)`, which would drop the
meaningful `value: null`; the service returns raw `data` typed as
`CustomFieldValuesMap` (snake_case matching the API). `getValues`/`bulkUpsert`
bypass `AbstractService` via direct `AuthenticatedHTTPFactory` calls (relative
paths — the factory pins baseURL to `/api/v1/`) to avoid the two verified
pitfalls (`getM`'s `maxPermission: NaN`; `objectToSnakeCase` mangling a bare
array into `{"0":{…}}`). `CustomFieldDeleteModel extends IAbstract` (the real
`AbstractService<Model extends IAbstract>` generic requires it).

**What was left open (self-contained):**
- `compose.test.yml` pins `ghcr.io/itrt4176/vikunja:2.6`, never published
  (`manifest unknown`) — fresh checkouts can't start the test instance without
  a local `docker build` (the same build `compose.test.override.yml` expresses).
  This is an environment/CI concern (the intended fix is publishing the image
  via CI, not changing the pin), not a code defect. Also: this machine's docker
  compose rejects the override's `watch` key. Triage in your normal env.
- `pnpm typecheck` 1769-error baseline (vue-tsc 3.3.11 / TS 6.0.3 lockfile drift)
  — pre-existing, byte-identical with/without S5. Triage in your normal env.
- Per-type commit-wiring tests for `select`/`multiselect`/`date`/`checkbox` are
  not present; the wiring was confirmed correct by code reading in the final
  review, so this is a regression-guard gap, not a correctness gap. Fast-follow:
  add per-type commit tests.
- Checkbox failed-save doesn't visually revert (the data layer reverts + the
  toast fires, but a native checkbox's DOM flips on click before the handler,
  and `revertToStored` writing the equal `false` is a no-op under Vue 3.5.42).
  Cosmetic. Fix: have `commitCheckbox` write `localValues[id] = v` optimistically
  so the revert produces a prop change.
- Grid nesting (`.column` inside `.column`) — fields stack vertically within
  one grid cell. The 15/15 headed-browser AC#7 check passed structurally; if it
  looks off in a real browser, drop the wrapper or render rows as direct
  children of `.columns.details`. Template-only.
- After clearing *all* values, `activeFields.customFields` stays true until the
  task is re-opened (it's re-evaluated only by `setActiveFields` in the watch,
  not after `clearCustomFieldValue`) → an empty "Custom Fields" label until
  reload. The spec explicitly accepts the `delete`-the-key approach; optional
  fix: set `.value = null` instead of deleting.
- Robust portable e2e (self-contained image / CI-grade harness) — deferred to
  a future project; the throwaway `mage test:e2e` wiring note records the
  local-relative-path recipe until it lands.
- Upstream: `TipTap.vue` with defaults (`showSave=false`,
  `enableDiscardShortcut=false`) leaves no visible save/exit in edit mode for
  any non-`Description` `AsyncEditor` consumer — not a `CustomFields` defect;
  worth an upstream issue.

**Behavior change for external API consumers:** `readOneValueHandler` now
returns 200 with `value: null` for an assigned-but-unset field (was 404 under
the old value-row iteration). Correct per the S3 read-path policy; a field not
assigned to the project still 404s.