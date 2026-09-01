# S5 — Custom Fields on Task Detail: Design Spec

**Date:** 2026-09-01
**Story:** [S5 — Custom Fields on Task Detail](../../stories/S5-custom-fields-on-task-detail.md)
**Status:** Approved (pending spec review)

## Summary

S5 makes custom fields tangible to the user. The backend APIs built in S2
(field definitions) and S3 (task field values) are invisible infrastructure; this
story renders them on the task detail view — mixed in with native fields, visually
indistinguishable, using the same interaction patterns.

The frontend fetches a task's custom field values from the plugin's values
endpoint (S3) in the same `watch(taskId)` load as the native task `GET`, and renders
each field according to its definition's type — a text input for a text field, a date
picker for a date field, a `Multiselect` for a select field — driven entirely by the
`field` metadata in the S3 response (`type`, `options`, `field_config`). No field type
is hardcoded. Editing a value persists it through the custom-fields values endpoint
(upsert on change, per-field `DELETE` to clear) — not through the native task save body.
Values are a separate resource, the assignees/labels precedent (S3 AC#1: the native
task `GET` response can't be augmented under yaegi; values are served from a
dedicated plugin endpoint the frontend calls alongside the native fetch). Fields
marked `is_api_only` display their value but are not UI-editable. Tasks in projects
without custom fields show no custom-field section — the view is unchanged from stock.

**One backend prerequisite** is required before the frontend can render the complete
field set: the S3 read path returns only fields that already have a value row, not
every field assigned to the task's project — so assigned-but-unset fields are absent
from the response. That breaks the core UX (a task with assigned fields but no values
returns `{}`, leaving nothing to render for the empty fields the user fills in). S5
fixes the plugin's `readValuesForTask` to iterate the project's assigned definitions and
emit `value: null` for unset ones — the S3 spec's intended read-path policy, which the
shipped implementation diverged from. The frontend does not work around it (the fix
belongs in the plugin, per the design principle).

## Context — decisions were grounded in upstream evidence, not assumption

S2 and S3 established the discipline: every load-bearing fact was checked against the
live `vikunja/` fork source and the plugin source, not inherited. S5 continues it. The
frontend investigation was run against `vikunja/frontend/src/` (this fork); the plugin
investigation against `vikunja-custom-fields-plugin/main.go`. All citations are
`file:line` under the respective repo.

### The task detail view (the surface S5 extends)

**`frontend/src/views/tasks/TaskDetailView.vue`** is the single component that renders
both the full-page task view and the modal form. It detects modal mode from a
`backdropView` prop (`TaskDetailView.vue`, `isModal` computed). The template is a
two-column layout: a `.details` grid of native fields (left, two-thirds when `canWrite`)
and an action-button column (right, one-third).

Every native field is a `div.column` in the `.details` grid, guarded by
`v-if="activeFields.<field>"`, with a `.detail-title` (icon + i18n string) + the input
component bound `v-model` + `@update:modelValue="saveTask()"`. `saveTask()` calls
`taskStore.update(currentTask)` (`stores/tasks.ts:185`), which calls the task service's
`update` and syncs the kanban store. **Native fields auto-save on change — there is no
modal-level Save/Cancel; every field commit persists immediately.**

The `activeFields` reactive map (`TaskDetailView.vue`, `FieldType` union +
`reactive({...})`) controls section visibility. `setActiveFields()` flips a field on
when the loaded task already has a value (e.g. `activeFields.dueDate = task.value.dueDate
!== null`); `setFieldActive(name)` flips it on from a right-column action button
(`@click="setFieldActive('priority')"`). The attachments section uses the auto-show
variant: `v-show="activeFields.attachments || hasAttachments"` — shown when any exist,
without a manual toggle. Time tracking is feature-gated: `v-if="timeTrackingEnabled &&
activeFields.timeTracking"`.

The fetch path: `watch(() => props.taskId, ...)` calls `taskService.get({id}, {expand:
['reactions','comments','is_unread','buckets']})` then `Object.assign(task.value,
loaded)` + `setActiveFields()`.

### The assignees/labels precedent (the exact pattern S5 mirrors)

Assignees and labels are `xorm:"-"` relations on the backend `models.Task` — not DB
columns — managed through **dedicated endpoints**, never the task body. This is the
precedent S5 mirrors, because custom field values are the same shape (S3: a separate
resource with its own CRUD endpoints, `xorm:"-"` on the task).

- **Service layer:** `frontend/src/services/taskAssignee.ts` — `create:
  '/tasks/{taskId}/assignees'`, `delete: '/tasks/{taskId}/assignees/{userId}'`;
  `frontend/src/services/labelTask.ts` — `create: '/tasks/{taskId}/labels'`, `delete:
  '/tasks/{taskId}/labels/{labelId}'`. Both extend `AbstractService`
  (`frontend/src/services/abstractService.ts:44`), which substitutes `{param}` in path
  templates (confirmed at `abstractService.ts:31`) and uses
  `AuthenticatedHTTPFactory()` (`abstractService.ts:53`) whose baseURL is `/api/v1`
  (`frontend/src/helpers/fetcher.ts:9-26`, `getApiBaseUrl` → `window.API_URL`, which
  defaults to `/api/v1`).
- **Store actions:** `frontend/src/stores/tasks.ts` — `addAssignee` (`:233`),
  `removeAssignee` (`:274`), `addLabel` (`:308`), `removeLabel` (`:343`). Each does its
  own HTTP call (via the corresponding service) then updates the **kanban store's**
  task copy: `kanbanStore.getTaskById(taskId)` → `kanbanStore.setTaskInBucketByIndex({...t,
  task: {...t.task, assignees: [...t.task.assignees, user]}})`. The modal holds a local
  `task` ref; only `saveTask` goes through `taskStore.update`.
- **UI components:** `frontend/src/components/tasks/partials/EditAssignees.vue` and
  `EditLabels.vue` receive `v-model` (the current `task.assignees`/`task.labels`) plus
  `taskId`; on each add/remove they call the store action and emit
  `update:modelValue`. Both wrap the generic `Multiselect.vue`
  (`frontend/src/components/input/Multiselect.vue`, `:multiple` true/false).

### Reusable input components (the type → input map is built from these)

| Purpose | File |
|---|---|
| Date / datetime picker | `frontend/src/components/input/Datepicker.vue` |
| Single- or multi-select (tag-style) | `frontend/src/components/input/Multiselect.vue` (`:multiple`, `searchResults`, `@select`, `#tag`/`#searchResult` slots) |
| Native select over an enum | `frontend/src/components/input/FormSelect.vue`; `tasks/partials/PrioritySelect.vue`, `PercentDoneSelect.vue` (tiny native `<select>` over a constant) |
| Number / text input | `frontend/src/components/input/FormInput.vue` (supports `modelModifiers.number`), `FormField.vue` |
| Multiline rich editor | `frontend/src/components/input/AsyncEditor.ts` (dynamic TipTap import — the same editor the Description field uses) |
| Checkbox | `frontend/src/components/input/FancyCheckbox.vue` (styled), `FormCheckbox.vue` (native) |
| URL | No dedicated component — `FormInput` with `type=url` is the precedent |

### i18n

`frontend/src/i18n/lang/en.json` (one file per locale); `task.attributes.*` for field
labels, `task.detail.actions.*` for action buttons, `task.detail.updateSuccess` for
the save toast. Field *names*, option *labels*, and descriptions come from the API
(`field.name`, `field.options[].label`) — data-driven, not translated — so only
structural strings (the section label, empty/error toasts) need keys. Per the repo
rule, only `en.json` is edited; other locales flow through the dedicated translation
workflow.

## The backend prerequisite (plugin repo) — fix the S3 read path

### The divergence

The S3 spec's read-path policy (§Read-path policy) specifies: a field assigned to the
task's project appears in the response **with `value: null` when unset or invalid** —
the field-presence axis (AC#4) is separate from the value axis (`null` if absent), and
the `field` metadata is always present when the key is present. This is what lets the
frontend render every assigned field — including the empty ones the user fills in — from
a single fetch.

The shipped `readValuesForTask` (`vikunja-custom-fields-plugin/main.go:1140`) instead
iterates `custom_field_values` rows for the task (`s.Table("custom_field_values").Where("task_id
= ?", taskID).Find(&values)`, `main.go:1146`) and emits one map entry per **value row**.
A field assigned to the task's project but with no value row is **absent** from the
response, not `value: null`. The S3 resolution's "`{}` before any write" note
(`S3-task-field-values-api.md` Resolution, AC#1) is the symptom: a task with assigned
fields but no values returns `{}`.

This breaks S5's core UX: the frontend fetches the map, and a task with assigned custom
fields but no values set gets `{}` — nothing to render for the empty fields the user
needs to fill in. The "one fetch, definitions embedded" intent (S5 story, §What & Why)
requires the response to carry every assigned field.

### Why the frontend cannot work around it (ruled out on principle and on feasibility)

The alternative — the frontend fetches the project's field definitions (S2) and overlays
them — is ruled out. **On feasibility:** `CustomFieldDefinition.CanRead` delegates to
`IsManager(u.Username)` (`main.go:454`), and the definitions list handler `listHandler`
(`main.go:979`) enforces it — a regular project member gets `403` on
`GET /plugins/custom-fields/definitions`. The task detail view's audience is regular
project members, who can only call the values endpoint. **On principle (the user's
ruling):** if the plugin implementation is incorrect, the fix belongs in the plugin,
regardless of whether a frontend workaround is possible. Correct the source, don't paper
over it.

### The fix (read-path only, no write-path change, no migration)

Rewrite `readValuesForTask` to iterate the task's **project-assigned definitions** and
look up each one's value, rather than iterating value rows:

1. Resolve the task's `ProjectID` via `models.GetTaskByIDSimple(s, taskID)` (already
   called at `main.go:1141`).
2. Fetch the assigned definitions via `ReadAll(s, t.ProjectID)` (`main.go:676`), which
   already filters `project_id = pid OR project_id = 0` (the global sentinel) and orders
   by `display_order asc`. This is the existing S2 query — no new SQL.
3. For each definition, look up its value row (`custom_field_values` by
   `(custom_field_definition_id, task_id)`) and resolve select-type child rows
   (`custom_field_value_options`) exactly as today. Coerce per type
   (`coerceReadValue`, `main.go` — the existing coercion). **No value row → `value: null`.**
   A stored value that fails coercion against the definition's current
   type+constraints → `value: null` (the same tolerate-stale-silently behavior, now
   reached per-field rather than only for fields that have a row).
4. Emit one map entry per **assigned definition**, keyed by `definition_id` as a string,
   `{value, field}` — `field` is always present (it comes from the definition, not the
   value row); `value` is the coerced scalar or `null`.

This makes the map non-empty iff the project has assigned custom fields (every assigned
field appears, `value: null` or not), which is exactly what the frontend's "show the
section when the project has custom fields" logic needs (§When the section shows). It
matches the S3 spec's intended read-path policy; the divergence was in the
implementation, not the spec. Write paths (`writeValue`, `main.go:1289`; the bulk POST
upsert; the per-field `POST`/`PUT`/`DELETE`) are untouched — the fix is read-path only.
No schema change, no migration (the feature is unreleased; read-path logic is a
`main.go` edit). The `fieldAppliesToProject` AC#4 check (`main.go`, the project-assignment
filter) becomes redundant inside `readValuesForTask` (the definitions are already
project-filtered by `ReadAll`) but stays in `writeValue` (the write path still enforces
it independently).

## Architecture & file layout (vikunja frontend)

S5 mirrors the assignee/label precedent. New files follow the existing
model/service/component/store split; the only existing file modified is
`TaskDetailView.vue` (to add the section) and `en.json` (structural strings).

```
frontend/src/
├── modelTypes/
│   └── ICustomField.ts                 NEW — the {value, field} entry + CustomFieldDefinition shape + the map type
├── models/
│   └── customField.ts                  NEW — CustomFieldModel (parses field.options, field_config; value as native type)
├── services/
│   └── customField.ts                  NEW — CustomFieldService extends AbstractService; paths under the plugin prefix
├── stores/
│   └── (useTaskStore, extended)         MODIFIED — loadCustomFields/saveCustomFieldValue/clearCustomFieldValue actions
├── components/tasks/partials/
│   └── CustomFields.vue                NEW — the data-driven section; one row per map entry, switch on field.type
├── views/tasks/
│   └── TaskDetailView.vue              MODIFIED — 'customFields' in FieldType + activeFields; load + render the section
└── i18n/lang/
    └── en.json                         MODIFIED — task.attributes.customFields + empty/error toasts (structural strings only)
```

### `modelTypes/ICustomField.ts` (NEW)

The S3 read response shape (verified against the spec's §Read response shape and
`valueToMap`/`definitionToMap` in `main.go`):

```ts
export interface ICustomFieldOption {
	id: number
	value: string          // the option's value string (the stored identity)
	label: string          // the display label
	display_order: number
}

export interface ICustomFieldConfig {
	required?: boolean
	default?: string
	is_api_only?: boolean
	min?: number           // integer/decimal range constraint
	max?: number
}

export interface ICustomFieldDefinition {
	id: number
	name: string            // the display label — data-driven, not i18n
	type: CustomFieldType
	description?: string
	field_config: ICustomFieldConfig
	display_order: number
	options?: ICustomFieldOption[]   // present for select/multiselect
	project_ids: number[]
}

export type CustomFieldType =
	| 'text' | 'textarea' | 'integer' | 'decimal' | 'date' | 'datetime'
	| 'select' | 'multiselect' | 'checkbox' | 'url'

// One entry in the values map: {value, field}
export interface ICustomFieldValue {
	value: unknown         // type-native: number, string, string[], boolean, or null
	field: ICustomFieldDefinition
}

// The S3 read response: map keyed by definition_id (as a string)
export type CustomFieldValuesMap = Record<string, ICustomFieldValue>
```

### `services/customField.ts` (NEW)

`CustomFieldService extends AbstractService` with paths under the plugin route prefix
(base `/api/v1`, confirmed `fetcher.ts:9-26`):

```ts
export default class CustomFieldService extends AbstractService {
	constructor() {
		super({
			get: '/plugins/custom-fields/tasks/{taskId}/custom-fields',       // GET → the map
			create: '/plugins/custom-fields/tasks/{taskId}/custom-fields',    // POST (bulk upsert, bare array)
			delete: '/plugins/custom-fields/tasks/{taskId}/custom-fields/{fieldId}',  // DELETE one value
		})
	}
	// The bulk POST body is a bare array [{custom_field_definition_id, value}, …],
	// not a {values: [...]} wrapper (S3 §Write request shapes, verified against
	// writeValue at main.go:1289). The service sends the array directly.
}
```

**Implementation point to verify (not load-bearing for the design):** `AbstractService`'s
`create`/`update` methods may transform the body via `processModel`/`autoTransformBeforePost`
(seen in `task.ts`, which disables it). The bulk POST's bare-array body may need to bypass
that transform — same class of detail `TaskService` already handles. Resolved in
implementation by reading `abstractService.ts`'s `create` body handling.

## The store & data flow (native-first, then the plugin/fork approximation)

### Native-first (if custom fields were a core feature)

Values would be a `customFields: CustomFieldValuesMap` field on `models.Task` —
`xorm:"-"`, `readOnly:"true` (the `Reactions`/`MaxPermission` enrichment precedent,
`tasks.go:170`) — populated by the native task `GET` (like `task.assignees`), with
`ITask.customFields` and `useTaskStore` actions (`addCustomFieldValue`/etc.) that sync
the **kanban copy** via `kanbanStore.getTaskById` → `setTaskInBucketByIndex`, exactly as
`addAssignee` does (`stores/tasks.ts:233-272`). The modal's `watch(taskId)` fetch would
be a single `taskService.get` that already carries `customFields`.

### The plugin/fork approximation (this phase)

The native task `GET` cannot be augmented under yaegi (S3 AC#1, settled against
`pkg/web/handler/read_one.go:33` and `pkg/routes/api/v2/tasks.go:107-131`). So:

1. **Separate fetch.** `loadCustomFields(taskId)` calls `customFieldsService.get({id:
   taskId})` → stashes the map in a `customFieldValues: Ref<Record<ITask['id'],
   CustomFieldValuesMap>>` (keyed by taskId, analogous to how `tasks` is keyed by id at
   `stores/tasks.ts:139`). Called in `TaskDetailView`'s `watch(taskId)` **alongside**
   `taskService.get(...)`. This is the "extra round trip" S3's AC#1 amendment budgeted;
   it is frontend↔backend, invisible to the end user.

2. **No kanban sync (the one forced divergence).** `addAssignee`/`removeLabel` sync the
   kanban copy because `assignees`/`labels` *are* fields on the kanban `ITask`
   (`t.task.assignees`). Custom-field values are **not** on `ITask` (S3 AC#1) — there is
   no kanban copy to sync. The actions own the `customFieldValues` ref instead. This is
   the only point where custom fields break the assignee/label mold, and it is forced by
   the S3 amendment, not a design choice. Upstream conversion: when values become a
   `xorm:"-"` field on `models.Task`, the actions gain the kanban sync and the
   `customFieldValues` ref collapses into `task.customFields`.

3. **Save (immediate commit — the user's decision).** `saveCustomFieldValue({taskId,
   fieldId, value})` → upsert `POST [{custom_field_definition_id: fieldId, value}]` (one
   item; the bulk upsert handles create+update, sidestepping the per-field `POST`-409 /
   `PUT`-404 dance) → on success, update `customFieldValues[taskId][fieldId].value`. This
   mirrors `addAssignee`'s "HTTP + update local" shape and the native-field
   `@update:modelValue="saveTask()"` immediate-commit pattern. The bulk `POST` is the
   "save these values" operation; the per-field `POST`/`PUT`/`DELETE` remain available to
   external API consumers (S9 management UI, API clients) but are not the modal's save
   path.

4. **Clear (the user's decision).** `clearCustomFieldValue({taskId, fieldId})` →
   per-field `DELETE /.../custom-fields/{fieldId}` → remove the key from
   `customFieldValues[taskId]`. Mirrors `removeAssignee` (`stores/tasks.ts:274`). The
   row is gone; the next read returns `value: null`. (Rejected alternative: upsert
   `null` via the bulk POST — leaves a null-valued row instead of removing it; `DELETE`
   is cleaner and matches the endpoint S3 already exposes.)

5. **Discard on navigate (AC#4).** Under immediate commit, an *in-progress* text edit
   (typed but not blurred) is local to the input; navigating away discards it because the
   upsert never fired. Discrete edits (select/date/checkbox) commit immediately on change,
   so there is nothing to discard. This satisfies AC#4 ("changing a value and navigating
   away without saving discards the change") and AC#7 ("same interaction patterns as
   native fields") — native fields auto-save on change and have no modal-level Save/Cancel.

6. **Optimistic vs on-success.** Update the store on success, not optimistically — the
   input holds the local value until commit, then snaps to the confirmed value. Avoids
   rollback complexity on a failed upsert (a 400 for a bad date/out-of-range number
   reverts the input to the stored value).

### `CustomFields.vue` (NEW — the data-driven section)

Iterates `taskStore.customFieldValues[taskId]` (sorted by `field.display_order`), one
row per entry, each in the same `div.column` + `.detail-title` + `CustomTransition`
rhythm as Priority/Due Date. Each row `switch`es on `field.type` to pick the input
component (§Field-type → input map), binds the input to `entry.value`, and on commit
calls `taskStore.saveCustomFieldValue` / `clearCustomFieldValue`. `:disabled="!canWrite ||
field.field_config.is_api_only"`. No per-type hardcoded files — the type set is runtime
data from the definition.

## Field-type → input mapping (data-driven, one code path)

| `field.type` | component | bound value | commit event | notes |
|---|---|---|---|---|
| `text` | `FormInput` | `string` | blur | |
| `textarea` | `AsyncEditor` (TipTap — the Description editor) | `string` | blur | matches the Description field's editor |
| `integer` | `FormInput` (`type=number`, `step=1`) | `number` | blur | `min`/`max` from `field_config` |
| `decimal` | `FormInput` (`type=number`, `step=any`) | `number` | blur | `min`/`max` from `field_config` |
| `date` | `Datepicker` | ISO `YYYY-MM-DD` | change | |
| `datetime` | `Datepicker` (with time) | RFC3339 | change | |
| `select` | `Multiselect` (`:multiple=false`, `:creatable=false`) | option value `string` | select | options from `field.options`; `:multiple` switches single vs multi in one component |
| `multiselect` | `Multiselect` (`:multiple=true`, `:creatable=false`) | `string[]` of option values | add/remove | same component, `:multiple` flag |
| `checkbox` | `FancyCheckbox` | `boolean` | toggle | |
| `url` | `FormInput` (`type=url`) | URL string | blur | no dedicated URL component — `FormInput` is the precedent |
| any `is_api_only` | the type's component, `:disabled` | display-only | — | the value is shown, the input is not editable (AC#5) |

**`value: null`** renders as an empty input (no synthesized default — S3's no-default-on-read
policy; `field_config.default` is metadata the consumer may optionally use, not a
server-side fallback). `Multiselect` for both single and multi (the user's decision) keeps
one code path, matching how `EditAssignees`/`EditLabels` both wrap `Multiselect`.

## When the section shows (AC#6)

After the backend prerequisite (§The backend prerequisite), the values map is non-empty
iff the project has assigned custom fields (every assigned field appears, `value: null`
or not). So:

- `setActiveFields()` flips `activeFields.customFields` based on whether the map is
  non-empty — the **attachments** precedent (`v-show="activeFields.attachments ||
  hasAttachments"`), not the priority/labels precedent (hidden until toggled). Custom
  fields are centrally governed; the user cannot add or remove them, so a manual toggle
  adds nothing. **No action button** (the user's decision).
- No assigned fields → map `{}` → `activeFields.customFields` stays `false` → no section,
  no visual change from stock Vikunja (AC#6). Same `v-if="activeFields.<field>"` +
  `setActiveFields()` pattern every native section uses.

`'customFields'` is added to the `FieldType` union and the `activeFields` reactive map in
`TaskDetailView.vue`, the same registration as every other field.

## Authorization & API-only

- Inputs are `:disabled="!canWrite"` — a read-only user sees values without editing, same
  as priority/due-date on a task they can't write. `canWrite` is the existing
  `TaskDetailView` computed (passed to every native field's `:disabled`).
- `is_api_only` → `:disabled="!canWrite || field.field_config.is_api_only"` — the value is
  displayed but not UI-editable (AC#5). The API still reads/writes it (S3); the flag is
  the UI's display-only signal.

## Error handling

A failed upsert (400 — bad date, option not in the field's options, number out of
`min`/`max`) surfaces via the toast/error helpers `TaskDetailView` already uses for
`saveTask` (`error(...)` from `@/message`). On error, revert the input to the stored
value (the store was updated on success only). Exact inline-error placement is left to
implementation (the story defers it: "errors are shown, but exact placement is left to
implementation").

## i18n

Only structural strings need keys — the section label and empty/error toasts. Field
names, option labels, and descriptions come from the API (`field.name`,
`field.options[].label`, `field.description`) — data-driven, not translated. Add to
`frontend/src/i18n/lang/en.json` under the existing `task` namespace (e.g.
`task.attributes.customFields` for the section label, plus toast strings), mirroring the
existing `task.attributes.assignees` / `task.detail.updateSuccess` pair. Only `en.json`
is edited; other locales flow through the dedicated translation workflow (per the repo
rule — do not translate into other languages directly).

## Testing

### Vitest (`pnpm test:unit`, vikunja frontend) — primary automated unit coverage

Mock `CustomFieldService`; test `CustomFields.vue` + the store actions:
- Non-empty map → one row per entry, sorted by `display_order`.
- Per-type rendering: one test per type → the right component + binding (number `min`/
  `max` from `field_config`, select options from `field.options`, date, checkbox, url).
- `saveCustomFieldValue` calls the upsert `POST` (not `taskStore.update`); on success
  the local map updates.
- `clearCustomFieldValue` calls the per-field `DELETE`; the key is removed.
- Discard on unmount (AC#4): type into a text field, unmount without blur → no upsert
  called.
- `is_api_only` field renders its value with the input `:disabled` (AC#5).
- Empty map → `CustomFields` renders nothing (AC#6).

No plugin needed for the Vitest suite (the service is mocked).

### Instance-based headed browser (`./scripts/run-test-env.sh` → `http://127.0.0.1:4176`)

The real end-to-end surface. `compose.test.override.yml` (`build.context: ../vikunja`)
builds the API from this fork and mounts the plugin live (`compose.test.yml:7`,
`./:/app/vikunja/plugins/custom-fields`); `config.test.yml` enables the yaegi loader. The
built image embeds the fork frontend (the override's `watch` rebuilds on
`../vikunja/frontend` changes), so a browser at `:4176` hits the **real task detail view
talking to the real plugin endpoint**. Covers all 7 ACs with no extra wiring.

`run-test-env.sh` already seeds a custom field definition (integer "Priority") + a task
(Step 5b, `scripts/run-test-env.sh:103-127`). **Extend Step 5b to seed the type variety**
(text, a select-with-options, date, checkbox, url, + an `is_api_only` field) so the
browser renders each type — a harness-modification point per the plugin repo's
`CLAUDE.md`.

### `mage test:e2e` wiring (throwaway, documented in the plugin repo) — in scope for S5

The throwaway wiring that brings the sibling plugin into the vikunja repo's Playwright
harness — e2e config pointed at `../vikunja-custom-fields-plugin` (mirroring how
`compose.test.override.yml` already points at `../vikunja`) — plus a short `docs/` note in
the plugin repo recording the wiring so it is reusable later. Throwaway = tied to the
local relative-path setup, not robust. Playwright automation of the custom-fields UI on
top of the instance-based surface.

### Out of scope (deferred) — robust portable end-to-end

A robust, portable end-to-end solution that does not require recreating the exact local
machine (a self-contained e2e image / CI-grade harness). Future project.

### Acceptance-criteria verification

| AC | How verified |
|---|---|
| 1. Fields show for a project with assigned fields | Vitest (non-empty map → a row per field) + headed browser (fields render on the seeded task after the §backend prerequisite). |
| 2. Per-type input renders based on type | Vitest (one test per type → right component + binding) + headed browser (each seeded type renders the right control). |
| 3. Editing persists through the custom-fields API, not the task body | Vitest (spy: upsert `POST` called, `taskStore.update` not) + headed browser (edit a value, confirm persisted via the values endpoint / `sqlite3 db/vikunja.db`). |
| 4. Change + navigate without saving discards | Vitest (type → unmount without blur → no upsert called). |
| 5. API-only fields display but are not editable | Vitest (`is_api_only` → input `:disabled`) + headed browser. |
| 6. Projects without custom fields show no section | Vitest (empty map → `CustomFields` renders nothing) + headed browser (a project with no assigned fields shows no custom-field section). |
| 7. No visual distinction from native fields | Headed browser (same `.column`/`.detail-title`/`CustomTransition` rhythm as Priority/Due Date) — a visual check, not automated. |

## Git workflow

Per the plugin repo's `CLAUDE.local.md`: **git-flow mandatory** (not substitutable — never
`git branch` + `git checkout` for `git flow feature start`). The plugin read-path fix
lives on a `feature/` branch in the plugin repo (`vikunja-custom-fields-plugin`);
the frontend changes live on a `feature/` branch in the vikunja fork
(`vikunja/`, base `cf-main` per its `CLAUDE.local.md`). Conventional Commits. The spec +
plan are committed to the plugin repo under `docs/superpowers/vikunja-fork/{specs,plans}/`
(per `CLAUDE.local.md`).

**The backend prerequisite** (the `readValuesForTask` fix) is a small, read-path-only
change to unreleased code — it lands first, on the plugin feature branch, and is
verified via the test instance before the frontend work depends on it.

The implementation plan (next step, via the writing-plans skill) details the
branch/commit structure across both repos and includes **Required Skills + Recommended
Skills lists per task** (per the plugin repo's `CLAUDE.local.md`). Relevant skills
include the local `designing-with-upstream-precedent` skill (the discipline this spec
followed) and the `golang-*` skills for the plugin read-path fix.

## Out of scope

- Quick Add Magic support for custom fields (per the PRD).
- Custom fields in list/kanban/table/gantt views — task detail only.
- The management UI for field definitions — S9.
- Exact validation-error placement (errors are shown; exact placement is implementation).
- Filtering or searching tasks by custom field value.
- The robust, portable end-to-end solution (a self-contained e2e image / CI-grade
  harness) — deferred to a future project.
- Inlining values into the native task `GET`/`PUT`/`PATCH` body — ruled out by S3 AC#1
  (the native task response can't be augmented under yaegi); the dedicated values
  endpoint is the lasting surface.
- Default synthesis on read — `field_config.default` is metadata; the consumer may
  optionally use it.
