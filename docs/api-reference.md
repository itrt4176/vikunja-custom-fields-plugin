# REST API reference

The custom-fields plugin exposes its entire surface under one base path on the
Vikunja server:

```
/api/v1/plugins/custom-fields
```

Every request and response body is JSON (`Content-Type: application/json`)
except the management UI routes, which serve HTML and JavaScript. Timestamps
are not part of the API surface. This document describes the plugin's API as
shipped; for installation and configuration see
[`installation.md`](installation.md).

## Authentication

All API routes except the two management-UI routes require authentication:

```
Authorization: Bearer <jwt>
```

This is the same token the Vikunja web app uses — it is held in the browser's
`localStorage` under `token` after login. There is no separate plugin
authentication.

**API tokens are accepted when scoped correctly.** A `tk_…` API token
authorizes a plugin route only if its permissions include the `plugins` group
entry for that exact route and method — every plugin endpoint is listed as its
own entry under the `plugins` group in `GET /api/v1/routes`, the same list the
token-creation UI offers. A token without the matching permission is rejected
with `401`, like any invalid credential. Requests made with an API token act
as the token's owner, so the management whitelist and task permissions apply
to that user exactly as they do for JWT requests.

## Authorization model

Two independent gates apply, depending on what a request touches:

- **Field definitions** (create, read, update, delete, impact previews) are
  gated on the **management whitelist** — the config key
  `customfields.whitelist` (see [`installation.md`](installation.md)).
  Non-managers receive `403 Not permitted to manage custom fields`.
- **Field values** follow the **task's own permissions**: reading values
  requires the task to be readable, writing values requires the task to be
  writable, exactly as `models.Task.CanRead`/`CanUpdate` decide it. The
  whitelist plays no role here — anyone who can edit a task can edit its
  custom field values.

Additionally, **every value write checks assignment**: the field must be
assigned to the task's project (specifically, or globally). Writes to a field
that does not apply to the task's project fail with `400`.

## Error model

Errors are returned as JSON with a human-readable `message`. Errors raised by
Vikunja's own auth middleware (malformed/missing JWT) carry the host's
standard shape with a numeric `code`; errors raised by the plugin carry only
`message`:

```json
{"message": "custom field definition 42 not found"}
```

The plugin's internal error codes (9001–9015) are **not** exposed on the wire.
Status codes and messages:

| Status | When | Message pattern |
| --- | --- | --- |
| 400 | Malformed body or path parameter | `invalid request body`, `invalid id`, `invalid task id`, `invalid field id`, `invalid project_id` |
| 400 | Definition validation failure | `custom field name must not be empty`, `invalid custom field type: <type>`, `options are only allowed for select/multiselect, not <type>`, `duplicate option value: <value>`, `constraint min/max is not valid for type <type>`, `invalid constraint: <detail>`, `project <id> does not exist`, `a field is either global (all projects) or assigned to specific projects, not both` |
| 400 | Value validation failure | `invalid value for <type> field: <detail>`, `value for a required field must not be empty`, `option value "<value>" is not a valid option for this field`, `field is not assigned to this task's project` |
| 401 | Missing or invalid JWT | `unauthorized` |
| 403 | Authenticated but not permitted | `not permitted to manage custom fields` (definitions), `no access to this task` (value reads), `no write access to this task` (value writes) |
| 404 | Resource absent | `custom field definition <id> not found`, `custom field value not found for field <id> on task <id>`, `task <id> not found` (only via a race between the permission check and the task lookup — a missing task normally surfaces as `403`), `value not found` |
| 409 | Create when a value already exists | `custom field value already exists for field <id> on task <id>` |
| 500 | Database or unexpected failure | Passes through the internal error text |

## Field types and value formats

Definitions declare one of ten types. The `value` JSON you send must match the
type; the `value` you read back is the same value in its native JSON form:

| Type | Write | Read back | Rules |
| --- | --- | --- | --- |
| `text` | string | string | Whitespace-only counts as empty |
| `textarea` | string (rich-text HTML) | string | Stored as sent |
| `url` | string | string | Must parse as a URL **with a scheme** (`https://…`, not `example.com/…`) |
| `integer` | number or decimal-free numeric string | number | Must be an int64; rejects `1.5`; honors `min`/`max` |
| `decimal` | number or numeric string | number | Parsed as float64; honors `min`/`max` |
| `date` | `"2026-09-06"` | string | Strict `YYYY-MM-DD` |
| `datetime` | `"2026-09-06T12:00:00Z"` | string | Strict RFC 3339 (timezone required) |
| `checkbox` | `true`/`false` (or their string forms) | boolean | |
| `select` | one option value string | string | Must match a current option value |
| `multiselect` | array of option value strings | array of strings | Each element must match a current option value; duplicates pointless |

Value rules that apply across types:

- **Required fields** (`field_config.required`) reject empty values — empty
  strings, and empty arrays for `multiselect` — with `400`.
- **Read-as-null policy.** A value that is absent, stored as an empty string,
  or can no longer be coerced to the field's type (e.g. after a removed select
  option or a numeric range change) reads back as `null`. Reads never fail
  because of stale stored data.
- Clearing by writing an "empty" value only works for `text` and `textarea`
  (empty string), and for non-required `select`/`multiselect` (empty string /
  empty array). For the remaining types an empty write fails validation with
  `400` — use the DELETE endpoint to clear any value. A stored empty value
  reads back as `null`.
- `field_config.default` and `field_config.is_api_only` are **not enforced by
  the API** — they are stored and returned. The fork's task UI honors
  `is_api_only` by rendering the field as display-only.

## Data objects

### Definition (single-resource form)

Returned by create, single read, and update. `project_ids` empty means the
field is **global** (all projects). `options` is always present, empty for
non-select types:

```json
{
  "id": 1,
  "name": "Estimate",
  "type": "decimal",
  "description": "Effort estimate",
  "field_config": {
    "required": false,
    "default": "",
    "is_api_only": false,
    "min": 0,
    "max": 100
  },
  "display_order": 1,
  "options": [],
  "project_ids": []
}
```

`field_config` always carries `required`, `default`, and `is_api_only`;
`min` and `max` appear only when set (integer/decimal types only). Option
objects look like:

```json
{
  "id": 3,
  "custom_field_definition_id": 1,
  "value": "high",
  "label": "High",
  "display_order": 0
}
```

`value` is the stored identifier sent with select/multiselect values; `label`
is the display text and may be empty.

### Definition (list-item form)

The list endpoint returns the same objects **without** `options` and
`project_ids` — follow up with a single read when you need the relations.

### Value entry

Every value response wraps a native value with its full definition (list-item
form plus `options` and `project_ids`), so a client can render without a
second call:

```json
{
  "value": 8,
  "field": {
    "id": 1,
    "name": "Estimate",
    "type": "decimal",
    "description": "Effort estimate",
    "field_config": {"required": false, "default": "", "is_api_only": false, "min": 0, "max": 100},
    "display_order": 1,
    "options": [],
    "project_ids": []
  }
}
```

## Endpoint summary

| Method | Path | Auth | Permission |
| --- | --- | --- | --- |
| GET | `/management-access` | yes | any authenticated user |
| GET | `/health` | yes | any authenticated user |
| GET | `/definitions` | yes | manager |
| POST | `/definitions` | yes | manager |
| GET | `/definitions/{id}` | yes | manager |
| PUT | `/definitions/{id}` | yes | manager |
| DELETE | `/definitions/{id}` | yes | manager |
| POST | `/definitions/{id}/impact` | yes | manager |
| GET | `/definitions/{id}/impact` | yes | manager |
| GET | `/tasks/{task}/custom-fields` | yes | task read |
| POST | `/tasks/{task}/custom-fields` | yes | task write |
| GET | `/tasks/{task}/custom-fields/{field_id}` | yes | task read |
| POST | `/tasks/{task}/custom-fields/{field_id}` | yes | task write |
| PUT | `/tasks/{task}/custom-fields/{field_id}` | yes | task write |
| DELETE | `/tasks/{task}/custom-fields/{field_id}` | yes | task write |
| GET | `/ui` and `/ui/app.js` | none | serves the management UI |

"Auth: yes" means any authenticated credential — a user JWT, or an API token
with the matching `plugins` permission (see [Authentication](#authentication)).

---

## Management endpoints

### `GET /management-access`

Answers whether the current user is on the management whitelist. Returns
`200` for any authenticated user — this is the gate check, not a gated
operation:

```json
{"is_manager": false}
```

### `GET /health`

Load and liveness proof:

```json
{"name": "custom-fields", "version": "0.1.0", "status": "ok"}
```

`version` is the plugin's internal version string.

---

## Definition endpoints

### `GET /definitions`

List definitions, ordered by `display_order` ascending, in list-item form
(no `options`/`project_ids`). **Requires the management whitelist** — regular
users get their fields embedded in task value reads instead.

| Query parameter | Type | Meaning |
| --- | --- | --- |
| `project_id` | int | Only fields assigned to that project (or global). Omit for all fields. |

```bash
curl -s -H "Authorization: Bearer $JWT" \
  "$BASE/definitions?project_id=3"
```

```json
[
  {
    "id": 1,
    "name": "Estimate",
    "type": "decimal",
    "description": "Effort estimate",
    "field_config": {"required": false, "default": "", "is_api_only": false, "min": 0, "max": 100},
    "display_order": 1
  }
]
```

Errors: `403` (not a manager), `400 invalid project_id`.

### `POST /definitions`

Create a definition. The body is the full definition; relations are created in
the same transaction:

| Field | Type | Rules |
| --- | --- | --- |
| `name` | string | Required, non-empty after trimming |
| `type` | string | One of the ten types (see table above) |
| `description` | string | Optional |
| `field_config` | object | Optional; `min`/`max` only valid for `integer`/`decimal`, `min` ≤ `max`; `required`, `default`, `is_api_only` stored as metadata |
| `display_order` | int | Optional, default 0 |
| `options` | array | **Only** for `select`/`multiselect`; `value` required and unique (non-empty), `label` optional, `display_order` optional |
| `project_ids` | array of int | Project IDs to assign; **empty/omitted = global**. `0` is rejected. Each ID must exist. |

```bash
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{
        "name": "Priority",
        "type": "select",
        "display_order": 2,
        "options": [
          {"value": "low", "label": "Low", "display_order": 0},
          {"value": "high", "label": "High", "display_order": 1}
        ],
        "project_ids": [3, 7]
      }' \
  "$BASE/definitions"
```

Returns `201` with the **single-resource form** — the canonical re-read with
real option IDs and resolved `project_ids`. Errors: `403`, and the 400
definition-validation catalog above.

### `GET /definitions/{id}`

Fetch one definition in single-resource form (with `options` and
`project_ids`). Errors: `403`, `404`.

### `PUT /definitions/{id}`

Replace a definition **wholesale**. The body is the same full definition as
create; every field is written as sent — fields omitted from the body are
cleared. Relations are replaced in the same transaction:

- `options` are reconciled **by value**: options whose `value` already exists
  are updated in place (their IDs are preserved, which stored select values
  reference); new values are inserted; options absent from the body are
  deleted. Stored values that referenced deleted options read back without
  them (`null` when nothing remains).
- `project_ids` are replaced; empty array means global. `0` is rejected; each
  ID must exist.

Returns `200` with the single-resource form of the updated definition.
Changing the `type` does not delete stored values — they remain stored but
read back as `null` until they happen to validate again (see the read-as-null
policy). Errors: `403`, `404`, 400 catalog.

### `DELETE /definitions/{id}`

Delete a definition. **All stored values are destroyed** — values, their
select-option links, the option list, and the project assignments cascade
synchronously in one transaction. There is no archive or undo.

Returns `204 No Content`. Errors: `403`, `404`.

### `POST /definitions/{id}/impact`

Preview how many stored values a candidate update would invalidate. The body
is **exactly the body the subsequent PUT would send** — the server diffs the
candidate against the current definition; clients must not reimplement the
semantics. Invalidating changes are: a type change (all values), removing
options from a select-like field (values using them), and tightening a
numeric `min`/`max` (out-of-range values):

```bash
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name": "Priority", "type": "select", "options": [{"value": "low"}], "project_ids": [3]}' \
  "$BASE/definitions/2/impact"
```

```json
{"affected_values": 4}
```

Writes nothing. Errors: `403`, `404`, `400`.

### `GET /definitions/{id}/impact`

Preview the delete cascade — how many stored values deleting this definition
would destroy (the total for the field):

```json
{"affected_values": 12}
```

Errors: `403`, `404`.

---

## Value endpoints

Value paths are nested under the task: `/tasks/{task}/custom-fields`, where
`{task}` is the Vikunja task ID and `{field_id}` is the definition ID.

### `GET /tasks/{task}/custom-fields`

All of the task's project-assigned fields with their values, as a **map keyed
by definition ID (as a string)**. This is the canonical read the fork's task
detail view renders from:

- Fields assigned to the task's project but with no value (or an unreadable
  one) appear with `value: null` — assigned-but-unset is present.
- Fields **not** assigned to the task's project are absent entirely.
- Ordered content is not guaranteed by the map; each entry's `field.display_order`
  is the sort key.

```bash
curl -s -H "Authorization: Bearer $JWT" \
  "$BASE/tasks/42/custom-fields"
```

```json
{
  "1": {"value": 8, "field": {"id": 1, "name": "Estimate", "type": "decimal", "field_config": {"required": false, "default": "", "is_api_only": false, "min": 0, "max": 100}, "display_order": 1, "options": [], "project_ids": []}},
  "2": {"value": ["bug", "regression"], "field": {"id": 2, "name": "Tags", "type": "multiselect", "field_config": {"required": false, "default": "", "is_api_only": false}, "display_order": 2, "options": [{"id": 4, "custom_field_definition_id": 2, "value": "bug", "label": "Bug", "display_order": 0}, {"id": 5, "custom_field_definition_id": 2, "value": "regression", "label": "Regression", "display_order": 1}], "project_ids": []}},
  "3": {"value": null, "field": {"id": 3, "name": "Reviewed", "type": "checkbox", "field_config": {"required": false, "default": "", "is_api_only": false}, "display_order": 3, "options": [], "project_ids": []}}
}
```

Errors: `403 no access to this task` — no read permission, **or the task does
not exist** (a missing task fails the permission check, not a lookup).

### `POST /tasks/{task}/custom-fields`

Write one or more values in a **single atomic request**. The body is a **bare
JSON array** — no wrapper object. Each item targets a field by ID; each write
is an **upsert** (an existing value for that field on that task is replaced):

```json
[
  {"custom_field_definition_id": 1, "value": 13},
  {"custom_field_definition_id": 2, "value": ["bug"]},
  {"custom_field_definition_id": 3, "value": true}
]
```

Rules:

- Every item must pass validation for its field's type and the field must
  apply to the task's project — otherwise the **whole batch fails** and
  nothing is written (single transaction).
- Unknown field IDs fail the batch with `404`.
- The write uses the same coercion as single writes: writing a value that
  validates is equivalent to the per-field PUT.

Returns `200` with the full task map (same shape as the collection GET),
re-read after the commit. Errors: `403 no write access to this task` (also for
a task that does not exist), `404` (unknown field ID), `400` (validation,
first failing item).

### `GET /tasks/{task}/custom-fields/{field_id}`

One field's entry on the task, in single-entry form:

```json
{"value": ["bug"], "field": {"id": 2, "name": "Tags", "type": "multiselect", "description": "", "field_config": {"required": false, "default": "", "is_api_only": false}, "display_order": 2, "options": [{"id": 4, "custom_field_definition_id": 2, "value": "bug", "label": "Bug", "display_order": 0}], "project_ids": []}}
```

An assigned-but-unset field returns `200` with `value: null`. `404 value not
found` means the field is **not assigned to the task's project** (or the
definition no longer exists). Errors: `403 no access to this task`.

### `POST /tasks/{task}/custom-fields/{field_id}`

Create a value for one field. Body:

```json
{"value": ["bug", "regression"]}
```

Create-only: if a value already exists for this (field, task) pair the request
fails with `409` — use PUT to overwrite. Returns `201` with the single entry,
re-read after the commit. Errors: `403 no write access to this task`, `404`,
`400` (validation, including *field is not assigned to this task's project*).

### `PUT /tasks/{task}/custom-fields/{field_id}`

Replace one field's value. Body as for POST. Replace-only: fails with `404`
(`custom field value not found for field <id> on task <id>`) when no value
exists yet — use POST to create. Returns `200` with the single entry,
re-read after the commit. Errors: `403`, `404`, `400`.

### `DELETE /tasks/{task}/custom-fields/{field_id}`

Delete one field's value (and, for select-like fields, its stored option
links). Idempotent: `204 No Content` whether or not a value existed. Errors:
`403 no write access to this task`.

---

## Behavior notes

- **Cascades.** Deleting a definition destroys all its stored values
  immediately. Deleting a task in Vikunja destroys the task's custom field
  values asynchronously via the host's `task.deleted` event — a value read in
  the window between task deletion and listener completion finds nothing.
- **No events for value changes.** Writes are not broadcast; poll the read
  endpoints if you need to track changes.
- **Empty bodies.** A definition list with no fields returns `[]`; a task with
  no assigned fields returns `{}`.
- **Bulk is atomic; single writes are not.** The bulk POST wraps all items in
  one transaction; per-field writes commit individually.
- **Select values are option-ID links.** Option IDs are preserved across
  label/display-order edits, so relabeling an option re-labels existing
  values. Deleting an option removes it from every stored value that used it.

## Management UI routes

Not part of the JSON API, listed for completeness. Both are **unauthenticated**
(the page authenticates its own API calls) and served from the plugin's
`ui/` directory on disk — edits take effect on the next page load:

| Path | Response |
| --- | --- |
| `GET /api/v1/plugins/custom-fields/ui` | `index.html` (`text/html`) |
| `GET /api/v1/plugins/custom-fields/ui/app.js` | `app.js` (`text/javascript`) |

---

## Worked example

```bash
BASE=http://localhost:3456/api/v1/plugins/custom-fields
JWT=<a Vikunja login JWT for a whitelisted user>

# 1. Create a select field on project 3
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name": "Severity", "type": "select",
       "options": [{"value": "s1", "label": "Critical"}, {"value": "s2", "label": "Major"}],
       "project_ids": [3]}' \
  "$BASE/definitions" | jq .

# 2. Set a value on task 42 (bulk endpoint, bare array)
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '[{"custom_field_definition_id": 1, "value": "s1"}]' \
  "$BASE/tasks/42/custom-fields" | jq .

# 3. Read it back
curl -s -H "Authorization: Bearer $JWT" "$BASE/tasks/42/custom-fields" | jq .

# 4. Clear it
curl -s -X DELETE -H "Authorization: Bearer $JWT" \
  "$BASE/tasks/42/custom-fields/1"
```

A non-manager JWT can do everything in steps 2–4 (gated only by task
permissions) but gets `403` on the definition endpoints.
