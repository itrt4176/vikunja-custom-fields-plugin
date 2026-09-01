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
component bound `v-model` + a commit handler that calls `saveTask()` →
`taskStore.update(currentTask)` (`stores/tasks.ts:185`). **The commit event varies by
component** — `@update:modelValue="saveTask()"` for some (e.g. reminders, repeat-after),
`@closeOnChange="saveTask()"` for the date pickers (`TaskDetailView.vue:138,193,227`), and
`@update:modelValue="setPriority"`/`"setPercentDone"` (wrappers that call `saveTask`) for
the selects. **Native fields auto-save on change — there is no modal-level Save/Cancel;
every field commit persists immediately.** (The type-specific commit events for custom
fields are in the Field-type → input table.)

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
  templates (`getRouteReplacements`/`getReplacedRoute`, `abstractService.ts:159-190`) and uses
  `AuthenticatedHTTPFactory()` (called in the constructor, `abstractService.ts:73`) whose baseURL is `/api/v1`
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
  `taskId`; on each add/remove they call the store action. (`EditLabels` emits
  `update:modelValue` on both add and remove; `EditAssignees` mutates the local ref on
  remove without emitting — the precedent that matters for `CustomFields.vue` is the
  store-action + local-state pattern, not emit symmetry.) Both wrap the generic
  `Multiselect.vue` (`frontend/src/components/input/Multiselect.vue`, `:multiple`
  true/false).

### Reusable input components (the type → input map is built from these)

| Purpose | File |
|---|---|
| Date / datetime picker | `frontend/src/components/input/Datepicker.vue` |
| Single- or multi-select (tag-style) | `frontend/src/components/input/Multiselect.vue` (`:multiple`, `searchResults`, `@select`, `#tag`/`#searchResult` slots) |
| Native select over an options prop/slot | `frontend/src/components/input/FormSelect.vue` (native `<select>` over an `options` prop or default slot); `tasks/partials/PrioritySelect.vue`, `PercentDoneSelect.vue` (tiny native `<select>` over a local constant) |
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
type/service/component/store split (types stand in for a model here — see
No `models/customField.ts` below); existing files modified are
`TaskDetailView.vue` (add the section), `Datepicker.vue` + `DatepickerInline.vue` (the
`withTime` prop — fork core), and `en.json` (structural strings).

```
frontend/src/
├── modelTypes/
│   └── ICustomField.ts                 NEW — the {value, field} entry + CustomFieldDefinition shape + the map type (snake_case matching the API — no model; see below)
├── services/
│   └── customField.ts                  NEW — CustomFieldService (bulkUpsert bypasses the snake_case interceptor; see service layer)
├── stores/
│   └── (useTaskStore, extended)         MODIFIED — loadCustomFields/saveCustomFieldValue/clearCustomFieldValue actions
├── components/input/
│   ├── Datepicker.vue                  MODIFIED (fork core) — withTime prop (default true), passed to DatepickerInline
│   └── DatepickerInline.vue            MODIFIED (fork core) — withTime prop → enableTime/dateFormat; formatDateToFlatpickrString; setDate
├── components/tasks/partials/
│   └── CustomFields.vue                NEW — the data-driven section; one row per map entry, switch on field.type
├── views/tasks/
│   └── TaskDetailView.vue              MODIFIED — 'customFields' in FieldType + activeFields; load + render the section
└── i18n/lang/
    └── en.json                         MODIFIED — task.attributes.customFields + empty/error toasts (structural strings only)
```

### No `models/customField.ts` (deliberate omission)

The file layout deliberately has **no `models/CustomFieldModel`**. `AbstractModel.assignData`
(`frontend/src/models/abstractModel.ts:17`) does `Object.assign(this, omitBy(data, isNil))` — it drops
`nil`/`undefined` values. The custom-field values map uses `value: null` as a **meaningful** value (unset,
or invalid per the current definition — the S3 read-path policy). Routing the map through a model would
`omitBy(isNil)`-drop every `value: null` entry, so a task with assigned-but-unset fields would render empty
inputs for the wrong reason (the value was dropped by the model, not absent from the API). The service
(§`services/customField.ts`) returns the raw `data` typed as `CustomFieldValuesMap` (the types use snake_case
matching the API verbatim — `field_config`, `display_order`, `project_ids`, `is_api_only`), bypassing
`AbstractService` and the `AbstractModel` transform entirely, so `value: null` is preserved end-to-end. No
model class is needed or wanted; the types alone are the contract. (This supersedes the file-layout entry that
appeared in earlier drafts of this spec.)

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

### `services/customField.ts` (NEW) — the bulk-POST path follows the `bulkCreate` precedent

The service must send the bulk upsert as a **bare-array POST**, which `AbstractService`'s
default `create`/`update` plumbing does not do safely. Two verified problems with the
default path (`abstractService.ts`, read this session):

1. **Verb mismatch.** `create()` sends **PUT** (`abstractService.ts:390`,
   `this.http.put(finalUrl, model)`); the plugin registers the bulk upsert on **POST only**
   (`main.go:1679`, `g.POST("/custom-fields/tasks/:task/custom-fields",
   bulkUpsertHandler)` — no PUT route on the collection). Using `create:` → PUT to a
   POST-only route → no match.
2. **Array mangling.** The request interceptor calls `objectToSnakeCase(config.data)`
   (`abstractService.ts:86-92`) for both the 'put' and 'post' paths. `objectToSnakeCase`
   (`frontend/src/helpers/case.ts:45`) iterates `Object.keys(object)` — on a bare array
   `[{…},{…}]` that yields `["0","1",…]`, producing `{"0":{…},"1":{…}}`, not an array. The
   plugin decodes the body as `[]valueItem` (`json.NewDecoder(...).Decode(&items)`,
   `main.go:1381`) and would 400 on the object. (The array-element branch in
   `objectToSnakeCase` does map over arrays correctly, but it is only reached for *values*
   of object keys, not for a top-level array argument.)

`TaskService` works around both problems for its own `bulkCreate` (`task.ts:180`) by using
a **fresh `AuthenticatedHTTPFactory().post(...)`** that bypasses the shared interceptors
entirely (`task.ts:211`), and by setting `autoTransformBeforePost() → false` (`task.ts:48`).
`CustomFieldService` mirrors that precedent:

```ts
import {AuthenticatedHTTPFactory} from '@/helpers/fetcher'
import AbstractService from '@/services/abstractService'

export default class CustomFieldService extends AbstractService<CustomFieldValueItem> {
	constructor() {
		super({
			// get's route param is {taskId} — the call must pass {taskId}, not {id}
			// (getRouteReplacements reads parameters['taskId']; abstractService.ts:159-184).
			get: '/plugins/custom-fields/tasks/{taskId}/custom-fields',
			delete: '/plugins/custom-fields/tasks/{taskId}/custom-fields/{fieldId}',
		})
	}

	// GET — bypasses AbstractService.getM, which mutates the response with
	// result.maxPermission = Number(headers['x-max-permission']) (NaN — the
	// plugin sets no such header, main.go:1239). Direct
	// AuthenticatedHTTPFactory().get() returns the bare map unchanged.
	async getValues(taskId: number): Promise<CustomFieldValuesMap> {
		const {data} = await AuthenticatedHTTPFactory().get(
			`/plugins/custom-fields/tasks/${taskId}/custom-fields`,
		)
		return data as CustomFieldValuesMap
	}

	// The bulk upsert — NOT create (PUT) and NOT the default update (which runs
	// objectToSnakeCase on the body, mangling a bare array into an object). Direct
	// AuthenticatedHTTPFactory().post() bypasses the shared interceptors, so the
	// bare-array body is sent unchanged. Mirror TaskService.bulkCreate (task.ts:211).
	async bulkUpsert(taskId: number, items: CustomFieldValueItem[]): Promise<CustomFieldValuesMap> {
		// Relative path — AuthenticatedHTTPFactory() pins baseURL to /api/v1/
		// (fetcher.ts: HTTPFactory → getApiBaseUrl → window.API_URL, default
		// '/api/v1/'). An absolute `/api/v1/...` path would double-prefix to
		// /api/v1/api/v1/... and 404 (axios combineURLs). TaskService.bulkCreate
		// uses apiV2Url() (an absolute URL via new URL(...).toString()) for its
		// v2 endpoint; the v1 plugin endpoint uses a relative path that combines
		// with the v1 baseURL — same as the get/delete routes above.
		const {data} = await AuthenticatedHTTPFactory().post(
			`/plugins/custom-fields/tasks/${taskId}/custom-fields`,
			items, // bare array — the plugin decodes []valueItem
		)
		return data as CustomFieldValuesMap // bulkUpsertHandler returns readValuesForTask (the whole map)
	}
}
```

**DELETE uses the inherited `AbstractService.delete`** (route-param substitution via
`getReplacedRoute`, no body, no `getM` mutation). **GET does NOT use the inherited
`AbstractService.get`** — it delegates to `getM` (`abstractService.ts:305`), which does
`result.maxPermission = Number(response.headers['x-max-permission'])` (`:314`). The plugin's
`listValuesHandler` returns `c.JSON(http.StatusOK, out)` with **no `x-max-permission`
header** (`main.go:1239`), so `Number(undefined)` = `NaN`; `result` (the default
`modelGetFactory` passthrough, `:227`) is the response map, so the map gains an enumerable
`maxPermission: NaN` key. That breaks AC#6 (a task with no assigned fields gets
`{maxPermission: NaN}`, `Object.keys(...).length === 1` → the section shows for a project
with no custom fields) **and** crashes rendering (`CustomFields.vue` iterates entries and
reads `entry.field`; `NaN.field` → `undefined.display_order` → TypeError). Overriding
`modelGetFactory` does not fix it — `getM` sets `.maxPermission` on whatever
`modelGetFactory` returns (`:314` after `:313`). So **GET uses a direct
`AuthenticatedHTTPFactory().get()`** (the same bypass pattern as `bulkUpsert`) — the
`getValues` method above.

The **route param is `{taskId}`** (for the inherited `delete`), so the call passes
`{taskId}`, not `{id}` — verified against `getRouteReplacements` (`abstractService.ts:159`,
`parameters[parameter]` where `parameter` is the `{taskId}` captured group). Passing
`{id: taskId}` → no `taskId` key → route `…/tasks/undefined/custom-fields` →
`strconv.ParseInt` 400 (`main.go:1221`).

**The bulk POST returns the full map** — `bulkUpsertHandler` ends with
`readValuesForTask(s, taskID)` (`main.go`), so the store action **replaces the entire
`customFieldValues[taskId]`** from the response, not a single field (see The store & data
flow). The per-field `POST`/`PUT`/`DELETE` remain available to external API consumers (S9
management UI, API clients) but the modal's save path uses `bulkUpsert` only.

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

1. **Separate fetch.** `loadCustomFields(taskId)` calls `customFieldsService.getValues(taskId)`
   (a direct `AuthenticatedHTTPFactory().get()` — see `services/customField.ts`; the
   route is a relative path, so no `{taskId}` model is needed) → stashes the returned map in a `customFieldValues:
   Ref<Record<ITask['id'], CustomFieldValuesMap>>` (keyed by taskId, analogous to how
   `tasks` is keyed by id at `stores/tasks.ts:139`). Called in `TaskDetailView`'s
   `watch(taskId)` **alongside** `taskService.get(...)`. This is the "extra round trip"
   S3's AC#1 amendment budgeted; it is frontend↔backend, invisible to the end user.

2. **No kanban sync (the one forced divergence).** `addAssignee`/`removeLabel` sync the
   kanban copy because `assignees`/`labels` *are* fields on the kanban `ITask`
   (`t.task.assignees`). Custom-field values are **not** on `ITask` (S3 AC#1) — there is
   no kanban copy to sync. The actions own the `customFieldValues` ref instead. This is
   the only point where custom fields break the assignee/label mold, and it is forced by
   the S3 amendment, not a design choice. Upstream conversion: when values become a
   `xorm:"-"` field on `models.Task`, the actions gain the kanban sync and the
   `customFieldValues` ref collapses into `task.customFields`.

3. **Save (immediate commit — the user's decision).** `saveCustomFieldValue({taskId,
   fieldId, value})` → `customFieldsService.bulkUpsert(taskId, [{custom_field_definition_id:
   fieldId, value}])` (one item; the bulk upsert handles create+update, sidestepping the
   per-field `POST`-409 / `PUT`-404 dance) → **on success, replace the entire
   `customFieldValues[taskId]`** with the response (the bulk POST returns
   `readValuesForTask` — the whole map, not the single value; verified at
   `bulkUpsertHandler` in `main.go`). This mirrors `addAssignee`'s "HTTP + update local"
   shape and the native-field `@update:modelValue="saveTask()"` immediate-commit pattern.
   The per-field `POST`/`PUT`/`DELETE` remain available to external API consumers (S9
   management UI, API clients) but are not the modal's save path.

4. **Clear (the user's decision).** `clearCustomFieldValue({taskId, fieldId})` →
   per-field `DELETE /.../custom-fields/{fieldId}` → on success, remove the key from
   `customFieldValues[taskId]` (or reload — the row is gone either way). Mirrors
   `removeAssignee` (`stores/tasks.ts:274`). The row is gone; the next read returns
   `value: null`. **The "upsert `null`" alternative is not viable, not merely less clean:**
   `writeValue` → `validateValue` rejects a `nil`/`null` raw value for every type (the
   validation expects a string/number/bool/array, not null; `main.go`'s `validateValue`) →
   400. `DELETE` is the only working clear path. (A `null` value could only be stored by
   writing the empty string for a non-required field — a different semantics, "set to
   empty," not "unset.")

   **Save-vs-clear routing (applies to every type).** The commit handler routes by the
   *new value*: empty (`null`, empty string, empty array) → `clearCustomFieldValue`
   (DELETE); non-empty → `saveCustomFieldValue` (upsert). This covers the
   **`Datepicker` clear-to-null** case a review surfaced — clearing a date sets the
   bound value to `null` (`Datepicker.vue:74` emits `Date | null`), `@closeOnChange` fires,
   and the handler routes to `clearCustomFieldValue` (not `saveCustomFieldValue`, which
   would upsert `null` → 400). For `checkbox`, `false` is a real value (not empty) →
   upsert, not clear. For `select`/`multiselect`, removing the last selected option empties
   the array → clear. For text/number/url, emptying the field → clear.

5. **Discard on navigate (AC#4).** Under immediate commit, an *in-progress* text/number
   edit (typed but not committed) is local to the input; navigating away discards it
   because the upsert never fired. Discrete edits (select/date/checkbox) commit immediately
   on change, so there is nothing to discard. **The commit event is type-specific, not
   uniformly "blur"** — see the Field-type → input table: text/number/url commit on blur
   (via a local-ref + `@blur` handler, because `FormInput` emits `update:modelValue`
   per-keystroke), select/multiselect commit on select/remove, date/datetime commit on
   `closeOnChange`, checkbox on toggle. This satisfies AC#4 and AC#7 ("same interaction
   patterns as native fields" — native fields auto-save on change with no modal-level
   Save/Cancel).

6. **Optimistic vs on-success.** Update the store on success, not optimistically — the
   input holds the local value until commit, then snaps to the confirmed value. Avoids
   rollback complexity on a failed upsert (a 400 for a bad date/out-of-range number
   reverts the input to the stored value).

### `CustomFields.vue` (NEW — the data-driven section)

Iterates `taskStore.customFieldValues[taskId]` (sorted by `field.display_order`), one
row per entry, each in the same `div.column` + `.detail-title` + `CustomTransition`
rhythm as Priority/Due Date. Each row `switch`es on `field.type` to pick the input
component (§Field-type → input map) and calls `taskStore.saveCustomFieldValue` /
`clearCustomFieldValue` on the type-specific commit event. `:disabled="!canWrite ||
field.field_config.is_api_only"`. No per-type hardcoded files — the type set is runtime
data from the definition.

**The commit wiring is type-specific (verified against the components' actual
emit/handlers):**

- **`FormInput`-based types (text/integer/decimal/url)** emit `update:modelValue` on every
  keystroke (`FormInput.vue` `@input="handleInput"` → `emit('update:modelValue', …)`). To
  commit on **blur** (not per-keystroke), `CustomFields.vue` holds a **local ref** per
  field, binds it with `v-model` to the input, and commits the ref via `@blur` →
  `saveCustomFieldValue`. `FormInput` exposes `focus()` + a value getter
  (`defineExpose`, `FormInput.vue`), so the parent can hold a local ref and commit on
  blur. This is the blur-commit mechanism AC#4 rests on; it is a *new* pattern for the
  modal (no native field uses `FormInput` — they are all `PrioritySelect`/
  `PercentDoneSelect`/`Datepicker`/`EditAssignees`/`EditLabels`), so it is documented as a
  CustomFields-local pattern, not "the same as native fields."
- **`Datepicker`** emits `closeOnChange` (and `update:modelValue` with a `Date`,
  `Datepicker.vue:76,103`) — commit on `@closeOnChange` (the event `TaskDetailView`
  already uses for dueDate/startDate/endDate, `TaskDetailView.vue:138`). The emitted value
  is a `Date` object, not a string — `CustomFields.vue` formats it to the type's wire
  format on commit (date-only → `YYYY-MM-DD`; datetime → RFC3339). See the `withTime`
  prop below.
- **`Multiselect`** binds/emits the **whole selected object**, not the value string
  (`Multiselect.vue` `select()` sets `internalValue.value = object` and emits it).
  `CustomFields.vue` passes `field.options` as `searchResults` (the options are the
  searchable entries) and extracts `.value` from the selected object in `@select` /
  `@remove` → `saveCustomFieldValue`/`clearCustomFieldValue` with the **option value
  string** (for `select`) or the array of value strings (for `multiselect`). The
  `#tag`/`#searchResult` slots display the option's `label`; the stored identity is the
  value string.
- **`FancyCheckbox`** toggles → commit on `@update:modelValue`.

## Field-type → input mapping (data-driven, reconciled with the components' real behavior)

| `field.type` | component | bound value | commit event | notes |
|---|---|---|---|---|
| `text` | `FormInput` | local ref `string` | `@blur` | local ref + `@blur` (FormInput emits per-keystroke); commit the ref |
| `textarea` | `AsyncEditor` (TipTap) | local ref `string` | `@blur` | **stores HTML, not plain text** — the value is rich-text markup; the `textarea` type name is multi-line text but the stored value is HTML (see Field-type notes) |
| `integer` | `FormInput` (`type=number`, `step=1`) | local ref `number` | `@blur` | `min`/`max` from `field_config`; commit the ref |
| `decimal` | `FormInput` (`type=number`, `step=any`) | local ref `number` | `@blur` | `min`/`max` from `field_config`; commit the ref |
| `date` | `Datepicker` (`:withTime=false`) | `Date` (emitted) → format to `YYYY-MM-DD` on commit | `@closeOnChange` | **`withTime` prop (NEW, fork core)** — see Date-only rendering below |
| `datetime` | `Datepicker` (`:withTime=true`, default) | `Date` → format to RFC3339 on commit | `@closeOnChange` | the existing time-on behavior |
| `select` | `Multiselect` (`:multiple=false`, `:creatable=false`) | whole object → extract `.value` | `@select` / `@remove` | `searchResults` = `field.options`; store the option value string |
| `multiselect` | `Multiselect` (`:multiple=true`, `:creatable=false`) | `string[]` of option values | `@select` / `@remove` | same component, `:multiple` flag; extract `.value` per entry |
| `checkbox` | `FancyCheckbox` | `boolean` | `@update:modelValue` (toggle) | |
| `url` | `FormInput` (`type=url`) | local ref `string` | `@blur` | local ref + `@blur`; no dedicated URL component |
| any `is_api_only` | the type's component, `:disabled` | display-only | — | the value is shown, the input is not editable (AC#5) |

**`value: null`** renders as an empty input (no synthesized default — S3's no-default-on-read
policy; `field_config.default` is metadata the consumer may optionally use, not a
server-side fallback). `Multiselect` for both single and multi (the user's decision) keeps
one code path, matching how `EditAssignees`/`EditLabels` both wrap `Multiselect`.

### Date-only rendering — `withTime` prop on `Datepicker` (NEW, fork core; the user's decision)

The existing `Datepicker` (`Datepicker.vue`) wraps `DatepickerInline.vue`, which hardcodes
`enableTime: true` + `dateFormat: 'Y-m-d H:i'` (`DatepickerInline.vue:124,129`) and emits a
`Date` (`:217`). There is **no date-only mode**. A `date`-type field rendered with the
time-on `Datepicker` would show a time picker the user shouldn't use, and the emitted
`Date` formatted as RFC3339 would be rejected by the API's
`time.Parse("2006-01-02", …)` (`main.go`'s date validation). A native `<input type=date>` was
considered and rejected — it would be the only browser-native date control in the app (every
Vikunja date field uses `Datepicker`), "sticking out like a sore thumb."

**Add a `withTime` prop (default `true`) to `Datepicker` + `DatepickerInline`, threaded
into `flatPickerConfig`** (`enableTime: props.withTime`, `dateFormat: props.withTime ?
'Y-m-d H:i' : 'Y-m-d'`), `formatDateToFlatpickrString` (drop the time part when
`!withTime`), and `setDate` (skip `getDateWithTime` when date-only). The shortcut buttons
(today/tomorrow/etc.) are date-only concepts and work unchanged. `CustomFields.vue` passes
`:withTime=false` for `date`, default (`true`) for `datetime`.

**Upstream-acceptable because it generalizes rather than forks:** every existing call site
(dueDate/startDate/endDate) is unchanged (`withTime` defaults to `true`); the prop adds
date-only capability to the component the codebase already uses for every date field; it is
backwards-compatible by construction. This is the kind of change that upstreams as a single
self-contained PR (ship the prop; native custom fields use it). Recorded as a **fork-core
touch** — `Datepicker.vue` + `DatepickerInline.vue` are modified in the fork, not the plugin.

### Field-type notes (precision)

- **`textarea` stores HTML, not plain text.** `AsyncEditor` is the TipTap rich-text editor
  (the Description field's editor); its value is HTML markup, stored in a `text` column and
  validated as a plain string (`main.go`). The `textarea` type name suggests multi-line
  plain text, but the rendered value is rich text — a UX nuance to confirm during
  implementation (plain `<textarea>` vs TipTap). If plain multi-line text is wanted, a
  native `<textarea>` (not `AsyncEditor`) is the match; if rich text is acceptable, `AsyncEditor`
  is the match. Left as an implementation decision, flagged here so it is not assumed away.
- **Clearing is `DELETE`-only.** Upserting `null` is rejected by `validateValue` for every
  type (null is not a string/number/bool/array) → 400. `DELETE` is the only working clear
  path (see The store & data flow #4).

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
`min`/`max`) surfaces via a toast from `error(...)` imported from `@/message`
(`TaskDetailView.vue:716` imports only `success` from `@/message` — `error` is **not** an
existing `TaskDetailView` precedent; it is imported new for custom fields). On error,
revert the input to the stored value (the store was updated on success only). Exact
inline-error placement is left to implementation (the story defers it: "errors are shown,
but exact placement is left to implementation").

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
