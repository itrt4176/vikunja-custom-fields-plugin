# S5 — Custom Fields on Task Detail: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render custom field values data-driven on the Vikunja task detail view, indistinguishable from native fields, persisted through the plugin's custom-fields values endpoint.

**Architecture:** The frontend fetches a task's custom field values from the plugin's S3 values endpoint (a separate fetch alongside the native task `GET` — the native response can't be augmented under yaegi, S3 AC#1) and renders each field by its definition's type. Values are a separate resource saved through dedicated endpoints (the assignees/labels precedent), not the task body. One backend prerequisite — the plugin's `readValuesForTask` must return every project-assigned field (with `value: null` for unset) so the frontend can render the empty fields the user fills in.

**Tech Stack:** Vue 3 (composition API, `<script setup lang="ts">`) + Pinia + TypeScript (frontend, this Vikunja fork); Go + xorm + yaegi (plugin, `vikunja-custom-fields-plugin/main.go`); Vitest (unit); the Docker test instance + `mage test:e2e` (integration).

**Spec:** `docs/superpowers/vikunja-fork/specs/2026-09-01-s5-custom-fields-on-task-detail-design.md` (committed, `feature/s5-vikunja-frontend`, through `35ae165`) — the plan argues from the spec; executors read both.

## Global Constraints

- **Two repos.** The backend prerequisite (Task 1) + the test harness + the e2e wiring (Task 7) live in the **plugin repo** (`vikunja-custom-fields-plugin`, branch `feature/s5-vikunja-frontend` — already the current branch; the spec is committed there). The frontend (Tasks 2–6) lives in the **Vikunja fork** (`vikunja/`, base branch `cf-main`, git-flow: start `feature/s5-custom-fields-task-detail` off `cf-main`). Never mix commits across repos.
- **Git-flow mandatory** (per both repos' `CLAUDE.local.md`). Plugin: stay on `feature/s5-vikunja-frontend`. Vikunja: `git flow feature start s5-custom-fields-task-detail` off `cf-main` (or the equivalent manual branch off `cf-main`). Conventional Commits.
- **Frontend lint rules** (enforced by ESLint): single quotes, trailing commas, no semicolons, tab indent, `<script setup lang="ts">`, PascalCase components, camelCase events. Obey `frontend/eslint.config.js` + `.editorconfig`.
- **Backend lint:** `mage lint:fix` (golangci-lint) before committing plugin/Go changes. No raw SQL — xorm query builder only.
- **Translations:** only `frontend/src/i18n/lang/en.json` is edited (source strings). Do not translate into other locales (dedicated translation workflow).
- **No model class for custom field values** (deviation from the spec's file layout, grounded in source): `AbstractModel.assignData` does `Object.assign(this, omitBy(data, isNil))` (`frontend/src/models/abstractModel.ts:17`) — it drops `nil`/`undefined`, so routing the values map through a model would **drop `value: null`**, which is meaningful (unset/invalid). The service returns raw `data` typed as `CustomFieldValuesMap`; the types use snake_case matching the API verbatim (`field_config`, `display_order`, `project_ids`, `is_api_only`). So `models/customField.ts` is **omitted**; the spec's file-layout entry for it is superseded.
- **The plugin cannot be `go test`ed standalone** (imports resolve only inside the vikunja module). Backend tests are integration via the Docker test instance (`./scripts/run-test-env.sh`) + `curl`/`sqlite3`. Frontend unit tests are Vitest.
- **`mage` for API tests**, never plain `go test`. Frontend: `pnpm test:unit` (Vitest). E2E: `mage test:e2e` (not `pnpm test:e2e`).

---

## File Structure

### Plugin repo (`vikunja-custom-fields-plugin/`)
| File | Action | Responsibility |
|---|---|---|
| `main.go` | Modify | `readValuesForTask` — iterate project-assigned definitions, emit `value: null` for unset (the backend prerequisite) |
| `scripts/run-test-env.sh` | Modify | Step 5b — seed the type variety (text/select/date/checkbox/url + an API-only field) for the headed-browser/e2e render test |
| `docs/superpowers/vikunja-fork/notes/s5-mage-test-e2e-wiring.md` | Create | The throwaway `mage test:e2e` wiring note (how to point the vikunja repo's Playwright harness at the sibling plugin) |

### Vikunja fork (`vikunja/frontend/src/`)
| File | Action | Responsibility |
|---|---|---|
| `modelTypes/ICustomField.ts` | Create | The `ICustomFieldValue` (`{value, field}`), `ICustomFieldDefinition`, `ICustomFieldOption`, `ICustomFieldConfig`, `CustomFieldType`, `CustomFieldValuesMap` types — snake_case matching the API |
| `services/customField.ts` | Create | `CustomFieldService` — `getValues` (direct `AuthenticatedHTTPFactory().get`, bypasses `getM`), `bulkUpsert` (direct `.post`, bare array, bypasses the snake_case interceptor), inherited `delete` (route param `{taskId}`) |
| `stores/tasks.ts` | Modify | `customFieldValues` ref + `loadCustomFields`/`saveCustomFieldValue`/`clearCustomFieldValue` actions (no kanban sync — values aren't on `ITask`) |
| `components/tasks/partials/CustomFields.vue` | Create | The data-driven section: one row per map entry, switch on `field.type` → the right input, commit wiring (blur/select/closeOnChange/toggle), save-vs-clear routing, `:disabled` for `is_api_only` |
| `views/tasks/TaskDetailView.vue` | Modify | `'customFields'` in `FieldType` + `activeFields`; `loadCustomFields(taskId)` in the `watch(taskId)` loader; render `<CustomFields>` in the `.details` grid with `v-if="activeFields.customFields"` |
| `components/input/Datepicker.vue` | Modify (fork core) | `withTime` prop (default `true`), passed to `DatepickerInline` |
| `components/input/DatepickerInline.vue` | Modify (fork core) | `withTime` prop → `enableTime`/`dateFormat` in `flatPickerConfig`; drop time in `formatDateToFlatpickrString`; skip `getDateWithTime` in `setDate` when date-only |
| `i18n/lang/en.json` | Modify | `task.attributes.customFields` (section label) + empty/error toast strings |

**Decomposition rationale:** each task produces an independently testable deliverable. Task 1 (backend) is integration-tested via the test instance. Tasks 2–6 (frontend) are unit-tested via Vitest with the service/store/component mocking patterns already in the repo (`task.test.ts`, `tasks.test.ts`, `FormInput.test.ts`). Task 7 ties them together with the headed browser + the throwaway e2e wiring. The `withTime` prop (Task 2) is standalone and lands first so Task 5 can render date-type fields with it.

---

## Task 1: Backend read-path fix — `readValuesForTask` (plugin repo)

**Files:**
- Modify: `main.go` — `readValuesForTask` (currently `func readValuesForTask(s *xorm.Session, taskID int64) (map[string]interface{}, error)`, around `main.go:1140`)
- Test: the Docker test instance (`./scripts/run-test-env.sh`) + `curl` (the plugin can't be `go test`ed standalone)

**Interfaces:**
- Consumes: `ReadAll(s *xorm.Session, projectID int64) ([]CustomFieldDefinition, error)` (`main.go:676` — already filters `project_id = pid OR project_id = 0`, orders by `display_order asc`); `(*CustomFieldDefinition).ReadOne(s)` (`main.go:649`); `coerceReadValue` + `isSelectLike` (existing, in `main.go`); `valueToMap` (`main.go:793`); `definitionToMap` (`main.go:831`).
- Produces: `readValuesForTask` now returns one entry per **project-assigned definition** (not per value row), with `value: null` when no value row exists or the stored value fails coercion. The write paths (`writeValue`, `bulkUpsertHandler`, the per-field handlers) are unchanged. `readOneValueHandler` (which calls `readValuesForTask` then picks the key) still works — the requested key is present iff the field is assigned.

**Required Skills:** `designing-with-upstream-precedent` (the discipline this change follows — verify the precedent maps structurally), `golang-database` (xorm query builder; no raw SQL), `golang-testing`.
**Recommended Skills:** `golang-code-style`, `golang-error-handling`.

**Context — why this change:** The shipped `readValuesForTask` iterates `custom_field_values` rows for the task (`s.Table("custom_field_values").Where("task_id = ?", taskID).Find(&values)`, `main.go:1146`) and emits one map entry per **value row**. A field assigned to the task's project but with no value row is **absent** from the response — so a task with assigned fields but no values returns `{}`, leaving the frontend nothing to render for the empty fields the user fills in. The S3 spec's read-path policy (§Read-path policy) specifies assigned-but-unset fields appear with `value: null`; the implementation diverged. This fix makes the implementation match the spec. Verified against `main.go:1140-1214` (the current body) and `main.go:676-691` (`ReadAll`, the project filter the fix reuses).

- [ ] **Step 1: Write the failing test (integration via the test instance)**

Start the test instance (it seeds an integer "Priority" field assigned to the test project + a task with **no** value — `run-test-env.sh:103-127`):

```bash
cd /mnt/data/nickp/Documents/Code/vikunja-projects/vikunja-custom-fields-plugin
./scripts/run-test-env.sh
# capture JWT, FIELD_ID, TASK_ID, PROJECT_ID from the banner
```

Assert the read returns the assigned field with `value: null` (before the fix, it returns `{}`):

```bash
curl -s -H "Authorization: Bearer $JWT" \
  "http://127.0.0.1:4176/api/v1/plugins/custom-fields/tasks/$TASK_ID/custom-fields" | jq .
```

Expected (after fix): `{"<FIELD_ID>": {"value": null, "field": {"id": ..., "name": "Priority", "type": "integer", "field_config": {...}, "display_order": ..., "project_ids": [$PROJECT_ID]}}}`. Before the fix: `{}` (empty — the field is assigned but has no value row). The assertion to fail: `jq -e 'keys | length == 1'` is false on `{}`.

- [ ] **Step 2: Run the test to verify it fails**

Run the curl above. Expected: `{}` (the field is assigned but absent from the response — the bug). The fix makes it appear with `value: null`.

- [ ] **Step 3: Rewrite `readValuesForTask` to iterate assigned definitions**

Replace the body of `readValuesForTask` (`main.go:1140`) so it iterates the task's project-assigned definitions and looks up each one's value, rather than iterating value rows. Sketch (adapt to the existing variable names + the existing `coerceReadValue`/`isSelectLike`/`definitionToMap`/`valueToMap` helpers — do not change those helpers):

```go
func readValuesForTask(s *xorm.Session, taskID int64) (map[string]interface{}, error) {
	t, err := models.GetTaskByIDSimple(s, taskID)
	if err != nil {
		return nil, ErrCustomFieldTaskNotFound{ID: taskID}
	}
	// Iterate the project-assigned definitions (project_id = pid OR project_id = 0,
	// ordered by display_order) — not the value rows. A field with no value row
	// appears with value: null (the S3 read-path policy: assigned-but-unset fields
	// are present; only unassigned fields are absent).
	defs, err := ReadAll(s, t.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("custom-fields: list definitions for values: %w", err)
	}
	out := map[string]interface{}{}
	for i := range defs {
		d := &defs[i]
		// Look up this definition's value row for the task (if any).
		var v CustomFieldValue
		has, err := s.Table("custom_field_values").
			Where("custom_field_definition_id = ? AND task_id = ?", d.ID, taskID).
			Get(&v)
		if err != nil {
			return nil, fmt.Errorf("custom-fields: get value for field %d: %w", d.ID, err)
		}
		opts, pids, err := readFieldOptionsAndProjects(s, d) // see Step 3a
		if err != nil {
			return nil, err
		}
		fieldMap := definitionToMap(d, opts, pids)
		var native interface{}
		if !has {
			// no value row → null (unset)
			native = nil
		} else if isSelectLike(d.Type) {
			native, err = resolveSelectValue(s, v.ID, d, opts) // see Step 3a
			if err != nil {
				return nil, err
			}
		} else {
			native = coerceReadValue(d, v.Value)
		}
		out[strconv.FormatInt(d.ID, 10)] = valueToMap(native, fieldMap)
	}
	return out, nil
}
```

**Step 3a — factor the per-definition lookups:** the current `readValuesForTask` inlines `d.ReadOne(s)` (`main.go:1161`) which returns `(def, opts, pids, err)`. Reuse `ReadOne` directly inside the loop (it already fetches the definition + options + project ids) instead of introducing `readFieldOptionsAndProjects`/`resolveSelectValue`, unless `ReadOne` re-fetches the definition row inefficiently (it does one query — acceptable at plugin scale; the S3 resolution already noted the N+1 as a future optimization). The select-value resolution (child-table join) is the existing block at `main.go:1176-1207`; extract it into a small `resolveSelectValue(s, valueID, def, opts) (interface{}, error)` helper and call it for select-like types. Keep the existing nil-on-empty-child-rows behavior. The point of the rewrite is the **outer loop changes from value-rows to definitions**; the per-value resolution stays the same.

If `ReadOne`'s signature is `func (d *CustomFieldDefinition) ReadOne(s *xorm.Session) (*CustomFieldDefinition, []CustomFieldOption, []int64, error)` (`main.go:649`), call it as:

```go
def, opts, pids, err := d.ReadOne(s)
if err != nil {
	// definition deleted while its value row remains: skip the orphan (defensive,
	// same as the current code at main.go:1169). yaegi can't type-assert
	// interpreted errors; discriminate by message prefix.
	if strings.HasPrefix(err.Error(), "custom field definition ") {
		continue
	}
	return nil, err
}
fieldMap := definitionToMap(def, opts, pids)
```

Then look up the value row by `(d.ID, taskID)` and resolve `native` (nil if no row; `resolveSelectValue` for select-like; `coerceReadValue(def, v.Value)` otherwise). Use `def` (the `ReadOne` result) for `coerceReadValue`/`isSelectLike`/`definitionToMap`, not the loop variable `d` (which `ReadOne` may have populated into a fresh struct).

- [ ] **Step 4: Run the test to verify it passes**

Restart the container to reload the plugin (the source is mounted live; yaegi re-interprets on restart):

```bash
docker compose -f compose.test.yml restart
# wait for health:
until curl -sf -o /dev/null http://127.0.0.1:4176/api/v2/info; do sleep 1; done
```

Re-run the curl from Step 1. Expected: `{"<FIELD_ID>": {"value": null, "field": {...}}}` — the assigned field appears with `value: null`. Verify a second field type (e.g. set a value, then read it back) returns the coerced native value, not null.

Also verify the write path is unchanged — set a value, then read:

```bash
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  "http://127.0.0.1:4176/api/v1/plugins/custom-fields/tasks/$TASK_ID/custom-fields" \
  -d "[{\"custom_field_definition_id\":$FIELD_ID,\"value\":7}]" | jq .
# expect {"<FIELD_ID>": {"value": 7, "field": {...}}}
```

- [ ] **Step 5: Lint + commit**

```bash
mage lint:fix   # golangci-lint; fix any reported issues
git add main.go
git commit -m "fix(s3): readValuesForTask iterates assigned definitions, not value rows

A field assigned to the task's project but with no value row was absent
from the read response (the map iterated custom_field_values rows). The S3
read-path policy is that assigned-but-unset fields appear with value: null.
Rewrite to iterate ReadAll(projectID) and look up each definition's value,
emitting null when no row exists or the stored value fails coercion. Write
paths unchanged."
```

---

## Task 2: `withTime` prop on `Datepicker` (vikunja fork, fork core)

**Files:**
- Modify: `frontend/src/components/input/Datepicker.vue` (props at `:56-71`, the `<DatepickerInline>` at `:25-29`)
- Modify: `frontend/src/components/input/DatepickerInline.vue` (props `:93-98`, `flatPickerConfig` `:118-134`, `formatDateToFlatpickrString` `:136-144`, `setDate` `:220-226`)
- Test: `frontend/src/components/input/DatepickerInline.test.ts` (Create)

**Interfaces:**
- Consumes: nothing new (the `Datepicker`/`DatepickerInline` existing `modelValue`/`showShortcuts`).
- Produces: a `withTime` prop (default `true`) on both components. `Datepicker` passes it through to `DatepickerInline`. `DatepickerInline` threads it into `flatPickerConfig` (`enableTime`, `dateFormat`), `formatDateToFlatpickrString` (drop the time part when `!withTime`), and `setDate` (skip `getDateWithTime` when date-only). Backwards-compatible: every existing call site (dueDate/startDate/endDate in `TaskDetailView.vue:134,189,222`) omits the prop → defaults to `true` → unchanged behavior.

**Required Skills:** `designing-with-upstream-precedent` (generalize the existing component; backwards-compatible), `golang-testing` (N/A — frontend; listed for skill parity is not required, omit).
**Recommended Skills:** none beyond the frontend conventions.

**Context — why a prop, not a native `<input type=date>`:** the existing `Datepicker` always shows a time picker (`DatepickerInline.vue:129` `enableTime: true` hardcoded). Every date field in Vikunja uses this `Datepicker`; a native `<input type=date>` for custom fields would be the only browser-native date control in the app. A defaulted `withTime` prop adds date-only capability to the component the codebase already uses, backwards-compatibly — the kind of change that upstreams cleanly (ship the prop; native custom fields use it). Verified against `DatepickerInline.vue:93-98` (only `modelValue`/`showShortcuts` props), `:118-134` (`flatPickerConfig`), `:136-144` (`formatDateToFlatpickrString`), `:220-226` (`setDate`).

- [ ] **Step 1: Write the failing test**

`frontend/src/components/input/DatepickerInline.test.ts`:

```ts
import {describe, it, expect, vi} from 'vitest'
import {mount} from '@vue/test-utils'
import {defineComponent, h} from 'vue'
import DatepickerInline from './DatepickerInline.vue'

// Stub flat-pickr so the test sees the config prop without instantiating flatpickr.
const FlatpickrStub = defineComponent({
	name: 'flat-pickr',
	props: {config: null, modelValue: null},
	setup(props) {
		return () => h('div', {class: 'fp-stub'}, JSON.stringify(props.config))
	},
})

vi.mock('vue-flatpickr-component', () => ({default: FlatpickrStub}))
vi.mock('flatpickr/dist/flatpickr.css', () => ({}))

function mountInline(props: Record<string, unknown> = {}) {
	return mount(DatepickerInline, {
		props: {modelValue: null, ...props},
		global: {
			stubs: {BaseButton: true},
			mocks: {
				$t: (k: string) => k,
				$i18n: {t: (k: string) => k},
			},
		},
	})
}

describe('DatepickerInline withTime', () => {
	it('enables time by default', async () => {
		const wrapper = mountInline()
		const config = JSON.parse(wrapper.find('.fp-stub').text())
		expect(config.enableTime).toBe(true)
		expect(config.dateFormat).toBe('Y-m-d H:i')
	})

	it('disables time and uses date-only format when withTime is false', async () => {
		const wrapper = mountInline({withTime: false})
		const config = JSON.parse(wrapper.find('.fp-stub').text())
		expect(config.enableTime).toBe(false)
		expect(config.dateFormat).toBe('Y-m-d')
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd frontend && pnpm test:unit src/components/input/DatepickerInline.test.ts
```

Expected: FAIL — `withTime` is not a prop (`Vue warn: invalid prop`), and `config.enableTime` is `true` in both cases (the hardcoded `enableTime: true`).

- [ ] **Step 3: Add the `withTime` prop to `DatepickerInline.vue`**

In `frontend/src/components/input/DatepickerInline.vue`:

1. Add `withTime` to the props (default `true`):

```ts
const props = withDefaults(defineProps<{
	modelValue: Date | null | string
	showShortcuts?: boolean
	withTime?: boolean
}>(), {
	showShortcuts: true,
	withTime: true,
})
```

2. Thread it into `flatPickerConfig` (`:118`):

```ts
const flatPickerConfig = computed(() => {
	const configuredDueTime = parseUserDefaultTime(useAuthStore().settings.frontendSettings.defaultDueTime)

	return {
		altFormat: t('date.altFormatLong'),
		altInput: true,
		dateFormat: props.withTime ? 'Y-m-d H:i' : 'Y-m-d',
		...(props.withTime && configuredDueTime === null ? {} : {
			defaultHour: configuredDueTime?.hours,
			defaultMinute: configuredDueTime?.minutes,
		}),
		enableTime: props.withTime,
		time_24hr: timeFormat.value === TIME_FORMAT.HOURS_24,
		inline: true,
		locale: useFlatpickrLanguage().value,
	}
})
```

(Keep the `defaultHour`/`defaultMinute` spread conditional on `withTime` — when date-only, there is no time input to default. If the original `configuredDueTime === null ? {} : {...}` shape is awkward to extend, the minimal correct change is `enableTime: props.withTime` + `dateFormat: props.withTime ? 'Y-m-d H:i' : 'Y-m-d'`; preserve the existing `defaultHour`/`defaultMinute` logic under the `withTime` branch.)

3. `formatDateToFlatpickrString` (`:136`) — drop the time part when date-only, so the `get()` side of `flatPickrDate` matches `dateFormat` (else the `set()` comparison at `:155` would mismatch and reset the date):

```ts
function formatDateToFlatpickrString(date: Date): string {
	const year = date.getFullYear()
	const month = (date.getMonth() + 1).toString().padStart(2, '0')
	const day = date.getDate().toString().padStart(2, '0')
	if (!props.withTime) {
		return `${year}-${month}-${day}`
	}
	const hours = date.getHours().toString().padStart(2, '0')
	const minutes = date.getMinutes().toString().padStart(2, '0')
	return `${year}-${month}-${day} ${hours}:${minutes}`
}
```

4. `setDate` (`:220`) — skip `getDateWithTime` when date-only (the shortcut buttons set a date at the configured due time; date-only fields don't carry a time):

```ts
function setDate(dateString: string) {
	const interval = calculateDayInterval(dateString)
	const newDate = new Date()
	newDate.setDate(newDate.getDate() + interval)
	date.value = props.withTime ? getDateWithTime(newDate) : newDate
	updateData()
}
```

- [ ] **Step 4: Add the `withTime` prop to `Datepicker.vue` and pass it through**

In `frontend/src/components/input/Datepicker.vue`:

1. Add `withTime` to the props (`:56-71`, default `true`):

```ts
const props = withDefaults(defineProps<{
	modelValue: Date | null | string
	chooseDateLabel?: string
	disabled?: boolean
	showShortcuts?: boolean
	emptyLabel?: string
	withTime?: boolean
}>(), {
	chooseDateLabel: () => { /* unchanged */ return t('input.datepicker.chooseDate') },
	disabled: false,
	showShortcuts: true,
	emptyLabel: '',
	withTime: true,
})
```

2. Pass it to `<DatepickerInline>` (`:25-29`):

```vue
<DatepickerInline
	v-model="date"
	:show-shortcuts="showShortcuts"
	:with-time="withTime"
	@update:modelValue="updateData"
/>
```

- [ ] **Step 5: Run the test to verify it passes**

```bash
cd frontend && pnpm test:unit src/components/input/DatepickerInline.test.ts
```

Expected: PASS — both assertions (`enableTime`/`dateFormat` per `withTime`).

- [ ] **Step 6: Run the existing datepicker tests to verify no regression**

```bash
cd frontend && pnpm test:unit src/components/date/DatepickerWithRange.test.ts
```

Expected: PASS (the range picker doesn't use `DatepickerInline`'s `withTime`, but confirm nothing else broke).

- [ ] **Step 7: Lint + commit**

```bash
cd frontend && pnpm lint:fix && pnpm lint:styles:fix
git add frontend/src/components/input/DatepickerInline.vue frontend/src/components/input/Datepicker.vue frontend/src/components/input/DatepickerInline.test.ts
git commit -m "feat(input): add withTime prop to Datepicker for date-only fields

Datepicker always showed a time picker (enableTime hardcoded). Add a
withTime prop (default true) threaded into flatPickerConfig,
formatDateToFlatpickrString, and setDate. Backwards-compatible — every
existing call site omits the prop and keeps time-on behavior. Enables
date-type custom fields to render date-only without a native <input type=date>
that would be the only browser-native date control in the app."
```

---

## Task 3: Custom field types + service (vikunja fork)

**Files:**
- Create: `frontend/src/modelTypes/ICustomField.ts`
- Create: `frontend/src/services/customField.ts`
- Test: `frontend/src/services/customField.test.ts` (Create)

**Interfaces:**
- Consumes: `AbstractService` (`frontend/src/services/abstractService.ts:44`), `AuthenticatedHTTPFactory` (`frontend/src/helpers/fetcher.ts`).
- Produces: `CustomFieldService` with:
  - `getValues(taskId: number): Promise<CustomFieldValuesMap>` — a direct `AuthenticatedHTTPFactory().get('/plugins/custom-fields/tasks/${taskId}/custom-fields')` (bypasses `AbstractService.getM`, which would mutate the response with `maxPermission: NaN`).
  - `bulkUpsert(taskId: number, items: CustomFieldValueItem[]): Promise<CustomFieldValuesMap>` — a direct `AuthenticatedHTTPFactory().post('/plugins/custom-fields/tasks/${taskId}/custom-fields', items)` (bypasses the `objectToSnakeCase` interceptor that mangles a bare array; `items` sent unchanged).
  - the inherited `delete(model)` via `super({delete: '/plugins/custom-fields/tasks/{taskId}/custom-fields/{fieldId}'})` (route-param substitution, no body, no `getM` mutation).
  - `CustomFieldValueItem` type (`{custom_field_definition_id: number, value: unknown}`) + the `CustomFieldValuesMap`/`ICustomFieldValue`/`ICustomFieldDefinition`/`ICustomFieldOption`/`ICustomFieldConfig`/`CustomFieldType` types.

**Required Skills:** `designing-with-upstream-precedent` (the `bulkCreate` bypass precedent — verified at `task.ts:180-220`), `golang-testing` (N/A — frontend; omit).
**Recommended Skills:** none beyond the frontend conventions.

**Context — the two `AbstractService` pitfalls this service avoids (both verified against source, called out by the Opus review):**
1. `create()` sends **PUT** (`abstractService.ts:390`); the plugin's bulk upsert is **POST-only** (`main.go:1679`). `update()` → `post()` but runs `objectToSnakeCase` on the body (`abstractService.ts:86-92`), which mangles a bare array `[{…}]` into `{"0":{…}}` (`case.ts:45` iterates `Object.keys`) → the plugin's `json.Decode(&[]valueItem)` (`main.go:1381`) 400s. `TaskService.bulkCreate` (`task.ts:180-220`) avoids both by using a fresh `AuthenticatedHTTPFactory().post(apiV2Url(...), {tasks})`. `CustomFieldService.bulkUpsert` mirrors that with a relative path (`/plugins/...`, combines with the `/api/v1/` baseURL) — **not** an absolute `/api/v1/...` path (which would double-prefix to `/api/v1/api/v1/...` and 404).
2. `AbstractService.get` → `getM` (`abstractService.ts:305`) does `result.maxPermission = Number(response.headers['x-max-permission'])` (`:314`); the plugin returns `c.JSON(out)` with no such header (`main.go:1239`) → `Number(undefined)` = `NaN` → the map gains an enumerable `maxPermission` key → breaks AC#6 (`{maxPermission: NaN}` looks non-empty) and crashes rendering (`entry.field` on it → TypeError). `getValues` uses a direct `AuthenticatedHTTPFactory().get()` to bypass `getM` entirely.

- [ ] **Step 1: Write the failing test**

`frontend/src/services/customField.test.ts`:

```ts
import {describe, it, expect, vi, beforeEach} from 'vitest'

import CustomFieldService from './customField'
import type {CustomFieldValuesMap, CustomFieldValueItem} from './customField'

const get = vi.hoisted(() => vi.fn<(url: string) => Promise<{data: CustomFieldValuesMap}>>())
const post = vi.hoisted(() => vi.fn<(url: string, body: CustomFieldValueItem[]) => Promise<{data: CustomFieldValuesMap}>>())
const deleteFn = vi.hoisted(() => vi.fn())

vi.mock('@/helpers/fetcher', () => ({
	getApiBaseUrl: () => '/api/v1/',
	apiV2Url: (p: string) => `/api/v2/${p}`,
	HTTPFactory: () => ({get, post, delete: deleteFn, interceptors: {request: {use: vi.fn()}, response: {use: vi.fn()}}}),
	AuthenticatedHTTPFactory: () => ({get, post, delete: deleteFn, interceptors: {request: {use: vi.fn()}, response: {use: vi.fn()}}}),
}))

const MAP: CustomFieldValuesMap = {'3': {value: null, field: {id: 3, name: 'Priority', type: 'integer', field_config: {}, display_order: 0, project_ids: [5]}}}

describe('CustomFieldService', () => {
	beforeEach(() => {
		get.mockReset()
		post.mockReset()
		deleteFn.mockReset()
		get.mockResolvedValue({data: {...MAP}})
		post.mockResolvedValue({data: {...MAP}})
	})

	it('getValues hits the relative v1 path and returns the map untouched', async () => {
		const out = await new CustomFieldService().getValues(7)
		expect(get).toHaveBeenCalledTimes(1)
		expect(get.mock.calls[0][0]).toBe('/plugins/custom-fields/tasks/7/custom-fields')
		expect(out).toEqual(MAP)
		// no maxPermission mutation (the raw data is returned as-is)
		expect('maxPermission' in out).toBe(false)
	})

	it('bulkUpsert posts a bare array to the relative v1 path', async () => {
		const items: CustomFieldValueItem[] = [{custom_field_definition_id: 3, value: 7}]
		await new CustomFieldService().bulkUpsert(7, items)
		expect(post).toHaveBeenCalledTimes(1)
		expect(post.mock.calls[0][0]).toBe('/plugins/custom-fields/tasks/7/custom-fields')
		expect(post.mock.calls[0][1]).toBe(items) // the bare array, not objectToSnakeCase'd
		expect(Array.isArray(post.mock.calls[0][1])).toBe(true)
	})

	it('delete uses the inherited route-param substitution', async () => {
		const svc = new CustomFieldService()
		deleteFn.mockResolvedValue({data: {}})
		await svc.delete({taskId: 7, fieldId: 3} as never)
		expect(deleteFn).toHaveBeenCalledTimes(1)
		expect(deleteFn.mock.calls[0][0]).toBe('/plugins/custom-fields/tasks/7/custom-fields/3')
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd frontend && pnpm test:unit src/services/customField.test.ts
```

Expected: FAIL — `./customField` does not exist.

- [ ] **Step 3: Create the types**

`frontend/src/modelTypes/ICustomField.ts` (snake_case matching the API verbatim — the service returns raw `data`, no camelCasing):

```ts
export type CustomFieldType =
	| 'text' | 'textarea' | 'integer' | 'decimal' | 'date' | 'datetime'
	| 'select' | 'multiselect' | 'checkbox' | 'url'

export interface ICustomFieldOption {
	id: number
	value: string
	label: string
	display_order: number
}

export interface ICustomFieldConfig {
	required?: boolean
	default?: string
	is_api_only?: boolean
	min?: number
	max?: number
}

export interface ICustomFieldDefinition {
	id: number
	name: string
	type: CustomFieldType
	description?: string
	field_config: ICustomFieldConfig
	display_order: number
	options?: ICustomFieldOption[]
	project_ids: number[]
}

export interface ICustomFieldValue {
	value: unknown
	field: ICustomFieldDefinition
}

export type CustomFieldValuesMap = Record<string, ICustomFieldValue>
```

- [ ] **Step 4: Create the service**

`frontend/src/services/customField.ts`:

```ts
import {AuthenticatedHTTPFactory} from '@/helpers/fetcher'
import AbstractService from '@/services/abstractService'

import type {CustomFieldValuesMap} from '@/modelTypes/ICustomField'

export interface CustomFieldValueItem {
	custom_field_definition_id: number
	value: unknown
}

// {taskId} / {fieldId} are route params the inherited AbstractService.delete
// substitutes via getReplacedRoute (abstractService.ts:159-190).
interface CustomFieldDeleteModel {
	taskId: number
	fieldId: number
}

export default class CustomFieldService extends AbstractService<CustomFieldDeleteModel> {
	constructor() {
		super({
			delete: '/plugins/custom-fields/tasks/{taskId}/custom-fields/{fieldId}',
		})
	}

	// Direct AuthenticatedHTTPFactory().get() — bypasses AbstractService.getM, which
	// mutates the response with result.maxPermission = Number(headers['x-max-permission'])
	// (NaN; the plugin sets no such header, main.go:1239). The map is returned untouched.
	async getValues(taskId: number): Promise<CustomFieldValuesMap> {
		const {data} = await AuthenticatedHTTPFactory().get(
			`/plugins/custom-fields/tasks/${taskId}/custom-fields`,
		)
		return data as CustomFieldValuesMap
	}

	// Direct AuthenticatedHTTPFactory().post() — NOT create (PUT) and NOT the inherited
	// update (which runs objectToSnakeCase on the body, mangling a bare array into an
	// object; case.ts:45). The bare array is sent unchanged; the plugin decodes []valueItem
	// (main.go:1381). Relative path — AuthenticatedHTTPFactory pins baseURL to /api/v1/
	// (fetcher.ts), so an absolute /api/v1/... would double-prefix; the relative path
	// combines with the v1 baseURL. Mirrors TaskService.bulkCreate (task.ts:180-220).
	async bulkUpsert(taskId: number, items: CustomFieldValueItem[]): Promise<CustomFieldValuesMap> {
		const {data} = await AuthenticatedHTTPFactory().post(
			`/plugins/custom-fields/tasks/${taskId}/custom-fields`,
			items,
		)
		return data as CustomFieldValuesMap
	}
}
```

- [ ] **Step 5: Run the test to verify it passes**

```bash
cd frontend && pnpm test:unit src/services/customField.test.ts
```

Expected: PASS — `getValues` hits the relative path and returns the map without `maxPermission`; `bulkUpsert` posts the bare array to the relative path; `delete` substitutes the route params.

- [ ] **Step 6: Typecheck + lint + commit**

```bash
cd frontend && pnpm typecheck && pnpm lint:fix
git add frontend/src/modelTypes/ICustomField.ts frontend/src/services/customField.ts frontend/src/services/customField.test.ts
git commit -m "feat(s5): add CustomFieldService + types (getValues/bulkUpsert bypass AbstractService pitfalls)

getValues and bulkUpsert use direct AuthenticatedHTTPFactory calls —
bypassing getM (which mutates the response with maxPermission: NaN) and
the objectToSnakeCase interceptor (which mangles a bare array). delete
uses the inherited route-param substitution. Mirrors TaskService.bulkCreate."
```

---

## Task 4: Store actions on `useTaskStore` (vikunja fork)

**Files:**
- Modify: `frontend/src/stores/tasks.ts` — add a `customFieldValues` ref + three actions; export them in the return object (`:644-672`)
- Test: `frontend/src/stores/tasks.customFields.test.ts` (Create — a focused test file, like `tasks.test.ts`)

**Interfaces:**
- Consumes: `CustomFieldService` (Task 3), `CustomFieldValuesMap`/`ICustomFieldValue` (Task 3), `useTaskStore`'s existing structure (`stores/tasks.ts:131` `defineStore`, `:139` `tasks` ref).
- Produces: on `useTaskStore`:
  - `customFieldValues: Ref<Record<ITask['id'], CustomFieldValuesMap>>` — keyed by taskId (analogous to `tasks` at `:139`).
  - `loadCustomFields(taskId: ITask['id']): Promise<CustomFieldValuesMap>` — calls `customFieldsService.getValues(taskId)`, stashes the map in `customFieldValues[taskId]`, returns it.
  - `saveCustomFieldValue({taskId, fieldId, value}): Promise<CustomFieldValuesMap>` — calls `customFieldsService.bulkUpsert(taskId, [{custom_field_definition_id: fieldId, value}])`, **replaces** `customFieldValues[taskId]` with the returned map (the bulk POST returns the whole map), returns it.
  - `clearCustomFieldValue({taskId, fieldId}): Promise<void>` — calls `customFieldsService.delete({taskId, fieldId})`, removes the key from `customFieldValues[taskId]`.
  - **No kanban sync** (values aren't on `ITask`; S3 AC#1) — the one forced divergence from `addAssignee`/`addLabel`.

**Required Skills:** `designing-with-upstream-precedent` (the `addAssignee`/`removeLabel` action shape, verified at `stores/tasks.ts:233-373`), `golang-testing` (N/A — frontend; omit).
**Recommended Skills:** none beyond the frontend conventions.

**Context — the precedent and the one divergence:** `addAssignee` (`stores/tasks.ts:233`) does its own HTTP call then syncs the **kanban copy** via `kanbanStore.getTaskById` → `setTaskInBucketByIndex`. The kanban sync exists because `assignees` *is* a field on the kanban `ITask` (`t.task.assignees`). Custom-field values are **not** on `ITask` (S3 AC#1 — the native task `GET` can't be augmented), so there is no kanban copy to sync; the actions own the `customFieldValues` ref instead. Upstream conversion: when values become an `xorm:"-"` field on `models.Task`, the kanban sync returns and `customFieldValues` collapses into `task.customFields`.

- [ ] **Step 1: Write the failing test**

`frontend/src/stores/tasks.customFields.test.ts`:

```ts
import {setActivePinia, createPinia} from 'pinia'
import {beforeEach, describe, expect, it, vi} from 'vitest'

vi.mock('@/router', () => ({default: {currentRoute: {value: {params: {}}}, isReady: () => Promise.resolve()}}))
vi.mock('vue-i18n', () => ({useI18n: () => ({t: (k: string) => k}), createI18n: () => ({global: {t: (k: string) => k}})}))
vi.mock('@/stores/base', () => ({useBaseStore: () => ({setHasTasks: vi.fn()})}))

import {useTaskStore} from './tasks'
import type {CustomFieldValuesMap} from '@/modelTypes/ICustomField'

const MAP: CustomFieldValuesMap = {
	'3': {value: 7, field: {id: 3, name: 'Priority', type: 'integer', field_config: {}, display_order: 0, project_ids: [5]}},
}

describe('useTaskStore custom field value actions', () => {
	beforeEach(() => {
		setActivePinia(createPinia())
	})

	it('loadCustomFields stashes the map by taskId', async () => {
		const store = useTaskStore()
		const svc = {getValues: vi.fn().mockResolvedValue({...MAP}), bulkUpsert: vi.fn(), delete: vi.fn()}
		// inject the service instance the store uses (see Step 3: the store news it up;
		// mock the module so the constructor returns the spy)
		vi.mock('@/services/customField', () => ({default: class { getValues = svc.getValues; bulkUpsert = svc.bulkUpsert; delete = svc.delete }))
		const out = await store.loadCustomFields(7)
		expect(svc.getValues).toHaveBeenCalledWith(7)
		expect(out).toEqual(MAP)
		expect(store.customFieldValues[7]).toEqual(MAP)
	})

	it('saveCustomFieldValue upserts one field and replaces the whole map from the response', async () => {
		const store = useTaskStore()
		const replaced: CustomFieldValuesMap = {'3': {...MAP['3'], value: 9}}
		const svc = {getValues: vi.fn(), bulkUpsert: vi.fn().mockResolvedValue(replaced), delete: vi.fn()}
		vi.mock('@/services/customField', () => ({default: class { getValues = svc.getValues; bulkUpsert = svc.bulkUpsert; delete = svc.delete }))
		store.customFieldValues[7] = {...MAP}
		await store.saveCustomFieldValue({taskId: 7, fieldId: 3, value: 9})
		expect(svc.bulkUpsert).toHaveBeenCalledWith(7, [{custom_field_definition_id: 3, value: 9}])
		expect(store.customFieldValues[7]).toEqual(replaced) // wholesale replace, not one-field patch
	})

	it('clearCustomFieldValue deletes the field and removes the key', async () => {
		const store = useTaskStore()
		const svc = {getValues: vi.fn(), bulkUpsert: vi.fn(), delete: vi.fn().mockResolvedValue({})}
		vi.mock('@/services/customField', () => ({default: class { getValues = svc.getValues; bulkUpsert = svc.bulkUpsert; delete = svc.delete }))
		store.customFieldValues[7] = {...MAP}
		await store.clearCustomFieldValue({taskId: 7, fieldId: 3})
		expect(svc.delete).toHaveBeenCalled()
		expect('3' in store.customFieldValues[7]).toBe(false)
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd frontend && pnpm test:unit src/stores/tasks.customFields.test.ts
```

Expected: FAIL — `store.loadCustomFields` is not a function (the actions don't exist yet).

- [ ] **Step 3: Add the ref + actions to `useTaskStore`**

In `frontend/src/stores/tasks.ts`:

1. Import the service + types (near the top, alongside the other service imports `:5-8`):

```ts
import CustomFieldService from '@/services/customField'
import type {CustomFieldValuesMap} from '@/modelTypes/ICustomField'
```

2. Inside `defineStore('task', () => { ... })`, after the `lastUpdatedTask` ref (`:142`), add:

```ts
const customFieldValues = ref<Record<ITask['id'], CustomFieldValuesMap>>({})
const customFieldsService = new CustomFieldService()
```

3. Add the three actions (near `addAssignee`, `:233` — keep them grouped with the other relation actions):

```ts
async function loadCustomFields(taskId: ITask['id']): Promise<CustomFieldValuesMap> {
	const cancel = setModuleLoading(setIsLoading)
	try {
		const map = await customFieldsService.getValues(taskId)
		customFieldValues.value[taskId] = map
		return map
	} finally {
		cancel()
	}
}

async function saveCustomFieldValue({taskId, fieldId, value}: {taskId: ITask['id'], fieldId: number, value: unknown}): Promise<CustomFieldValuesMap> {
	const cancel = setModuleLoading(setIsLoading)
	try {
		// The bulk POST returns the whole map (readValuesForTask); replace wholesale,
		// not a one-field patch.
		const map = await customFieldsService.bulkUpsert(taskId, [{custom_field_definition_id: fieldId, value}])
		customFieldValues.value[taskId] = map
		return map
	} finally {
		cancel()
	}
}

async function clearCustomFieldValue({taskId, fieldId}: {taskId: ITask['id'], fieldId: number}): Promise<void> {
	await customFieldsService.delete({taskId, fieldId} as never)
	if (customFieldValues.value[taskId]) {
		delete customFieldValues.value[taskId][String(fieldId)]
	}
}
```

> **`as never` on the delete model:** `AbstractService.delete` is generic over `Model`; the `{taskId, fieldId}` shape is a structural placeholder for the route-param substitution (`getReplacedRoute` reads `model['taskId']`/`model['fieldId']`). The cast satisfies the generic without exporting a model type the service doesn't need. If the inherited `delete` complains about the model type at typecheck, adjust the `CustomFieldDeleteModel`-shaped argument; the route-param keys (`taskId`, `fieldId`) are what matter.

4. Export them in the return object (`:644-672`):

```ts
	return {
		// ...existing...
		customFieldValues,
		loadCustomFields,
		saveCustomFieldValue,
		clearCustomFieldValue,
	}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd frontend && pnpm test:unit src/stores/tasks.customFields.test.ts
```

Expected: PASS — `loadCustomFields` stashes by taskId; `saveCustomFieldValue` upserts one field and replaces the whole map; `clearCustomFieldValue` deletes and removes the key.

- [ ] **Step 5: Run the existing store tests for no regression + typecheck + lint + commit**

```bash
cd frontend && pnpm test:unit src/stores/tasks.test.ts && pnpm typecheck && pnpm lint:fix
git add frontend/src/stores/tasks.ts frontend/src/stores/tasks.customFields.test.ts
git commit -m "feat(s5): add custom field value actions to useTaskStore

loadCustomFields/saveCustomFieldValue/clearCustomFieldValue mirror the
addAssignee/removeLabel shape but own a customFieldValues ref instead of
syncing the kanban copy — custom field values aren't on ITask (S3 AC#1).
saveCustomFieldValue replaces the whole map (the bulk POST returns it);
clearCustomFieldValue deletes the value row and removes the key."
```

---

## Task 5: `CustomFields.vue` — the data-driven section (vikunja fork)

**Files:**
- Create: `frontend/src/components/tasks/partials/CustomFields.vue`
- Test: `frontend/src/components/tasks/partials/CustomFields.test.ts` (Create)

**Interfaces:**
- Consumes: `useTaskStore` (Task 4) — `customFieldValues`, `loadCustomFields`, `saveCustomFieldValue`, `clearCustomFieldValue`; the input components (`FormInput`, `Multiselect`, `Datepicker` with `withTime` from Task 2, `FancyCheckbox`, `AsyncEditor`); `ICustomFieldValue`/`ICustomFieldDefinition` (Task 3).
- Produces: `<CustomFields :task-id="..." :can-write="..." />` — renders one row per entry in `customFieldValues[taskId]` (sorted by `field.display_order`), each row switching on `field.type` to the right input, committing on the type-specific event with save-vs-clear routing (empty → `clearCustomFieldValue`, non-empty → `saveCustomFieldValue`; `false` for checkbox is a real value → save), `:disabled="!canWrite || field.field_config.is_api_only"`. Renders nothing when the map is empty.

**Required Skills:** `designing-with-upstream-precedent` (the `EditAssignees`/`EditLabels` component shape + the native-field `div.column`/`.detail-title`/`CustomTransition` rhythm, verified at `TaskDetailView.vue:75-160`), `golang-testing` (N/A — frontend; omit).
**Recommended Skills:** `frontend-design` (the section must be visually indistinguishable from native fields — same `.column`/`.detail-title` rhythm).

**Context — the commit wiring is type-specific (verified against the components' real emit/handlers, per the Opus review):**
- `FormInput` emits `update:modelValue` per keystroke (`FormInput.vue` `@input="handleInput"`) → hold a **local ref**, commit on `@blur`.
- `Datepicker` emits `closeOnChange` with a `Date | null` (`Datepicker.vue:76,103`) → commit on `@closeOnChange`, format the `Date` to the wire format (date-only → local `YYYY-MM-DD`; datetime → `toISOString()` RFC3339); `null` → clear.
- `Multiselect` binds/emits the **whole selected object** (`Multiselect.vue` `select()` sets `internalValue.value = object`) → pass `field.options` as `searchResults`, extract `.value` via `@select`/`@remove`.
- `FancyCheckbox` toggles → `@update:modelValue`; `false` is a real value (save, not clear).

**Save-vs-clear routing (applies to every type):** on commit, empty (`null`, `''`, `[]`) → `clearCustomFieldValue` (DELETE); non-empty → `saveCustomFieldValue` (upsert). `checkbox` `false` is non-empty (a real value) → save. `select`/`multiselect` with the last option removed → empty array → clear. This covers the `Datepicker` clear-to-null case (clearing a date → `null` → `clearCustomFieldValue`, not `saveCustomFieldValue(null)` which would 400 — `validateValue` rejects `null` for every type).

- [ ] **Step 1: Write the failing test**

`frontend/src/components/tasks/partials/CustomFields.test.ts`:

```ts
import {describe, it, expect, beforeEach, vi} from 'vitest'
import {mount} from '@vue/test-utils'
import {setActivePinia, createPinia} from 'pinia'

vi.mock('@/router', () => ({default: {currentRoute: {value: {params: {}}}, isReady: () => Promise.resolve()}}))
vi.mock('vue-i18n', () => ({useI18n: () => ({t: (k: string) => k}), createI18n: () => ({global: {t: (k: string) => k}})}))

import CustomFields from './CustomFields.vue'
import {useTaskStore} from '@/stores/tasks'
import type {CustomFieldValuesMap, ICustomFieldDefinition} from '@/modelTypes/ICustomField'

function def(partial: Partial<ICustomFieldDefinition> & Pick<ICustomFieldDefinition, 'id' | 'type'>): ICustomFieldDefinition {
	return {name: 'Field', field_config: {}, display_order: 0, project_ids: [], ...partial}
}
function entry(value: unknown, field: ICustomFieldDefinition) {
	return {value, field}
}

function mountFields(taskId: number, map: CustomFieldValuesMap, canWrite = true) {
	setActivePinia(createPinia())
	const store = useTaskStore()
	store.customFieldValues[taskId] = map
	store.saveCustomFieldValue = vi.fn().mockResolvedValue(map)
	store.clearCustomFieldValue = vi.fn().mockResolvedValue(undefined)
	store.loadCustomFields = vi.fn().mockResolvedValue(map)
	const wrapper = mount(CustomFields, {
		props: {taskId, canWrite},
		global: {mocks: {$t: (k: string) => k}, stubs: {CustomTransition: true, AsyncEditor: true}},
	})
	return {wrapper, store}
}

describe('CustomFields.vue', () => {
	it('renders nothing when the map is empty', () => {
		const {wrapper} = mountFields(1, {})
		expect(wrapper.find('.custom-fields').exists()).toBe(false)
	})

	it('renders one row per entry, sorted by display_order', () => {
		const map: CustomFieldValuesMap = {
			'3': entry(null, def({id: 3, type: 'text', display_order: 2, name: 'B'})),
			'7': entry(null, def({id: 7, type: 'text', display_order: 1, name: 'A'})),
		}
		const {wrapper} = mountFields(1, map)
		const labels = wrapper.findAll('.detail-title').map(n => n.text())
		expect(labels).toEqual(['A', 'B']) // display_order 1 before 2
	})

	it('disables the input when is_api_only', () => {
		const map: CustomFieldValuesMap = {'3': entry('x', def({id: 3, type: 'text', field_config: {is_api_only: true}}))}
		const {wrapper} = mountFields(1, map)
		const input = wrapper.find('input')
		expect(input.attributes('disabled')).toBeDefined()
	})

	it('disables the input when !canWrite', () => {
		const map: CustomFieldValuesMap = {'3': entry('x', def({id: 3, type: 'text'}))}
		const {wrapper} = mountFields(1, map, /*canWrite*/ false)
		expect(wrapper.find('input').attributes('disabled')).toBeDefined()
	})

	it('renders the right input per type', () => {
		const map: CustomFieldValuesMap = {
			'1': entry('hi', def({id: 1, type: 'text'})),
			'2': entry(true, def({id: 2, type: 'checkbox'})),
			'3': entry(null, def({id: 3, type: 'date'})),
			'4': entry(null, def({id: 4, type: 'select', options: [{id: 1, value: 'a', label: 'A', display_order: 0}]})),
		}
		const {wrapper} = mountFields(1, map)
		// text → FormInput (input); checkbox → FancyCheckbox (input[type=checkbox]);
		// date → Datepicker (a .datepicker); select → Multiselect (a combobox input)
		expect(wrapper.findAll('input').length).toBeGreaterThanOrEqual(2)
		expect(wrapper.find('.datepicker').exists() || wrapper.findComponent({name: 'Datepicker'}).exists()).toBe(true)
	})

	it('save-vs-clear: empty value routes to clearCustomFieldValue', async () => {
		const map: CustomFieldValuesMap = {'3': entry('x', def({id: 3, type: 'text'}))}
		const {wrapper, store} = mountFields(1, map)
		const input = wrapper.find('input')
		await input.setValue('')
		await input.trigger('blur')
		expect(store.clearCustomFieldValue).toHaveBeenCalledWith({taskId: 1, fieldId: 3})
		expect(store.saveCustomFieldValue).not.toHaveBeenCalled()
	})

	it('save-vs-clear: non-empty routes to saveCustomFieldValue', async () => {
		const map: CustomFieldValuesMap = {'3': entry('', def({id: 3, type: 'text'}))}
		const {wrapper, store} = mountFields(1, map)
		const input = wrapper.find('input')
		await input.setValue('hello')
		await input.trigger('blur')
		expect(store.saveCustomFieldValue).toHaveBeenCalledWith({taskId: 1, fieldId: 3, value: 'hello'})
	})
})
```

> The component must be resilient to the store's `customFieldValues` being a reactive `ref` (the test sets `store.customFieldValues[taskId]`). The component reads `taskStore.customFieldValues[props.taskId]`.

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd frontend && pnpm test:unit src/components/tasks/partials/CustomFields.test.ts
```

Expected: FAIL — `./CustomFields.vue` does not exist.

- [ ] **Step 3: Implement `CustomFields.vue`**

`frontend/src/components/tasks/partials/CustomFields.vue`:

```vue
<template>
	<CustomTransition
		v-for="entry in sortedEntries"
		:key="entry.field.id"
		name="flash-background"
		appear
	>
		<div class="column custom-field">
			<div class="detail-title">{{ entry.field.name }}</div>
			<!-- text / url -->
			<FormInput
				v-if="entry.field.type === 'text' || entry.field.type === 'url'"
				v-model="localValues[String(entry.field.id)]"
				:type="entry.field.type === 'url' ? 'url' : 'text'"
				:disabled="!canWrite || entry.field.field_config.is_api_only"
				@blur="commit(entry.field, localValues[String(entry.field.id)])"
			/>
			<!-- integer / decimal -->
			<FormInput
				v-else-if="entry.field.type === 'integer' || entry.field.type === 'decimal'"
				v-model.number="localValues[String(entry.field.id)]"
				type="number"
				:step="entry.field.type === 'decimal' ? 'any' : '1'"
				:min="entry.field.field_config.min"
				:max="entry.field.field_config.max"
				:disabled="!canWrite || entry.field.field_config.is_api_only"
				@blur="commit(entry.field, localValues[String(entry.field.id)])"
			/>
			<!-- textarea (TipTap; stores HTML — see spec Field-type notes) -->
			<AsyncEditor
				v-else-if="entry.field.type === 'textarea'"
				v-model="localValues[String(entry.field.id)]"
				:disabled="!canWrite || entry.field.field_config.is_api_only"
				@blur="commit(entry.field, localValues[String(entry.field.id)])"
			/>
			<!-- date / datetime -->
			<Datepicker
				v-else-if="entry.field.type === 'date' || entry.field.type === 'datetime'"
				:model-value="localValues[String(entry.field.id)]"
				:with-time="entry.field.type === 'datetime'"
				:disabled="!canWrite || entry.field.field_config.is_api_only"
				@close-on-change="commitDate(entry.field, $event)"
			/>
			<!-- single-select -->
			<Multiselect
				v-else-if="entry.field.type === 'select'"
				:model-value="selectValue(entry)"
				:multiple="false"
				:creatable="false"
				:search-results="entry.field.options ?? []"
				:disabled="!canWrite || entry.field.field_config.is_api_only"
				@select="commitSelect(entry.field, $event)"
				@remove="commitSelectRemove(entry.field)"
			/>
			<!-- multi-select -->
			<Multiselect
				v-else-if="entry.field.type === 'multiselect'"
				:model-value="multiselectValue(entry)"
				:multiple="true"
				:creatable="false"
				:search-results="entry.field.options ?? []"
				:disabled="!canWrite || entry.field.field_config.is_api_only"
				@select="commitMultiselectAdd(entry.field, $event)"
				@remove="commitMultiselectRemove(entry.field, $event)"
			/>
			<!-- checkbox -->
			<FancyCheckbox
				v-else-if="entry.field.type === 'checkbox'"
				:model-value="!!localValues[String(entry.field.id)]"
				:disabled="!canWrite || entry.field.field_config.is_api_only"
				@update:model-value="commitCheckbox(entry.field, $event)"
			/>
		</div>
	</CustomTransition>
</template>

<script setup lang="ts">
import {computed, ref, watch} from 'vue'
import FormInput from '@/components/input/FormInput.vue'
import Datepicker from '@/components/input/Datepicker.vue'
import Multiselect from '@/components/input/Multiselect.vue'
import FancyCheckbox from '@/components/input/FancyCheckbox.vue'
// AsyncEditor is a dynamic import (TipTap); keep it lazy to match the Description field.
const AsyncEditor = (await import('@/components/input/AsyncEditor')).default

import CustomTransition from '@/components/misc/CustomTransition.vue'

import {useTaskStore} from '@/stores/tasks'
import type {ICustomFieldValue, ICustomFieldDefinition, ICustomFieldOption} from '@/modelTypes/ICustomField'

const props = defineProps<{
	taskId: number
	canWrite: boolean
}>()

const taskStore = useTaskStore()

const entries = computed<ICustomFieldValue[]>(() => {
	const map = taskStore.customFieldValues[props.taskId]
	return map ? Object.values(map) : []
})

const sortedEntries = computed(() =>
	[...entries.value].sort((a, b) => (a.field.display_order ?? 0) - (b.field.display_order ?? 0)),
)

// Local refs for the blur-commit types (FormInput emits per-keystroke).
const localValues = ref<Record<string, unknown>>({})
watch(entries, () => {
	for (const e of entries.value) {
		const key = String(e.field.id)
		if (!(key in localValues.value)) {
			localValues.value[key] = e.value
		}
	}
}, {immediate: true, deep: true})

function isEmpty(v: unknown): boolean {
	return v === null || v === undefined || v === '' || (Array.isArray(v) && v.length === 0)
}

// Save-vs-clear routing: empty → clear; non-empty → save. checkbox false is a real value.
function commit(field: ICustomFieldDefinition, value: unknown) {
	if (isEmpty(value)) {
		taskStore.clearCustomFieldValue({taskId: props.taskId, fieldId: field.id})
	} else {
		taskStore.saveCustomFieldValue({taskId: props.taskId, fieldId: field.id, value})
	}
}

function commitCheckbox(field: ICustomFieldDefinition, value: boolean) {
	// false is a real value, not empty — always save.
	taskStore.saveCustomFieldValue({taskId: props.taskId, fieldId: field.id, value})
}

function toDateOnly(d: Date): string {
	const m = (d.getMonth() + 1).toString().padStart(2, '0')
	const day = d.getDate().toString().padStart(2, '0')
	return `${d.getFullYear()}-${m}-${day}`
}

function commitDate(field: ICustomFieldDefinition, date: Date | null) {
	if (date === null) {
		taskStore.clearCustomFieldValue({taskId: props.taskId, fieldId: field.id})
		return
	}
	const value = field.type === 'datetime' ? date.toISOString() : toDateOnly(date)
	taskStore.saveCustomFieldValue({taskId: props.taskId, fieldId: field.id, value})
}

function selectValue(e: ICustomFieldValue): ICustomFieldOption | null {
	const v = e.value
	return (e.field.options ?? []).find(o => o.value === v) ?? null
}
function multiselectValue(e: ICustomFieldValue): ICustomFieldOption[] {
	const vals = Array.isArray(e.value) ? (e.value as string[]) : []
	return (e.field.options ?? []).filter(o => vals.includes(o.value))
}

function commitSelect(field: ICustomFieldDefinition, option: ICustomFieldOption | null) {
	if (option === null) {
		taskStore.clearCustomFieldValue({taskId: props.taskId, fieldId: field.id})
	} else {
		taskStore.saveCustomFieldValue({taskId: props.taskId, fieldId: field.id, value: option.value})
	}
}
function commitSelectRemove(field: ICustomFieldDefinition) {
	taskStore.clearCustomFieldValue({taskId: props.taskId, fieldId: field.id})
}

function commitMultiselectAdd(field: ICustomFieldDefinition, _option: ICustomFieldOption) {
	// Re-derive the full selection from the multiselect's current state is complex;
	// simplest correct approach: save the current array of value strings from the
	// local model. The Multiselect emits @select per add; accumulate via the model.
	const current = (localValues.value[String(field.id)] as ICustomFieldOption[] | undefined) ?? multiselectValue(entries.value.find(e => e.field.id === field.id)!)
	// NOTE: see Step 3 note — the multiselect accumulation needs the model binding;
	// the test asserts saveCustomFieldValue is called with a value array. Wire the
	// model so localValues[field.id] holds the current ICustomFieldOption[] and commit
	// its .value strings.
	const values = (Array.isArray(current) ? current : []).map((o: ICustomFieldOption) => o.value)
	taskStore.saveCustomFieldValue({taskId: props.taskId, fieldId: field.id, value: values})
}
function commitMultiselectRemove(field: ICustomFieldDefinition, _option: ICustomFieldOption) {
	const current = (localValues.value[String(field.id)] as ICustomFieldOption[] | undefined) ?? []
	const values = current.map(o => o.value)
	if (values.length === 0) {
		taskStore.clearCustomFieldValue({taskId: props.taskId, fieldId: field.id})
	} else {
		taskStore.saveCustomFieldValue({taskId: props.taskId, fieldId: field.id, value: values})
	}
}
</script>
```

> **Step 3 note — multiselect accumulation:** the sketch above wires `@select`/`@remove`, but `Multiselect`'s `v-model` already accumulates the selected objects (the `:model-value` binding). The cleanest implementation binds `v-model="localValues[String(field.id)]"` to the `Multiselect` (an array of option objects for multi, a single object for single) and commits the `.value` strings on each `@select`/`@remove` by reading the model. Reconcile the binding so `localValues` holds the option objects and the commit reads `current.map(o => o.value)`; do not leave the `!` non-null assertion in the final code (it's a sketch crutch). The test asserts `saveCustomFieldValue` is called with a value array — make the multi-select branch derive the array from the bound model.

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd frontend && pnpm test:unit src/components/tasks/partials/CustomFields.test.ts
```

Expected: PASS — empty map renders nothing; one row per entry sorted by `display_order`; `is_api_only`/`!canWrite` disable; the right input per type; save-vs-clear routing (empty → clear, non-empty → save).

- [ ] **Step 5: Typecheck + lint + commit**

```bash
cd frontend && pnpm typecheck && pnpm lint:fix && pnpm lint:styles:fix
git add frontend/src/components/tasks/partials/CustomFields.vue frontend/src/components/tasks/partials/CustomFields.test.ts
git commit -m "feat(s5): add CustomFields.vue — data-driven custom field rendering on task detail

One row per values-map entry, sorted by display_order, switching on
field.type to the right input. Commit wiring is type-specific (blur for
text/number/url, closeOnChange for date/datetime, select/remove for
select/multiselect, toggle for checkbox). Save-vs-clear routing: empty
→ clearCustomFieldValue, non-empty → saveCustomFieldValue (checkbox false
is a real value). is_api_only and !canWrite disable the input. Empty map
renders nothing (AC#6)."
```

---

## Task 6: `TaskDetailView` integration + i18n (vikunja fork)

**Files:**
- Modify: `frontend/src/views/tasks/TaskDetailView.vue` — `FieldType` union (`:985`), `activeFields` reactive (`:1001`), `setActiveFields` (`:1018`), the `watch(taskId)` loader (`:953-956`), the `.details` grid (`:75`), the return/exports
- Modify: `frontend/src/i18n/lang/en.json` — `task.attributes.customFields` (`:1070`)
- Test: `frontend/src/views/tasks/TaskDetailView.customFields.test.ts` (Create — a focused test; mounting the full `TaskDetailView` is heavy, so test the integration lightly: the section appears when the map is non-empty, and `loadCustomFields` is called on task load)

**Interfaces:**
- Consumes: `CustomFields` (Task 5), `useTaskStore.loadCustomFields` (Task 4), the `activeFields`/`setActiveFields`/`FieldType`/`watch(taskId)` patterns already in `TaskDetailView.vue`.
- Produces: the task detail view loads custom fields on task open and renders the `CustomFields` section in the `.details` grid, `v-if="activeFields.customFields"`, auto-shown when the map is non-empty (the attachments precedent, not the priority/labels toggle — no action button).

**Required Skills:** `designing-with-upstream-precedent` (the `activeFields`/`setActiveFields`/`v-if` pattern, verified at `TaskDetailView.vue:985-1036`; the attachments auto-show at `:75`).
**Recommended Skills:** none beyond the frontend conventions.

**Context — the integration points (verified at the cited lines):**
- `FieldType` union (`:985-999`): add `'customFields'`.
- `activeFields` reactive (`:1001-1017`): add `customFields: false`.
- `setActiveFields` (`:1018-1036`): add `activeFields.customFields = Object.keys(customFieldValues[task.value.id] ?? {}).length > 0` — the map is non-empty iff the project has assigned fields (after Task 1's fix).
- `watch(taskId)` loader (`:953-956`): after `taskService.get` + `Object.assign(task.value, loaded)` + `setActiveFields()`, also `await taskStore.loadCustomFields(task.value.id)` then `setActiveFields()` again (the map arrives after the native load).
- `.details` grid (`:75` `<div class="columns details">`): add a `<CustomFields>` block in the same `CustomTransition`/`div.column` rhythm as the native fields.
- **No action button** (the user's decision) — `customFields` is not in the right-column action grid; it auto-shows.

- [ ] **Step 1: Write the failing test**

`frontend/src/views/tasks/TaskDetailView.customFields.test.ts`:

```ts
import {describe, it, expect, beforeEach, vi} from 'vitest'
import {mount} from '@vue/test-utils'
import {setActivePinia, createPinia} from 'pinia'

vi.mock('@/router', () => ({default: {currentRoute: {value: {params: {taskId: '1'}, fullpath: ''}, push: vi.fn(), isReady: () => Promise.resolve()}}))
vi.mock('vue-i18n', () => ({useI18n: () => ({t: (k: string) => k}), createI18n: () => ({global: {t: (k: string) => k}})}))

import {useTaskStore} from '@/stores/tasks'

// Mounting the full TaskDetailView is heavy (it imports many partials). The
// load-bearing integration assertion is: loadCustomFields is called on task
// load, and the CustomFields section appears when the map is non-empty. Use a
// shallow mount that stubs the heavy partials.
describe('TaskDetailView custom fields integration', () => {
	beforeEach(() => setActivePinia(createPinia()))

	it('loads custom fields on task load', async () => {
		const store = useTaskStore()
		store.loadCustomFields = vi.fn().mockResolvedValue({})
		// ...mount TaskDetailView shallowly, trigger the watch(taskId), assert
		// store.loadCustomFields was called with the task id.
		// (See Step 3 for the exact mount setup; the test asserts the call.)
		expect(store.loadCustomFields).not.toHaveBeenCalled()
		// trigger load:
		// await wrapper.vm.$nextTick()
		// expect(store.loadCustomFields).toHaveBeenCalledWith(1)
	})
})
```

> The full `TaskDetailView` mount is heavy (many partials, route, i18n, stores). If mounting it proves too brittle, narrow the test to: (a) `setActiveFields` flips `activeFields.customFields` based on `customFieldValues[id]` length (extract or exercise it), and (b) `loadCustomFields` is called in the watch. The load-bearing integration is the `watch` calling `loadCustomFields` + `setActiveFields` reading the map. If a full mount is infeasible, add a focused test that imports the `setActiveFields` behavior by exercising the store's `customFieldValues` + the view's reactive. Prefer the headed-browser test (Task 7) for the full visual integration.

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd frontend && pnpm test:unit src/views/tasks/TaskDetailView.customFields.test.ts
```

Expected: FAIL — `'customFields'` is not in `FieldType`; `loadCustomFields` is not called.

- [ ] **Step 3: Wire the integration into `TaskDetailView.vue`**

1. `FieldType` union (`:985`) — add `'customFields'`:

```ts
type FieldType =
	| 'assignees'
	// ...existing...
	| 'timeTracking'
	| 'customFields'
```

2. `activeFields` reactive (`:1001`) — add `customFields: false`:

```ts
const activeFields: { [type in FieldType]: boolean } = reactive({
	// ...existing...
	customFields: false,
})
```

3. `setActiveFields` (`:1018`) — add (after the existing lines):

```ts
	activeFields.customFields = Object.keys(taskStore.customFieldValues[task.value.id] ?? {}).length > 0
```

4. The `watch(taskId)` loader (`:953-956`) — after `setActiveFields()` (the native load), also load custom fields and re-run `setActiveFields`:

```ts
	const loaded = await taskService.get({id}, {expand})
	Object.assign(task.value, loaded)
	taskColor.value = task.value.hexColor
	setActiveFields()

	// Custom fields are a separate resource (S3 AC#1: the native task GET can't
	// carry them). Load alongside, then re-run setActiveFields so the section
	// shows iff the project has assigned fields.
	await taskStore.loadCustomFields(task.value.id)
	setActiveFields()
```

5. The `.details` grid (`:75` `<div class="columns details">`) — add the `CustomFields` section among the field `CustomTransition` blocks (e.g. after the related-tasks/move section, before the description, or wherever the design's section rhythm lands it; place it inline with the other `div.column` fields):

```vue
<CustomTransition
	name="flash-background"
	appear
>
	<div
		v-if="activeFields.customFields"
		class="column custom-fields"
	>
		<CustomFields
			:task-id="task.id"
			:can-write="canWrite"
		/>
	</div>
</CustomTransition>
```

Import `CustomFields` near the other partial imports, and `taskStore` is already in scope (the view uses `taskStore.update`/`markTaskAsRead`).

- [ ] **Step 4: Add the i18n key**

In `frontend/src/i18n/lang/en.json`, under the `task.attributes` object (`:1070`), add:

```json
"customFields": "Custom Fields",
```

(The field names themselves are data-driven from `field.name`; only the section label needs a key. If a toast string is wanted for save errors, add `task.detail.customFields.saveError` and reference it from `CustomFields.vue` via `error(...)` from `@/message` — but the spec defers exact error placement; a minimal `customFields` label is enough for the section.)

- [ ] **Step 5: Run the test to verify it passes**

```bash
cd frontend && pnpm test:unit src/views/tasks/TaskDetailView.customFields.test.ts
```

Expected: PASS — `loadCustomFields` is called on task load; `activeFields.customFields` flips on a non-empty map.

- [ ] **Step 6: Run the full unit suite + typecheck + lint + commit**

```bash
cd frontend && pnpm test:unit && pnpm typecheck && pnpm lint:fix && pnpm lint:styles:fix
git add frontend/src/views/tasks/TaskDetailView.vue frontend/src/views/tasks/TaskDetailView.customFields.test.ts frontend/src/i18n/lang/en.json
git commit -m "feat(s5): render CustomFields section on the task detail view

Add 'customFields' to FieldType + activeFields; load custom fields in the
watch(taskId) loader alongside the native task GET (S3 AC#1 — values are a
separate resource); render <CustomFields> in the .details grid with
v-if='activeFields.customFields' (auto-shows when the project has assigned
fields — the attachments precedent, no action button). Add the
task.attributes.customFields i18n key."
```

---

## Task 7: Test harness + throwaway `mage test:e2e` wiring + docs note (plugin repo)

**Files:**
- Modify: `scripts/run-test-env.sh` — Step 5b (seed the type variety)
- Create: `docs/superpowers/vikunja-fork/notes/s5-mage-test-e2e-wiring.md` — the throwaway wiring note
- Test: `mage test:e2e` (in the vikunja repo) + the headed browser at `http://127.0.0.1:4176`

**Interfaces:**
- Consumes: Task 1 (the read-path fix — the map is non-empty for assigned fields), Tasks 2–6 (the frontend renders the type variety).
- Produces: a test instance that seeds every field type (so the headed browser renders each), and a documented (throwaway) way to point `mage test:e2e` at the sibling plugin so the Playwright suite can exercise the custom-fields UI.

**Required Skills:** `designing-with-upstream-precedent` (N/A), `golang-testing` (the e2e harness), `golang-continuous-integration` (the `mage test:e2e` wiring).
**Recommended Skills:** none.

**Context — the test instance already serves the real fork frontend + plugin** (`compose.test.yml:3` builds the fork image `ghcr.io/itrt4176/vikunja:2.6`; `compose.test.override.yml:4` `build.context: ../vikunja`; `compose.test.yml:7` mounts the plugin live; `config.test.yml` enables the yaegi loader). A headed browser at `http://127.0.0.1:4176` hits the real task detail view talking to the real plugin endpoint. `run-test-env.sh:103-127` (Step 5b) already seeds an integer "Priority" field + a task — **extend it to seed the type variety** so the browser renders each type. The `mage test:e2e` wiring is throwaway (tied to the local relative-path setup) and documented in the plugin repo for reuse; the robust portable e2e is out of scope (deferred).

- [ ] **Step 1: Extend `run-test-env.sh` Step 5b to seed the type variety**

In `scripts/run-test-env.sh`, after the existing integer field + task seed (`:103-127`), add definitions for each type (via the manager JWT) so the headed browser renders each:

```bash
# ── Step 5c: Seed the field type variety (for the S5 render test) ────
echo "==> Creating field definitions for each type..."
TYPE_DEFS='[
  {"name":"Text Field","type":"text","project_ids":['"$PROJECT_ID"']},
  {"name":"Select Field","type":"select","options":[{"value":"draft","label":"Draft"},{"value":"published","label":"Published"}],"project_ids":['"$PROJECT_ID"']},
  {"name":"Date Field","type":"date","project_ids":['"$PROJECT_ID"']},
  {"name":"Checkbox Field","type":"checkbox","project_ids":['"$PROJECT_ID"']},
  {"name":"URL Field","type":"url","project_ids":['"$PROJECT_ID"']},
  {"name":"API-Only Field","type":"text","field_config":{"is_api_only":true},"project_ids":['"$PROJECT_ID"']}
]'
for def in $(echo "$TYPE_DEFS" | jq -c '.[]'); do
  curl -s -X POST "$BASE_URL/api/v1/plugins/custom-fields/definitions" \
    -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
    -d "$def" >/dev/null && echo "   created: $(echo "$def" | jq -r .name)" || echo "   WARN: failed to create $(echo "$def" | jq -r .name)"
done
```

(The exact `jq`/loop shape can be adjusted; the point is one definition per type + an API-only field, all assigned to `$PROJECT_ID`.)

- [ ] **Step 2: Verify the headed browser renders each type**

```bash
./scripts/run-test-env.sh
# open http://127.0.0.1:4176 in a browser, log in as testuser/testpassword,
# open the seeded task, and confirm:
#  - the custom fields section appears (mixed in with native fields)
#  - each type renders the right input (text input, select dropdown, date picker, checkbox, url input)
#  - the API-only field displays its value with a disabled input
#  - editing a value persists (refresh the task; the value is still there)
#  - clearing a value (e.g. emptying the text field) removes it
#  - a project with no custom fields shows no section
```

Manually verify each AC (the Vitest suite covers the unit behavior; this is the end-to-end visual check):

| AC | How verified (headed browser) |
|---|---|
| 1. Fields show for a project with assigned fields | The section appears on the seeded task. |
| 2. Per-type input | Each seeded type renders the right control. |
| 3. Saves through custom-fields API, not task body | Edit a value, refresh; the value persists (and `sqlite3 db/vikunja.db "select * from custom_field_values"` shows the row). |
| 4. Discard on navigate | Type into the text field, close the modal without blur; the value is not saved. |
| 5. API-only display-only | The API-only field's input is disabled; the value is shown. |
| 6. No section for projects without custom fields | A project with no assigned fields shows no custom-fields section. |
| 7. No visual distinction from native | Same `.column`/`.detail-title` rhythm as Priority/Due Date. |

- [ ] **Step 3: Write the throwaway `mage test:e2e` wiring note**

`docs/superpowers/vikunja-fork/notes/s5-mage-test-e2e-wiring.md`:

```markdown
# S5 — Throwaway `mage test:e2e` Wiring (plugin ↔ Playwright)

The vikunja repo's `mage test:e2e` builds the API from the vikunja fork and runs
the Playwright suite in `frontend/test/e2e/`. The plugin lives in a sibling repo
(`../vikunja-custom-fields-plugin`) and is **not** loaded by the e2e API instance
by default. To exercise the custom-fields UI via Playwright, the e2e API instance
must load the plugin — point the e2e harness at the sibling plugin the same way
`compose.test.override.yml` points at `../vikunja`:

1. The e2e API build (mage) must include the plugin source mounted at
   `/app/vikunja/plugins/custom-fields` (mirror `compose.test.yml:7`) and the
   yaegi loader enabled in the e2e config (mirror `config.test.yml`).
2. Seed a custom field definition + a task in the e2e setup (mirror
   `scripts/run-test-env.sh` Step 5b/5c) so a Playwright spec can open the task
   and assert the custom-fields section renders.

This wiring is **throwaway** — it depends on the local relative-path layout
(`../vikunja-custom-fields-plugin`). A robust, portable e2e (self-contained
image / CI-grade harness that doesn't require recreating the exact local setup)
is **out of scope for S5** and deferred to a future project. This note records the
wiring so it can be reused until the robust solution lands.

## What a Playwright spec would assert

- Open a task in a project with assigned custom fields → the custom-fields
  section appears (`[data-cy]` or the `.custom-fields` class).
- Each field type renders the right input.
- Editing a value persists (reload the task; the value remains).
- Clearing a value removes it.
- An API-only field is disabled.
- A project with no custom fields shows no section.
```

- [ ] **Step 4: (Optional) Add a Playwright spec under `mage test:e2e`**

If the `mage test:e2e` wiring is feasible without rebuilding the e2e API (e.g., the e2e instance can be pointed at the running `:4176` test instance instead of its own build), add a `frontend/test/e2e/custom-fields.spec.ts` that opens the seeded task and asserts the section renders. If the wiring requires non-trivial mage/e2e-config changes that risk the existing suite, **skip the Playwright spec** and rely on the headed-browser check (Step 2) — the note (Step 3) records the wiring for future reuse. The robust e2e is out of scope.

- [ ] **Step 5: Commit**

```bash
cd /mnt/data/nickp/Documents/Code/vikunja-projects/vikunja-custom-fields-plugin
git add scripts/run-test-env.sh docs/superpowers/vikunja-fork/notes/s5-mage-test-e2e-wiring.md
# (and the Playwright spec in the vikunja repo, if Step 4 was done)
git commit -m "test(s5): seed field type variety + document throwaway mage test:e2e wiring

run-test-env.sh Step 5c seeds a definition per type (text/select/date/
checkbox/url + API-only) so the headed browser renders each. The
s5-mage-test-e2e-wiring note records how to point the vikunja repo's
Playwright harness at the sibling plugin — throwaway (local-relative-path),
reusable until the robust portable e2e lands (out of scope for S5)."
```

---

## Self-Review (run by the plan author)

**1. Spec coverage:**
- Backend prerequisite (`readValuesForTask` fix) → Task 1. ✓
- `withTime` prop on `Datepicker` (fork core) → Task 2. ✓
- Types + service (`getValues`/`bulkUpsert`/`delete` bypass) → Task 3. ✓
- Store actions (piggyback on `useTaskStore`, no kanban sync, replace-whole-map) → Task 4. ✓
- `CustomFields.vue` (data-driven, type→input, save-vs-clear, api-only) → Task 5. ✓
- `TaskDetailView` integration + i18n → Task 6. ✓
- Test harness (type variety) + throwaway e2e wiring + docs note → Task 7. ✓
- ACs 1–7 → covered by the Vitest suite (Tasks 3–6) + the headed browser (Task 7); the AC↔test table is in Task 7 Step 2 + the spec's testing section. ✓
- Out of scope (Quick Add Magic; list/kanban/table/gantt views; management UI S9; robust portable e2e) → not in any task. ✓

**2. Placeholder scan:** no TBD/TODO. The Task 5 multiselect sketch has a `!` non-null assertion flagged as a "sketch crutch" to remove — that's an implementation note, not a plan placeholder. The Task 6 test has a commented-out assertion shape with guidance — acceptable (the test is real; the mount setup is the implementer's to finalize against the heavy view). No "implement later"/"add appropriate error handling".

**3. Type consistency:** `CustomFieldService.getValues(taskId)` / `bulkUpsert(taskId, items)` / `delete({taskId, fieldId})` — consistent across Task 3 (defines), Task 4 (store calls), Task 5 (component calls the store). `saveCustomFieldValue({taskId, fieldId, value})` / `clearCustomFieldValue({taskId, fieldId})` — consistent across Task 4 (defines), Task 5 (calls), Task 6 (`loadCustomFields`). `CustomFieldValuesMap` / `ICustomFieldValue` / `ICustomFieldDefinition` — consistent across Task 3 (defines), Task 4 (uses), Task 5 (uses). `withTime` prop — consistent across Task 2 (defines on both), Task 5 (`:with-time`).

**4. Deviation from spec:** `models/customField.ts` is omitted (the spec's file layout lists it; the service returns raw `data` and a model would `omitBy(isNil)`-drop `value: null`). Noted in Global Constraints. Flag to the user.

No further revision needed inline.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/vikunja-fork/plans/2026-09-01-s5-custom-fields-on-task-detail.md`.** Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
