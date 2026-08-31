# S3 — Task Field Values API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the CRUD API for custom field values on tasks — read/write values via the plugin API, validated against field definitions, cascade-deleted when a task or definition is removed, absent for projects without the field assigned.

**Architecture:** Single `main.go` yaegi plugin (S2's layout). Values are a dedicated resource modeled on Vikunja's assignees/labels (own CRUD endpoints, `xorm:"-"` readOnly on the task upstream). Scalars store in `custom_field_values.Value`; select-type values store in a new `custom_field_value_options` child table (the `label_tasks` shape). A `TaskDeletedEvent` listener cascade-deletes values async after the host's delete commits; `CustomFieldDefinition.Delete` cascade-deletes values synchronously. Authorization reuses host `Task.CanRead`/`CanUpdate` via `*user.User` (or plugin-local fallback, spike-decided). Response maps, not structs (yaegi's `c.JSON({})` limitation).

**Tech Stack:** Go (yaegi-interpreted), xorm (`xorm.Session` string-chaining — no `xorm.io/builder`, not in the symbol table), echo v5, watermill `*message.Message`, Vikunja 2.5 (`itrt4176/vikunja:2.5-plugin-fix-backport`). SQLite test instance via `compose.test.yml`.

**Spec:** `docs/superpowers/specs/2026-08-30-s3-task-field-values-api-design.md` — the plan argues from the spec; executors read both. Every decision below is grounded there with upstream evidence.

## Global Constraints

- **Single `main.go`, `package main`** — multi-file yaegi plugins hit order-dependency issues (`vikunja-docs/docs/plugin-development.md:379`). Every task edits `main.go`. NEVER split into a second `.go` file.
- **No `go test`.** The plugin source can't be `go test`ed standalone (imports resolve only inside the Vikunja module). Testing is **integration via the test instance**: `curl` + JWTs against `http://127.0.0.1:4176`, verified with `sqlite3 db/vikunja.db`. Start it with `./scripts/run-test-env.sh` (prints `JWT`, `JWT_OTHERUSER`, `PROJECT_ID`); restart after each `main.go` edit with `docker compose -f compose.test.yml restart`. **TDD here = integration-test-first:** write the curl/sqlite3 command that should pass, then implement until it does.
- **No `pkg/web/handler`, no `xorm.io/builder`** — neither is in the yaegi symbol table. Use xorm string-chaining (`s.Where(...).And("... OR ...", ...)`), never `builder.Eq/Or/In`.
- **`*user.User` not `web.Auth`** in `CanX`/model signatures — `web` is unavailable to yaegi; upstream-conversion point.
- **Response maps, not structs** — interpreted structs serialize as `{}` through `c.JSON` (spike 1, S2). Build response maps field-by-field with concrete `interface{}` values.
- **Error discrimination by message prefix** — `switch err.(type)` never matches under yaegi (interpreted errors wrap as `interp._error`); use `strings.HasPrefix(err.Error(), ...)` (S2's `toHTTPError`, `main.go:535`). Each prefix is its own `case` clause (yaegi evaluates only the first expression in a multi-expression `case`).
- **git-flow mandatory, not substitutable** — `git flow feature start` (already done: branch `feature/s3-task-field-values-api`). For any new branch, `git flow ...`, never `git branch` + `git checkout`. Conventional Commits.
- **Modify the S1 creation migration in place** (pattern B — unreleased). No new migration ID. Additive only.
- **`setOptions` is modified by S3** to preserve existing option IDs on reorder/relabel (the spec's N4 fix) — a code change, not a migration.
- **The DB is wiped and reseeded on every `run-test-env.sh`** — `docker compose down` no longer destroys it; it's wiped at next startup. To re-run ACs against fresh data, re-run `run-test-env.sh`.

---

## File Structure

All in `main.go` (single-file constraint) plus the test harness:

- **`main.go`** — every code change lives here. S3 adds layers to S2's internal layering (see spec Architecture). Sections, in file order, mirror S2's: errors → validation → permissions → models → handlers → plugin lifecycle.
- **`scripts/run-test-env.sh`** — seed additions: a custom field definition (whitelist-gated, via the S2 API) and a task, so AC verification has data to work against. Some AC seeds are done *in the test flow* (via the values API), not in the harness.

## Task Dependency

Task 0 (spikes) gates Tasks 3 (auth approach) and 5 (listener body). Tasks 1-2 (schema, models) are independent of the spikes and can proceed first. Tasks build on each other; the order below is the execution order.

---

### Task 0: Spikes (the gate — de-risk the two unverified reflect paths)

**Files:**
- Throwaway: `main.go` (replaced entirely for each spike, restored after — `git diff --exit-code main.go` must be clean when done)
- Test: the test instance (`run-test-env.sh` + `curl` + `docker logs`)

**Interfaces:**
- Consumes: the test instance, the S2 `custom_field_values` table (created by S1's migration), `pkg/events`, `pkg/models`
- Produces: two decisions recorded in this plan's task briefs — (1) does `json.Unmarshal(msg.Payload, &models.TaskDeletedEvent{})` populate `evt.Task.ID` under yaegi? (Task 5's listener body shape); (2) does `models.Task{}.CanRead(s, u)` / `CanUpdate(s, u)` work with `*user.User`? (Task 3's a-vs-b). **Task 3 and Task 5 cannot be implemented until Task 0's outcomes are recorded.**

**Required Skills:** `golang-troubleshooting` (yaegi reflect failures, log inspection), `golang-testing` (integration-test-via-curl pattern).
**Recommended Skills:** `golang-database` (the `s.Table().Where().Delete()` xorm path), `golang-structs-interfaces` (the `*user.User`-as-`web.Auth` interface-satisfaction question).

- [ ] **Step 1: Spike 1 — listener receive-and-cascade**

Back up `main.go`: `cp main.go main.go.bak`.

Replace `main.go` with a throwaway plugin that (a) registers a `task.deleted` listener in `Init()`, (b) the listener `json.Unmarshal`s `msg.Payload` into `models.TaskDeletedEvent` and logs `evt.Task.ID`, (c) deletes a seeded `custom_field_values` row by that task id, (d) exposes a route that seeds a value row for a known task id (so you can verify the delete). Minimal skeleton:

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/plugins"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/labstack/echo/v5"
)

type SpikePlugin struct{}

func (p *SpikePlugin) Name() string    { return "spike" }
func (p *SpikePlugin) Version() string { return "0.0.1" }
func (p *SpikePlugin) Init() error {
	events.RegisterListener((&models.TaskDeletedEvent{}).Name(), &spikeListener{})
	log.Infof("spike plugin init")
	return nil
}
func (p *SpikePlugin) Shutdown() error { return nil }

type spikeListener struct{}

func (l *spikeListener) Handle(msg *message.Message) error {
	var evt models.TaskDeletedEvent
	if err := json.Unmarshal(msg.Payload, &evt); err != nil {
		log.Errorf("spike unmarshal: %v", err)
		return err
	}
	log.Infof("spike listener: task.deleted for task id %d", evt.Task.ID)
	s := db.NewSession()
	defer s.Close()
	if _, err := s.Table("custom_field_values").Where("task_id = ?", evt.Task.ID).Delete(&struct{}{}); err != nil {
		return fmt.Errorf("spike delete: %w", err)
	}
	return s.Commit()
}
func (l *spikeListener) Name() string { return "spike-listener" }

func (p *SpikePlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/spike/seed-value/:taskid", func(c *echo.Context) error {
		taskID := c.PathParam("taskid")
		s := db.NewSession()
		defer s.Close()
		if _, err := s.Table("custom_field_values").Insert(map[string]interface{}{"custom_field_definition_id": 1, "task_id": taskID, "value": "seeded"}); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return s.Commit()
	})
}

var singleton = &SpikePlugin{}

func NewPlugin() plugins.Plugin                                  { return singleton }
func NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin { return singleton }
```

Run: `./scripts/run-test-env.sh` then, in a second shell:
```
# seed a value for an existing task (create a task first via the API, note its id)
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"title":"spike task","project_id":'$PROJECT_ID'}' http://127.0.0.1:4176/api/v2/tasks | jq .id
# (say it's 1)
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/spike/seed-value/1
# verify the row exists
sqlite3 db/vikunja.db "select count(*) from custom_field_values where task_id=1"
# delete the task via the native API (triggers the event)
curl -s -X DELETE -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v2/tasks/1
# wait a moment for the async listener, then check
sleep 2
sqlite3 db/vikunja.db "select count(*) from custom_field_values where task_id=1"
docker compose -f compose.test.yml logs --tail=30 | grep spike
```
Expected: the count goes 1 → 0; the logs show `spike listener: task.deleted for task id 1`. If `evt.Task.ID` is 0 or the unmarshal errors, fall back the ladder: unmarshal into a local `struct{ Task struct{ ID int64 \`json:"id"\` } \`json:"task"\` }` → unmarshal into `map[string]interface{}` and walk `evt["task"].(map[string]interface{})["id"]` → any-other-viable-path. Record which form populated the ID.

- [ ] **Step 2: Record Spike 1 outcome**

In this plan, edit Task 5's listener-body step to use the unmarshal form that worked. If the host `models.TaskDeletedEvent` form worked, Task 5 uses it as written. If a fallback was needed, Task 5 uses that. **Record the outcome here before restoring `main.go`:**
> Spike 1 outcome: [fill in — which unmarshal form populated evt.Task.ID]

- [ ] **Step 3: Spike 2 — task access via `*user.User`**

Restore `main.go` from backup: `mv main.go.bak main.go` (then re-backup: `cp main.go main.go.bak`). Replace with a throwaway that exposes a route `/spike2/can-read/:taskid` calling `models.Task{ID: id}.CanRead(s, u)` with `*user.User` from `user.GetCurrentUserFromDB(s, c)`, returning `{can_read: true/false}`. Minimal skeleton:

```go
package main

import (
	"net/http"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/plugins"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
)

type Spike2Plugin struct{}

func (p *Spike2Plugin) Name() string    { return "spike2" }
func (p *Spike2Plugin) Version() string { return "0.0.1" }
func (p *Spike2Plugin) Init() error     { return nil }
func (p *Spike2Plugin) Shutdown() error { return nil }

func (p *Spike2Plugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/spike2/can-read/:taskid", func(c *echo.Context) error {
		s := db.NewSession()
		defer s.Close()
		u, err := user.GetCurrentUserFromDB(s, c)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "no user")
		}
		id := c.PathParam("taskid")
		var taskID int64
		fmt.Sscanf(id, "%d", &taskID)
		t := &models.Task{ID: taskID}
		ok, _, err := t.CanRead(s, u) // discard maxPermission
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, map[string]interface{}{"can_read": ok})
	})
}

var singleton = &Spike2Plugin{}

func NewPlugin() plugins.Plugin                                    { return singleton }
func NewAuthenticatedRouterPlugin() plugins.AuthenticatedRouterPlugin { return singleton }
```

(Add `"fmt"` to imports.) Run: `./scripts/run-test-env.sh`, create a task in `$PROJECT_ID` (testuser owns it), then:
```
# testuser IS a project member → expect can_read: true
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/spike2/can-read/<taskid> | jq .
# otheruser is NOT a member → expect can_read: false (or an error — see below)
curl -s -H "Authorization: Bearer $JWT_OTHERUSER" http://127.0.0.1:4176/api/v1/plugins/spike2/can-read/<taskid> | jq .
```
Expected: testuser → `{"can_read": true}`; otheruser → `{"can_read": false}`. If the route panics or errors with a yaegi reflect message (e.g. "cannot convert", "interface conversion"), approach (a) is dead → approach (b) (plugin-local xorm). Check `docker compose -f compose.test.yml logs --tail=30` for panic traces.

- [ ] **Step 4: Record Spike 2 outcome**

Edit Task 3's `CanX` body step to use (a) if Spike 2 returned clean booleans, or (b) if it failed. **Record the outcome here before restoring:**
> Spike 2 outcome: [fill in — (a) host Task.CanRead/CanUpdate works with *user.User, or (b) fallback to plugin-local xorm]

- [ ] **Step 5: Restore `main.go` and verify clean**

```
mv main.go.bak main.go
git diff --exit-code main.go && echo "main.go restored clean" || echo "ERROR: main.go not clean"
```
Expected: `main.go restored clean`. No commit — spikes are throwaway. The outcomes are recorded above and carried into Tasks 3 and 5.

---

### Task 1: Schema — add the unique composite index and the child table

**Files:**
- Modify: `main.go` — `CustomFieldValue` struct (add `unique(field_task)` tags + `index`), add `CustomFieldValueOption` struct, modify the `Migrations()` block + `Rollback`.

**Interfaces:**
- Consumes: S2's existing `CustomFieldValue` (`main.go:51`), the existing `Migrations()` block (`main.go:771`).
- Produces: `CustomFieldValueOption` struct with `TableName() string` returning `"custom_field_value_options"`; the `UNIQUE(custom_field_definition_id, task_id)` composite; the `task_id` index. Tasks 2, 5, 6 reference `CustomFieldValueOption` and `custom_field_value_options` by these names.

**Required Skills:** `golang-database` (xorm struct tags for composite unique + index, the explicit-table `Sync2` form).
**Recommended Skills:** `golang-project-layout` (single-file layering), the `vikunja-docs/docs` migration guidance.

- [ ] **Step 1: Write the failing verification**

The schema change is verified by inspecting the migrated DB. Start the instance with the *current* (pre-change) `main.go` and confirm the index/table don't exist yet:
```
./scripts/run-test-env.sh
sqlite3 db/vikunja.db ".indices custom_field_values"
sqlite3 db/vikunja.db ".tables" | tr ' ' '\n' | grep custom_field_value_options || echo "table absent (expected before Task 1)"
```
Expected: no `UQE_custom_field_values_*` composite; `custom_field_value_options` absent. (This is the "test fails" baseline — the thing we're about to add isn't there.)

- [ ] **Step 2: Add the unique composite + index to `CustomFieldValue`**

Modify the `CustomFieldValue` struct (`main.go:51`) — change the two FK column tags to form the composite unique and add the task_id index:

```go
type CustomFieldValue struct {
	ID                      int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldDefinitionID int64     `xorm:"bigint not null unique(field_task)" json:"custom_field_definition_id"`
	TaskID                  int64     `xorm:"bigint not null unique(field_task) index" json:"task_id"`
	Value                   string    `xorm:"text" json:"value"`
	Created                 time.Time `xorm:"created not null" json:"-"`
	Updated                 time.Time `xorm:"updated not null" json:"-"`
}
```

- [ ] **Step 3: Add the `CustomFieldValueOption` child-table struct**

Add after the `CustomFieldValue` struct + its `TableName()`:

```go
// CustomFieldValueOption is one selected option of a select/multiselect value on a task.
// The label_tasks shape: a real table with its own PK, FKs to the value and the option.
type CustomFieldValueOption struct {
	ID                  int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	CustomFieldValueID  int64     `xorm:"bigint not null index" json:"custom_field_value_id"`
	CustomFieldOptionID int64     `xorm:"bigint not null index" json:"custom_field_option_id"`
	Created             time.Time `xorm:"created not null" json:"-"`
}

func (CustomFieldValueOption) TableName() string { return "custom_field_value_options" }
```

- [ ] **Step 4: Modify the `Migrations()` block — Sync2 the new table**

In `Migrations()` (`main.go:771`), inside the existing `Migrate` func, add the new table's `Sync2` after the `custom_field_values` sync, before the `custom_field_options` sync:

```go
if err := tx.Table("custom_field_value_options").Sync2(&CustomFieldValueOption{}); err != nil {
	return fmt.Errorf("custom-fields: sync value options: %w", err)
}
```

And in the `Rollback` func, add `"custom_field_value_options"` to the drop list, *before* `"custom_field_values"` (dependency order — value-options reference values):

```go
Rollback: func(tx *xorm.Engine) error {
	return tx.DropTables("custom_field_value_options", "custom_field_values", "custom_field_options", "custom_field_projects", "custom_field_definitions")
},
```

- [ ] **Step 5: Restart and verify the schema**

```
./scripts/run-test-env.sh
sqlite3 db/vikunja.db ".indices custom_field_values"
sqlite3 db/vikunja.db ".tables" | tr ' ' '\n' | grep custom_field_value_options
sqlite3 db/vikunja.db ".schema custom_field_value_options"
```
Expected: a `UQE_custom_field_values_*` composite-unique index present; an `IDX` or `UQE` on `task_id`; the `custom_field_value_options` table exists with columns `id, custom_field_value_id, custom_field_option_id, created`. If `Sync2` complains about dropping an existing index, fall back to a column-add migration (S2's watched point) — but it shouldn't (the tables are fresh).

- [ ] **Step 6: Commit**

```
git add main.go
git commit -m "feat(s3): add unique(field,task) index and custom_field_value_options child table"
```

---

### Task 2: Value validation (pure function)

**Files:**
- Modify: `main.go` — add `validateValue` (pure, no DB) and select-type value error types + ErrCode consts + error structs. Add select-value value-string-to-option-ID resolution as a *separate* function (called by the write handlers, not by `validateValue` which stays DB-free).

**Interfaces:**
- Consumes: `CustomFieldDefinition` (the field's `Type` + `FieldConfig`), `[]CustomFieldOption` (the field's options, for select validation).
- Produces: `validateValue(def *CustomFieldDefinition, options []CustomFieldOption, raw interface{}) (string, []int64, error)` — returns the coerced storage string for scalars, or (empty string, option IDs) for select-types; or a typed error. `resolveOptionIDs(def, options, valueStrings []string) ([]int64, error)` — maps option value strings to option IDs (DB-free: matches against the passed `options` slice by `Value`). Tasks 4, 6 call these.

**Required Skills:** `golang-error-handling` (typed errors, the `ErrCode` const pattern from S2), `golang-safety` (type-switch on `raw interface{}`, numeric overflow).
**Recommended Skills:** `golang-testing` (the integration-via-curl pattern for validation ACs), `golang-naming` (error type names matching S2's `ErrCustomField*` convention).

- [ ] **Step 1: Write the failing verification (validation AC#3)**

```
./scripts/run-test-env.sh
# (create a field definition + a task first; see Task 4 for the route — for now this is the target behavior)
# After Task 4 lands, this is the AC#3 verification; here we set up the expectation:
# integer field, write a non-numeric value → 400
# select field, write an option value not in the list → 400
```
Expected: (placeholder until Task 4's routes exist; `validateValue` is exercised through them). The pure function itself can't be `go test`ed standalone — verify it via the routes in Task 4's AC#3 step.

- [ ] **Step 2: Add the select-value error types**

Extend S2's error block (after `ErrCustomFieldGlobalConflict`, `main.go:91-101`) with:

```go
const (
	ErrCodeCustomFieldValueInvalid       = 9010
	ErrCodeCustomFieldValueEmpty         = 9011
	ErrCodeCustomFieldOptionNotFound     = 9012
	ErrCodeCustomFieldValueAlreadyExists = 9013
	ErrCodeCustomFieldValueNotFound      = 9014
	ErrCodeCustomFieldTaskNotFound       = 9015
)

type ErrCustomFieldValueInvalid struct{ Type, Detail string }

func (e ErrCustomFieldValueInvalid) Error() string {
	return fmt.Sprintf("invalid value for %s field: %s", e.Type, e.Detail)
}

type ErrCustomFieldValueEmpty struct{}

func (ErrCustomFieldValueEmpty) Error() string { return "value for a required field must not be empty" }

type ErrCustomFieldOptionNotFound struct{ Value string }

func (e ErrCustomFieldOptionNotFound) Error() string {
	return fmt.Sprintf("option value %q is not a valid option for this field", e.Value)
}

type ErrCustomFieldValueAlreadyExists struct{ FieldID, TaskID int64 }

func (e ErrCustomFieldValueAlreadyExists) Error() string {
	return fmt.Sprintf("value for field %d already exists on task %d", e.FieldID, e.TaskID)
}

type ErrCustomFieldValueNotFound struct{ FieldID, TaskID int64 }

func (e ErrCustomFieldValueNotFound) Error() string {
	return fmt.Sprintf("value for field %d not found on task %d", e.FieldID, e.TaskID)
}

type ErrCustomFieldTaskNotFound struct{ ID int64 }

func (e ErrCustomFieldTaskNotFound) Error() string {
	return fmt.Sprintf("task %d not found", e.ID)
}
```

- [ ] **Step 3: Implement `validateValue`**

Add after S2's `validateDefinition` (`main.go:199`):

```go
// validateValue coerces and validates a raw value against a field definition's type
// and constraints. No DB access. For scalar types it returns (storageString, nil, nil);
// for select-types it returns ("", nil, nil) — the option IDs are resolved separately by
// resolveOptionIDs (which needs the options slice the same way this does, but is called
// by the write handler, not here, to keep this pure). raw is the JSON-decoded value.
func validateValue(def *CustomFieldDefinition, options []CustomFieldOption, raw interface{}) (string, []int64, error) {
	switch def.Type {
	case "text", "textarea", "url":
		v, ok := raw.(string)
		if !ok {
			return "", nil, ErrCustomFieldValueInvalid{Type: def.Type, Detail: "must be a string"}
		}
		if def.FieldConfig.Required && strings.TrimSpace(v) == "" {
			return "", nil, ErrCustomFieldValueEmpty{}
		}
		if def.Type == "url" {
			u, err := url.Parse(v)
			if err != nil || u.Scheme == "" {
				return "", nil, ErrCustomFieldValueInvalid{Type: "url", Detail: "must be a valid URL with a scheme"}
			}
		}
		return v, nil, nil
	case "integer":
		switch n := raw.(type) {
		case float64: // JSON numbers arrive as float64
			i := int64(n)
			if float64(i) != n {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "out of range"}
			}
			if def.FieldConfig.Min != nil && float64(i) < *def.FieldConfig.Min {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "below min"}
			}
			if def.FieldConfig.Max != nil && float64(i) > *def.FieldConfig.Max {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "above max"}
			}
			return strconv.FormatInt(i, 10), nil, nil
		case string:
			i, err := strconv.ParseInt(n, 10, 64)
			if err != nil {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "not a valid integer"}
			}
			if def.FieldConfig.Min != nil && float64(i) < *def.FieldConfig.Min {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "below min"}
			}
			if def.FieldConfig.Max != nil && float64(i) > *def.FieldConfig.Max {
				return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "above max"}
			}
			return strconv.FormatInt(i, 10), nil, nil
		}
		return "", nil, ErrCustomFieldValueInvalid{Type: "integer", Detail: "must be a number"}
	case "decimal":
		var f float64
		switch n := raw.(type) {
		case float64:
			f = n
		case string:
			v, err := strconv.ParseFloat(n, 64)
			if err != nil {
				return "", nil, ErrCustomFieldValueInvalid{Type: "decimal", Detail: "not a valid number"}
			}
			f = v
		default:
			return "", nil, ErrCustomFieldValueInvalid{Type: "decimal", Detail: "must be a number"}
		}
		if def.FieldConfig.Min != nil && f < *def.FieldConfig.Min {
			return "", nil, ErrCustomFieldValueInvalid{Type: "decimal", Detail: "below min"}
		}
		if def.FieldConfig.Max != nil && f > *def.FieldConfig.Max {
			return "", nil, ErrCustomFieldValueInvalid{Type: "decimal", Detail: "above max"}
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil, nil
	case "date":
		s, ok := raw.(string)
		if !ok {
			return "", nil, ErrCustomFieldValueInvalid{Type: "date", Detail: "must be an ISO date string"}
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return "", nil, ErrCustomFieldValueInvalid{Type: "date", Detail: "must be YYYY-MM-DD"}
		}
		return s, nil, nil
	case "datetime":
		s, ok := raw.(string)
		if !ok {
			return "", nil, ErrCustomFieldValueInvalid{Type: "datetime", Detail: "must be an RFC3339 string"}
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return "", nil, ErrCustomFieldValueInvalid{Type: "datetime", Detail: "must be RFC3339"}
		}
		return s, nil, nil
	case "checkbox":
		switch v := raw.(type) {
		case bool:
			return strconv.FormatBool(v), nil, nil
		case string:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return "", nil, ErrCustomFieldValueInvalid{Type: "checkbox", Detail: "must be a boolean"}
			}
			return strconv.FormatBool(b), nil, nil
		}
		return "", nil, ErrCustomFieldValueInvalid{Type: "checkbox", Detail: "must be a boolean"}
	case "select", "multiselect":
		// validate the option value string(s) are in the field's current options' values.
		// returns no storage string — the handler calls resolveOptionIDs to get the IDs.
		validValues := map[string]struct{}{}
		for _, o := range options {
			validValues[o.Value] = struct{}{}
		}
		if def.Type == "select" {
			s, ok := raw.(string)
			if !ok {
				return "", nil, ErrCustomFieldValueInvalid{Type: "select", Detail: "must be a string option value"}
			}
			if def.FieldConfig.Required && s == "" {
				return "", nil, ErrCustomFieldValueEmpty{}
			}
			if s != "" {
				if _, ok := validValues[s]; !ok {
					return "", nil, ErrCustomFieldOptionNotFound{Value: s}
				}
			}
			return s, nil, nil
		}
		// multiselect: raw is a []interface{} of strings (JSON array)
		arr, ok := raw.([]interface{})
		if !ok {
			return "", nil, ErrCustomFieldValueInvalid{Type: "multiselect", Detail: "must be an array of option values"}
		}
		vals := make([]string, 0, len(arr))
		for _, e := range arr {
			s, ok := e.(string)
			if !ok {
				return "", nil, ErrCustomFieldValueInvalid{Type: "multiselect", Detail: "array elements must be strings"}
			}
			if _, ok := validValues[s]; !ok {
				return "", nil, ErrCustomFieldOptionNotFound{Value: s}
			}
			vals = append(vals, s)
		}
		if def.FieldConfig.Required && len(vals) == 0 {
			return "", nil, ErrCustomFieldValueEmpty{}
		}
		// join for a notional storage string (not actually stored for select-types, but
		// return it for completeness; the handler uses resolveOptionIDs for the child rows)
		return strings.Join(vals, "\x00"), vals, nil
	}
	return "", nil, ErrCustomFieldInvalidType{Type: def.Type}
}

// resolveOptionIDs maps option value strings to option IDs by matching the passed
// options slice's Value field. Called by write handlers after validateValue succeeds.
func resolveOptionIDs(options []CustomFieldOption, valueStrings []string) ([]int64, error) {
	byValue := map[string]int64{}
	for _, o := range options {
		byValue[o.Value] = o.ID
	}
	ids := make([]int64, 0, len(valueStrings))
	for _, v := range valueStrings {
		id, ok := byValue[v]
		if !ok {
			return nil, ErrCustomFieldOptionNotFound{Value: v}
		}
		ids = append(ids, id)
	}
	return ids, nil
}
```

Add `"net/url"` to imports if not present.

- [ ] **Step 4: Restart and verify it compiles + loads**

```
docker compose -f compose.test.yml restart
docker compose -f compose.test.yml logs --tail=20 | grep -iE "loaded plugin|error" | head
```
Expected: `Loaded plugin custom-fields` with no reflect/compile errors. (`validateValue` is exercised via the routes in Task 4; this step only confirms the plugin still loads.)

- [ ] **Step 5: Commit**

```
git add main.go
git commit -m "feat(s3): add validateValue per-type coercion + option-value resolution"
```

---

### Task 3: Authorization (`CustomFieldValue.CanX`, spike-decided)

**Files:**
- Modify: `main.go` — add `CustomFieldValue.CanRead`/`CanCreate`/`CanUpdate`/`CanDelete` methods (`*xorm.Session, *user.User` → `(bool, error)`).

**Interfaces:**
- Consumes: Spike 2's outcome (a or b); `models.GetTaskByIDSimple(s, id)` for `task.ProjectID`; `models.Task{}.CanRead`/`CanUpdate` (approach a) or `models.Project{}.CanWrite` + xorm on `project_users`/`team_members` (approach b).
- Produces: `CustomFieldValue.CanRead(s, u) (bool, error)`, `CanCreate`/`CanUpdate`/`CanDelete(s, u) (bool, error)` — all gate on task-level access. Tasks 4, 5, 6 call these in their handlers.

**Required Skills:** `golang-structs-interfaces` (the `*user.User`-as-`web.Auth` path, interface satisfaction), `golang-error-handling`.
**Recommended Skills:** `golang-database` (the plugin-local xorm fallback's `project_users`/`team_members` query), `golang-naming`.

- [ ] **Step 1: Write the failing verification (AC#5)**

This is verified through the routes (Task 4+), but the *gate* is exercised once routes exist:
```
# (after Task 4) non-member GET → 403; member GET → 200
```
Record the expected behavior here; the AC#5 step in Task 4's verification is the real test.

- [ ] **Step 2: Implement `CanX` per Spike 2's outcome**

**If Spike 2 outcome was (a)** (host `Task.CanRead`/`CanUpdate` works with `*user.User`):

```go
func (v *CustomFieldValue) CanRead(s *xorm.Session, u *user.User) (bool, error) {
	t := &models.Task{ID: v.TaskID}
	ok, _, err := t.CanRead(s, u) // discard maxPermission (3-return → 2-return)
	if err != nil {
		// a not-found task means no access; surface as false, not 500
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "does not exist") {
			return false, nil
		}
		return false, err
	}
	return ok, nil
}

func (v *CustomFieldValue) canWrite(s *xorm.Session, u *user.User) (bool, error) {
	t := &models.Task{ID: v.TaskID}
	ok, err := t.CanUpdate(s, u)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "does not exist") {
			return false, nil
		}
		return false, err
	}
	return ok, nil
}

func (v *CustomFieldValue) CanCreate(s *xorm.Session, u *user.User) (bool, error) { return v.canWrite(s, u) }
func (v *CustomFieldValue) CanUpdate(s *xorm.Session, u *user.User) (bool, error) { return v.canWrite(s, u) }
func (v *CustomFieldValue) CanDelete(s *xorm.Session, u *user.User) (bool, error) { return v.canWrite(s, u) }
```

**If Spike 2 outcome was (b)** (plugin-local xorm fallback): resolve the task's `ProjectID` via `models.GetTaskByIDSimple(s, v.TaskID)`, then check the user's permission on that project via a direct query against `project_users` (right >= 1) OR `team_members` (via the project's teams). Use:
```go
func (v *CustomFieldValue) canWrite(s *xorm.Session, u *user.User) (bool, error) {
	t, err := models.GetTaskByIDSimple(s, v.TaskID)
	if err != nil {
		return false, nil // task gone → no access
	}
	// direct-write check on project_users
	has, err := s.Table("project_users").
		Where("project_id = ?", t.ProjectID).
		And("user_id = ?", u.ID).
		And("right >= ?", 1). // 1 = read/write; 2 = admin
		Exist(&models.ProjectUser{})
	if err != nil {
		return false, err
	}
	if has {
		return true, nil
	}
	// then team access: team_members join team_projects
	has, err = s.Table("team_members").
		Join("INNER", "team_projects", "team_projects.team_id = team_members.team_id").
		Where("team_projects.project_id = ?", t.ProjectID).
		And("team_members.user_id = ?", u.ID).
		And("team_projects.right >= ?", 1).
		Exist(&models.TeamMember{})
	if err != nil {
		return false, err
	}
	return has, nil
}
```
(`CanRead` uses `right >= 0` instead of `>= 1`.) `CanRead`/`CanCreate`/`CanUpdate`/`CanDelete` delegate to `canWrite`/a `canRead` as above. **Use the (b) form only if Spike 2 failed; the (a) form is preferred.**

- [ ] **Step 3: Restart and verify the plugin loads**

```
docker compose -f compose.test.yml restart
docker compose -f compose.test.yml logs --tail=20 | grep -iE "loaded plugin|panic|error" | head
```
Expected: loads clean (the `CanX` methods aren't called until Task 4's routes).

- [ ] **Step 4: Commit**

```
git add main.go
git commit -m "feat(s3): add task-level CanX authorization for custom field values"
```

---

### Task 4: Read + write value handlers + routes (AC#1, #2, #3, #4, #5)

**Files:**
- Modify: `main.go` — add the value request/response structs, the read/write handlers, the response-map builders, the route registrations in `RegisterAuthenticatedRoutes`. Extend `toHTTPError` for the new error prefixes.

**Interfaces:**
- Consumes: `validateValue`, `resolveOptionIDs` (Task 2); `CustomFieldValue.CanX` (Task 3); `models.GetTaskByIDSimple` (for `task.ProjectID`); S2's `CustomFieldDefinition.ReadOne`/`ReadAll` (for the `field` metadata + the project-assignment check).
- Produces: the six routes (`GET`/`POST` collection; `GET`/`POST`/`PUT`/`DELETE` per-field), the response shape `{definition_id: {value, field}}`. Tasks 5, 6, 7 verify against these.

**Required Skills:** `golang-error-handling` (extending `toHTTPError`), `golang-database` (the read-path project-assignment filter + the child-table join), `golang-testing` (the AC verification curl commands).
**Recommended Skills:** `golang-code-style` (response-map construction), `golang-naming`, `golang-safety` (the `value: null` handling, the absent-vs-invalid distinction).

- [ ] **Step 1: Write the failing verification (AC#1 — read shape)**

```
./scripts/run-test-env.sh
# create a field definition assigned to $PROJECT_ID (whitelist-gated, via S2 API):
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name":"Priority","type":"integer","project_ids":['$PROJECT_ID']}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions | jq .id
# (note the field id, say 1)
# create a task in $PROJECT_ID:
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"title":"t1","project_id":'$PROJECT_ID'}' http://127.0.0.1:4176/api/v2/tasks | jq .id
# (note the task id, say 1)
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/tasks/1/custom-fields | jq .
```
Expected (before implementation): 404 or empty/error — the route doesn't exist yet. After implementation: `{}` (no values yet) — a map keyed by definition id, empty because no value written.

- [ ] **Step 2: Add the request/response structs + map builders**

Add after S2's handler structs (`main.go:462`):

```go
type valueItem struct {
	CustomFieldDefinitionID int64       `json:"custom_field_definition_id"`
	Value                   interface{} `json:"value"`
}

type bulkValueRequest struct {
	Values []valueItem `json:"values"`
}

type singleValueRequest struct {
	Value interface{} `json:"value"`
}

// valueToMap builds the {value, field} entry for the read response. fieldMap is the
// definition's metadata (built by S2's definitionToMap, reused). value is the coerced
// native value, or nil if absent/invalid.
func valueToMap(value interface{}, fieldMap map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"value": value,
		"field": fieldMap,
	}
}
```

- [ ] **Step 3: Implement the read handlers (collection + per-field)**

The read path: resolve `task.ProjectID` via `GetTaskByIDSimple` → fetch the task's values → for each, filter by project-assignment (AC#4: the field's definition must be assigned to `task.ProjectID` via the global sentinel OR a specific row) → coerce the value to native type → build the `{definition_id: {value, field}}` map.

```go
// fieldAppliesToProject mirrors S2's ReadAll project filter (NotificationProjectFilter logic,
// not builder syntax). A field applies if it has a custom_field_projects row for the project
// OR the global sentinel (project_id = 0).
func fieldAppliesToProject(s *xorm.Session, defID, projectID int64) (bool, error) {
	return s.Table("custom_field_projects").
		Where("custom_field_definition_id = ?", defID).
		And("project_id = ? OR project_id = 0", projectID).
		Exist(&CustomFieldProject{})
}

// coerceReadValue returns the native JSON value for a stored string + field type, or
// nil if the value can't be coerced (invalid → absent per the read-path policy).
func coerceReadValue(def *CustomFieldDefinition, options []CustomFieldOption, stored string) interface{} {
	switch def.Type {
	case "integer":
		i, err := strconv.ParseInt(stored, 10, 64)
		if err != nil {
			return nil
		}
		return i
	case "decimal":
		f, err := strconv.ParseFloat(stored, 64)
		if err != nil {
			return nil
		}
		return f
	case "checkbox":
		b, err := strconv.ParseBool(stored)
		if err != nil {
			return nil
		}
		return b
	case "select":
		// stored as one option_id in the child table; resolve to the option value string
		return stored // resolved in the handler via the child-table join (see readValuesForTask)
	case "multiselect":
		return stored // resolved in the handler via the child-table join
	default:
		if stored == "" {
			return nil
		}
		return stored
	}
}
```

(The select-value resolution from the child table is in `readValuesForTask` below — it joins `custom_field_value_options` → `custom_field_options` to get the option value strings, since the value string is what the API exposes.)

```go
// readValuesForTask fetches the task's values, filters by project assignment (AC#4),
// coerces to native types, and returns the {definition_id: {value, field}} map.
func readValuesForTask(s *xorm.Session, taskID int64) (map[string]interface{}, error) {
	t, err := models.GetTaskByIDSimple(s, taskID)
	if err != nil {
		return nil, ErrCustomFieldTaskNotFound{ID: taskID}
	}
	var values []CustomFieldValue
	if err := s.Table("custom_field_values").Where("task_id = ?", taskID).Find(&values); err != nil {
		return nil, fmt.Errorf("custom-fields: get values: %w", err)
	}
	out := map[string]interface{}{}
	for _, v := range values {
		// AC#4: field must be assigned to the task's project
		applies, err := fieldAppliesToProject(s, v.CustomFieldDefinitionID, t.ProjectID)
		if err != nil {
			return nil, err
		}
		if !applies {
			continue
		}
		// fetch the definition + options (for the field metadata + type coercion)
		d := &CustomFieldDefinition{ID: v.CustomFieldDefinitionID}
		def, opts, pids, err := d.ReadOne(s)
		if err != nil {
			// definition deleted but value row remains (S2 delete doesn't touch values
			// yet — Task 7 fixes the cascade). Skip it; it's an orphan.
			continue
		}
		_ = pids // field metadata uses definitionToMap, which doesn't need pids here
		fieldMap := definitionToMap(def, opts, pids)
		var native interface{}
		if isSelectLike(def.Type) {
			// resolve the option value strings from the child table
			var childRows []CustomFieldValueOption
			if err := s.Table("custom_field_value_options").Where("custom_field_value_id = ?", v.ID).Find(&childRows); err != nil {
				return nil, fmt.Errorf("custom-fields: get value options: %w", err)
			}
			if len(childRows) == 0 {
				native = nil
			} else {
				optIDs := make([]int64, 0, len(childRows))
				for _, c := range childRows {
					optIDs = append(optIDs, c.CustomFieldOptionID)
				}
				// resolve option ids → value strings
				valStrings := make([]string, 0, len(childRows))
				for _, o := range opts {
					for _, id := range optIDs {
						if o.ID == id {
							valStrings = append(valStrings, o.Value)
						}
					}
				}
				if def.Type == "select" {
					if len(valStrings) > 0 {
						native = valStrings[0]
					} else {
						native = nil
					}
				} else {
					native = valStrings
				}
			}
		} else {
			native = coerceReadValue(def, opts, v.Value)
		}
		out[strconv.FormatInt(v.CustomFieldDefinitionID, 10)] = valueToMap(native, fieldMap)
	}
	return out, nil
}

func listValuesHandler(c *echo.Context) error {
	u, err := user.GetCurrentUserFromDB(s_safe(c), c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.PathParam("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	s := db.NewSession()
	defer s.Close()
	v := &CustomFieldValue{TaskID: taskID}
	ok, err := v.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no access to this task")
	}
	out, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, out)
}

func readOneValueHandler(c *echo.Context) error {
	u, err := user.GetCurrentUserFromDB(s_safe(c), c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.PathParam("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	fieldID, err := strconv.ParseInt(c.PathParam("field_id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid field id")
	}
	s := db.NewSession()
	defer s.Close()
	v := &CustomFieldValue{TaskID: taskID, CustomFieldDefinitionID: fieldID}
	ok, err := v.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no access to this task")
	}
	// fetch this one value + apply the same AC#4 + coercion as readValuesForTask
	all, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	entry, present := all[strconv.FormatInt(fieldID, 10)]
	if !present {
		return echo.NewHTTPError(http.StatusNotFound, "value not found")
	}
	return c.JSON(http.StatusOK, entry)
}
```

`user.GetCurrentUserFromDB` takes a session — add a tiny helper to avoid repeating the session-open in the auth step (or open the session first and pass it). Simplify: open the session *before* the auth call in each handler:
```go
s := db.NewSession()
defer s.Close()
u, err := user.GetCurrentUserFromDB(s, c)
```
Replace `s_safe(c)` accordingly — there is no `s_safe` helper; open `s` first. (Adjust the skeleton above: every handler opens `s := db.NewSession(); defer s.Close()` first, then `u, err := user.GetCurrentUserFromDB(s, c)`.)

- [ ] **Step 4: Implement the write handlers (bulk POST upsert + per-field POST/PUT/DELETE)**

```go
func bulkUpsertHandler(c *echo.Context) error {
	s := db.NewSession()
	defer s.Close()
	u, err := user.GetCurrentUserFromDB(s, c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, err := strconv.ParseInt(c.PathParam("task"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	var req bulkValueRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	v := &CustomFieldValue{TaskID: taskID}
	ok, err := v.CanUpdate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no write access to this task")
	}
	for _, item := range req.Values {
		// fetch the definition + options
		d := &CustomFieldDefinition{ID: item.CustomFieldDefinitionID}
		def, opts, _, err := d.ReadOne(s)
		if err != nil {
			return toHTTPError(err)
		}
		// AC#4: field must be assigned to the task's project to be writable
		t, err := models.GetTaskByIDSimple(s, taskID)
		if err != nil {
			return toHTTPError(ErrCustomFieldTaskNotFound{ID: taskID})
		}
		applies, err := fieldAppliesToProject(s, item.CustomFieldDefinitionID, t.ProjectID)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if !applies {
			return echo.NewHTTPError(http.StatusBadRequest, "field is not assigned to this task's project")
		}
		storage, valStrings, err := validateValue(def, opts, item.Value)
		if err != nil {
			return toHTTPError(err)
		}
		// upsert: delete any existing value for (field, task), then insert
		if _, err := s.Table("custom_field_value_options").
			Where("custom_field_value_id IN (SELECT id FROM custom_field_values WHERE custom_field_definition_id = ? AND task_id = ?)", item.CustomFieldDefinitionID, taskID).
			Delete(&CustomFieldValueOption{}); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if _, err := s.Table("custom_field_values").
			Where("custom_field_definition_id = ? AND task_id = ?", item.CustomFieldDefinitionID, taskID).
			Delete(&CustomFieldValue{}); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		val := &CustomFieldValue{CustomFieldDefinitionID: item.CustomFieldDefinitionID, TaskID: taskID}
		if isSelectLike(def.Type) {
			val.Value = ""
		} else {
			val.Value = storage
		}
		if _, err := s.Table("custom_field_values").Insert(val); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if isSelectLike(def.Type) && len(valStrings) > 0 {
			optIDs, err := resolveOptionIDs(opts, valStrings)
			if err != nil {
				return toHTTPError(err)
			}
			childRows := make([]CustomFieldValueOption, len(optIDs))
			for i, id := range optIDs {
				childRows[i] = CustomFieldValueOption{CustomFieldValueID: val.ID, CustomFieldOptionID: id}
			}
			if _, err := s.Table("custom_field_value_options").Insert(&childRows); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// re-read for the canonical response
	out, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusOK, out)
}

func createOneValueHandler(c *echo.Context) error {
	s := db.NewSession()
	defer s.Close()
	u, err := user.GetCurrentUserFromDB(s, c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, _ := strconv.ParseInt(c.PathParam("task"), 10, 64)
	fieldID, _ := strconv.ParseInt(c.PathParam("field_id"), 10, 64)
	var req singleValueRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	v := &CustomFieldValue{TaskID: taskID, CustomFieldDefinitionID: fieldID}
	ok, err := v.CanCreate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no write access to this task")
	}
	// create-only: 409 if already exists
	exists, err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ? AND task_id = ?", fieldID, taskID).
		Exist(&CustomFieldValue{})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if exists {
		return echo.NewHTTPError(http.StatusConflict, ErrCustomFieldValueAlreadyExists{FieldID: fieldID, TaskID: taskID}.Error())
	}
	// (validate + insert — same inner block as bulkUpsert, factored if you prefer)
	d := &CustomFieldDefinition{ID: fieldID}
	def, opts, _, err := d.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	t, err := models.GetTaskByIDSimple(s, taskID)
	if err != nil {
		return toHTTPError(ErrCustomFieldTaskNotFound{ID: taskID})
	}
	applies, err := fieldAppliesToProject(s, fieldID, t.ProjectID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !applies {
		return echo.NewHTTPError(http.StatusBadRequest, "field is not assigned to this task's project")
	}
	storage, valStrings, err := validateValue(def, opts, req.Value)
	if err != nil {
		return toHTTPError(err)
	}
	val := &CustomFieldValue{CustomFieldDefinitionID: fieldID, TaskID: taskID}
	if isSelectLike(def.Type) {
		val.Value = ""
	} else {
		val.Value = storage
	}
	if _, err := s.Table("custom_field_values").Insert(val); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if isSelectLike(def.Type) && len(valStrings) > 0 {
		optIDs, err := resolveOptionIDs(opts, valStrings)
		if err != nil {
			return toHTTPError(err)
		}
		childRows := make([]CustomFieldValueOption, len(optIDs))
		for i, id := range optIDs {
			childRows[i] = CustomFieldValueOption{CustomFieldValueID: val.ID, CustomFieldOptionID: id}
		}
		if _, err := s.Table("custom_field_value_options").Insert(&childRows); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	all, err := readValuesForTask(s, taskID)
	if err != nil {
		return toHTTPError(err)
	}
	return c.JSON(http.StatusCreated, all[strconv.FormatInt(fieldID, 10)])
}

func updateOneValueHandler(c *echo.Context) error {
	// same as create but replace-only: 404 if absent; no 409-on-exists check
	s := db.NewSession()
	defer s.Close()
	u, err := user.GetCurrentUserFromDB(s, c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, _ := strconv.ParseInt(c.PathParam("task"), 10, 64)
	fieldID, _ := strconv.ParseInt(c.PathParam("field_id"), 10, 64)
	var req singleValueRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	v := &CustomFieldValue{TaskID: taskID, CustomFieldDefinitionID: fieldID}
	ok, err := v.CanUpdate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no write access to this task")
	}
	exists, err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ? AND task_id = ?", fieldID, taskID).
		Exist(&CustomFieldValue{})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, ErrCustomFieldValueNotFound{FieldID: fieldID, TaskID: taskID}.Error())
	}
	// delete + re-insert (replace the value + child rows), same inner block as create
	// (delete child rows, delete value, validate, insert value, insert child rows)
	// ... (mirror createOneValueHandler's insert block, prefixed by the delete)
	// [implementer: factor the validate+insert into a helper shared with create/bulk]
	// ... commit, re-read, return 200
	return c.JSON(http.StatusOK, all[strconv.FormatInt(fieldID, 10)])
}

func deleteOneValueHandler(c *echo.Context) error {
	s := db.NewSession()
	defer s.Close()
	u, err := user.GetCurrentUserFromDB(s, c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	taskID, _ := strconv.ParseInt(c.PathParam("task"), 10, 64)
	fieldID, _ := strconv.ParseInt(c.PathParam("field_id"), 10, 64)
	v := &CustomFieldValue{TaskID: taskID, CustomFieldDefinitionID: fieldID}
	ok, err := v.CanDelete(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "no write access to this task")
	}
	if _, err := s.Table("custom_field_value_options").
		Where("custom_field_value_id IN (SELECT id FROM custom_field_values WHERE custom_field_definition_id = ? AND task_id = ?)", fieldID, taskID).
		Delete(&CustomFieldValueOption{}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if _, err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ? AND task_id = ?", fieldID, taskID).
		Delete(&CustomFieldValue{}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := s.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}
```

(Note: the create/update/bulk handlers share the validate-then-insert-then-child-rows block — the implementer should factor it into a `writeValue(s, taskID, fieldID, def, opts, raw)` helper to DRY, rather than copy it three times. The plan shows it inline for clarity; the implementer extracts the helper.)

- [ ] **Step 5: Register the routes**

In `RegisterAuthenticatedRoutes` (`main.go:800`), add after the S2 definitions routes:

```go
g.GET("/tasks/:task/custom-fields", listValuesHandler)
g.POST("/tasks/:task/custom-fields", bulkUpsertHandler)
g.GET("/tasks/:task/custom-fields/:field_id", readOneValueHandler)
g.POST("/tasks/:task/custom-fields/:field_id", createOneValueHandler)
g.PUT("/tasks/:task/custom-fields/:field_id", updateOneValueHandler)
g.DELETE("/tasks/:task/custom-fields/:field_id", deleteOneValueHandler)
```

- [ ] **Step 6: Extend `toHTTPError` for the new error prefixes**

Add to the `switch` in `toHTTPError` (`main.go:544`), each its own `case` (yaegi multi-expression `case` bug):

```go
case strings.HasPrefix(msg, "invalid value for"):
	return echo.NewHTTPError(http.StatusBadRequest, msg)
case strings.HasPrefix(msg, "value for a required field must not be empty"):
	return echo.NewHTTPError(http.StatusBadRequest, msg)
case strings.HasPrefix(msg, "option value") && strings.Contains(msg, "is not a valid option"):
	return echo.NewHTTPError(http.StatusBadRequest, msg)
case strings.HasPrefix(msg, "value for field") && strings.Contains(msg, "already exists"):
	return echo.NewHTTPError(http.StatusConflict, msg)
case strings.HasPrefix(msg, "value for field") && strings.Contains(msg, "not found"):
	return echo.NewHTTPError(http.StatusNotFound, msg)
case strings.HasPrefix(msg, "task ") && strings.Contains(msg, " not found"):
	return echo.NewHTTPError(http.StatusNotFound, msg)
```

- [ ] **Step 7: Verify AC#1, #2, #3, #4, #5**

Restart (`docker compose -f compose.test.yml restart`), then:
```
# AC#1: read returns the map keyed by definition id
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/tasks/<taskid>/custom-fields | jq 'keys'
# (after a value is written)

# AC#2: write values in a single request (bulk)
curl -s -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"values":[{"custom_field_definition_id":<intfield>,"value":5}]}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/tasks/<taskid>/custom-fields | jq .
sqlite3 db/vikunja.db "select * from custom_field_values where task_id=<taskid>"

# AC#3: validation — non-numeric for integer → 400
curl -s -o /dev/null -w "%{http_code}\n" -X POST -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"values":[{"custom_field_definition_id":<intfield>,"value":"notanumber"}]}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/tasks/<taskid>/custom-fields
# expect 400

# AC#4: field not assigned to the task's project absent
# (create a 2nd project, a field assigned to project A, a task in project B, GET → field absent)

# AC#5: non-member → 403
curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $JWT_OTHERUSER" \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/tasks/<taskid>/custom-fields
# expect 403
```
Expected: AC#1 → keys present; AC#2 → 200, row in DB; AC#3 → 400; AC#4 → field absent; AC#5 → 403.

- [ ] **Step 8: Commit**

```
git add main.go
git commit -m "feat(s3): add custom field value read/write handlers + routes"
```

---

### Task 5: TaskDeletedEvent listener for cascade-delete (AC#6)

**Files:**
- Modify: `main.go` — add `taskDeletedListener` (`Handle` + `Name`), register it in `Init()`. Use the unmarshal form from Spike 1's outcome.

**Interfaces:**
- Consumes: Spike 1's outcome (the unmarshal form that populated `evt.Task.ID`); `db.NewSession`, `models.TaskDeletedEvent`, `json.Unmarshal`, `message.Message`.
- Produces: a registered `task.deleted` listener that deletes `custom_field_value_options` then `custom_field_values` by `task_id`. Task 7's AC#6 verification checks the rows are gone.

**Required Skills:** `golang-troubleshooting` (async listener debugging, log inspection), `golang-database` (the two-step delete, the subquery on value IDs).
**Recommended Skills:** `golang-concurrency` (the async-after-commit semantics), `golang-error-handling`.

- [ ] **Step 1: Write the failing verification (AC#6)**

```
./scripts/run-test-env.sh
# seed: create a select field + a task, write a value (so a value row + child row exist):
# (use the values API from Task 4)
sqlite3 db/vikunja.db "select count(*) from custom_field_values where task_id=<taskid>"   # 1
sqlite3 db/vikunja.db "select count(*) from custom_field_value_options where custom_field_value_id=<valueid>"  # >=1
# delete the task via the native API (triggers the event):
curl -s -X DELETE -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v2/tasks/<taskid>
sleep 3  # async listener
sqlite3 db/vikunja.db "select count(*) from custom_field_values where task_id=<taskid>"   # expect 0
```
Expected (before Task 5): count stays 1 (no listener). After: 0.

- [ ] **Step 2: Add the listener**

Use the unmarshal form from Spike 1. If Spike 1 found the host `models.TaskDeletedEvent` form worked:

```go
type taskDeletedListener struct{}

func (l *taskDeletedListener) Handle(msg *message.Message) error {
	var evt models.TaskDeletedEvent
	if err := json.Unmarshal(msg.Payload, &evt); err != nil {
		log.Errorf("[custom-fields] task-deleted listener unmarshal: %v", err)
		return err
	}
	s := db.NewSession()
	defer s.Close()
	// delete child rows first (no hard FK cascade), then the value rows.
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

If Spike 1 needed a fallback unmarshal form, use that form to extract the task ID instead of `evt.Task.ID`.

Add `"encoding/json"` to imports.

- [ ] **Step 3: Register the listener in `Init()`**

In `Init()` (`main.go:749`), add the `RegisterListener` call:

```go
func (p *CustomFieldsPlugin) Init() error {
	whitelist = loadWhitelist()
	events.RegisterListener((&models.TaskDeletedEvent{}).Name(), &taskDeletedListener{})
	log.Infof("[custom-fields] plugin v0.1.0 initialized")
	return nil
}
```

Add `"code.vikunja.io/api/pkg/events"` to imports.

- [ ] **Step 4: Verify AC#6**

Restart, seed a select field + a task + a value (so both a `custom_field_values` row and a `custom_field_value_options` child row exist), delete the task via the native API, then:
```
sleep 3
sqlite3 db/vikunja.db "select count(*) from custom_field_values where task_id=<taskid>"   # 0
sqlite3 db/vikunja.db "select count(*) from custom_field_value_options"  # the child row gone too
docker compose -f compose.test.yml logs --tail=40 | grep -i "task-deleted listener\|custom-fields" | tail
```
Expected: both counts 0; no error in logs. If the value row persists, check the logs for the listener firing and any unmarshal/delete error.

- [ ] **Step 5: Commit**

```
git add main.go
git commit -m "feat(s3): add TaskDeletedEvent listener to cascade-delete values"
```

---

### Task 6: `setOptions` update-in-place fix (the spec's N4 fix)

**Files:**
- Modify: `main.go` — replace S2's `setOptions` delete-all-reinsert-all (`main.go:299`) with update-in-place: preserve existing option IDs when the option value is unchanged (reorder/relabel), insert new options, delete removed options.

**Interfaces:**
- Consumes: S2's `setOptions(s, defID, t, options)` signature.
- Produces: the same signature, but option IDs are stable across reorder/relabel. This is a *prerequisite* for the child-table design's correctness (Task 4's select values reference option IDs).

**Required Skills:** `golang-database` (the update-in-place xorm logic — `Update` by ID, insert new, delete missing), `golang-safety` (the ID-preservation invariant).
**Recommended Skills:** `golang-refactoring` (preserving S2's behavior for the non-option cases), `golang-testing` (regression: reorder should preserve IDs).

- [ ] **Step 1: Write the failing verification (the orphan-on-reorder regression)**

```
./scripts/run-test-env.sh
# create a select field with options [a, b], assign to $PROJECT_ID
# create a task, write value "a" (creates a custom_field_value_options row referencing option "a"'s id)
# note option "a"'s id:
sqlite3 db/vikunja.db "select id, value from custom_field_options where custom_field_definition_id=<fieldid>"
# (say a=10, b=11)
# now EDIT the definition to reorder options [b, a] (PUT to the S2 definitions API):
curl -s -X PUT -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"name":"...","type":"select","options":[{"value":"b","display_order":0},{"value":"a","display_order":1}],"project_ids":['$PROJECT_ID']}' \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/<fieldid> | jq .
# check the option ids are PRESERVED (a still = 10):
sqlite3 db/vikunja.db "select id, value from custom_field_options where custom_field_definition_id=<fieldid>"
# and the stored value still reads back:
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/tasks/<taskid>/custom-fields | jq .
```
Expected (before Task 6): option "a" gets a NEW id (the delete-reinsert); the stored value reads back as `null` (orphaned). After Task 6: option "a" keeps id 10; the stored value still reads back as `"a"`.

- [ ] **Step 2: Replace `setOptions` with update-in-place**

Replace S2's `setOptions` (`main.go:299-313`) with a version that preserves existing option IDs by matching on `Value`:

```go
// setOptions reconciles a definition's option rows. Existing options are matched by Value
// and updated in place (preserving their IDs — critical because custom_field_value_options
// references option IDs); new options are inserted; removed options are deleted. The
// delete-existing-then-reinsert-all of S2 would orphan all stored select values on any
// option edit, even a reorder (spec: setOptions re-creation interaction).
func setOptions(s *xorm.Session, defID int64, t string, options []CustomFieldOption) error {
	// only for select/multiselect
	if !isSelectLike(t) {
		// non-select: ensure no option rows exist
		_, err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", defID).Delete(&CustomFieldOption{})
		return err
	}
	// fetch existing options keyed by value
	var existing []CustomFieldOption
	if err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", defID).Find(&existing); err != nil {
		return fmt.Errorf("custom-fields: get options for reconcile: %w", err)
	}
	existingByValue := map[string]*CustomFieldOption{}
	for i := range existing {
		existingByValue[existing[i].Value] = &existing[i]
	}
	seen := map[string]struct{}{}
	for i := range options {
		opt := options[i]
		opt.ID = 0 // don't trust a client-supplied id
		opt.CustomFieldDefinitionID = defID
		seen[opt.Value] = struct{}{}
		if e, ok := existingByValue[opt.Value]; ok {
			// update in place: preserve e.ID, update label + display_order
			e.Label = opt.Label
			e.DisplayOrder = opt.DisplayOrder
			if _, err := s.Table("custom_field_options").ID(e.ID).Cols("label", "display_order").Update(e); err != nil {
				return fmt.Errorf("custom-fields: update option: %w", err)
			}
		} else {
			// new option: insert
			if _, err := s.Table("custom_field_options").Insert(&opt); err != nil {
				return fmt.Errorf("custom-fields: insert option: %w", err)
			}
		}
	}
	// delete options not in the new set
	for _, e := range existing {
		if _, ok := seen[e.Value]; !ok {
			if _, err := s.Table("custom_field_options").ID(e.ID).Delete(&CustomFieldOption{}); err != nil {
				return fmt.Errorf("custom-fields: delete removed option: %w", err)
			}
		}
	}
	return nil
}
```

- [ ] **Step 3: Verify the regression is fixed**

Re-run Step 1's verification: after reordering options, option "a" keeps its id; the stored value reads back as `"a"`, not `null`.

- [ ] **Step 4: Verify S2's definition CRUD still passes (regression)**

```
# S2's ACs: create/update/delete a definition with options; options persist correctly
# (re-run S2's AC#6 verification per the S2 spec's testing table)
```
Expected: S2 behavior intact.

- [ ] **Step 5: Commit**

```
git add main.go
git commit -m "fix(s3): setOptions preserves option IDs on reorder/relabel (N4 fix)"
```

---

### Task 7: Definition-delete cascade (AC#8) + AC#7 (archive/move)

**Files:**
- Modify: `main.go` — extend `CustomFieldDefinition.Delete` (`main.go:435`) to cascade-delete values + child rows by `definition_id` in the same transaction.

**Interfaces:**
- Consumes: S2's `CustomFieldDefinition.Delete` (`main.go:435`); the `custom_field_values` + `custom_field_value_options` tables.
- Produces: definition delete cascades values synchronously. Task 7's AC#8 verification checks the rows are gone.

**Required Skills:** `golang-database` (the two-step cascade with the value-id subquery), `golang-error-handling`.
**Recommended Skills:** `golang-testing` (AC#8 verification with a select-type seed).

- [ ] **Step 1: Write the failing verification (AC#8)**

```
./scripts/run-test-env.sh
# seed a SELECT field + a task + a value (so a custom_field_values row AND a custom_field_value_options child row exist):
# (use the S2 + S3 APIs from Tasks 4)
sqlite3 db/vikunja.db "select count(*) from custom_field_values where custom_field_definition_id=<fieldid>"  # 1
sqlite3 db/vikunja.db "select count(*) from custom_field_value_options"  # >=1
# delete the definition via the S2 API:
curl -s -X DELETE -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions/<fieldid>
sqlite3 db/vikunja.db "select count(*) from custom_field_values where custom_field_definition_id=<fieldid>"  # expect 0
sqlite3 db/vikunja.db "select count(*) from custom_field_value_options"  # expect 0 (child rows gone)
```
Expected (before Task 7): the definition deletes (S2 deletes definition + options + assignment), but the value row + child row persist (orphaned). After Task 7: all gone.

- [ ] **Step 2: Extend `CustomFieldDefinition.Delete`**

In `Delete` (`main.go:435`), add the cascade before the final definition-row delete (or after — same transaction, order within doesn't matter for correctness, but delete child-rows first then values to keep FK-ish consistency):

```go
func (d *CustomFieldDefinition) Delete(s *xorm.Session) error {
	has, err := s.Table("custom_field_definitions").ID(d.ID).Exist(&CustomFieldDefinition{})
	if err != nil {
		return fmt.Errorf("custom-fields: check definition: %w", err)
	}
	if !has {
		return ErrCustomFieldNotFound{ID: d.ID}
	}
	// cascade-delete values (two-step: child rows via the value-id subquery, then values)
	if _, err := s.Table("custom_field_value_options").
		Where("custom_field_value_id IN (SELECT id FROM custom_field_values WHERE custom_field_definition_id = ?)", d.ID).
		Delete(&CustomFieldValueOption{}); err != nil {
		return fmt.Errorf("custom-fields: cascade-delete value options: %w", err)
	}
	if _, err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ?", d.ID).
		Delete(&CustomFieldValue{}); err != nil {
		return fmt.Errorf("custom-fields: cascade-delete values: %w", err)
	}
	// S2's existing cascade (options + assignment + definition)
	if _, err := s.Table("custom_field_options").Where("custom_field_definition_id = ?", d.ID).Delete(&CustomFieldOption{}); err != nil {
		return fmt.Errorf("custom-fields: delete options: %w", err)
	}
	if _, err := s.Table("custom_field_projects").Where("custom_field_definition_id = ?", d.ID).Delete(&CustomFieldProject{}); err != nil {
		return fmt.Errorf("custom-fields: delete assignment: %w", err)
	}
	if _, err := s.Table("custom_field_definitions").ID(d.ID).Delete(&CustomFieldDefinition{}); err != nil {
		return fmt.Errorf("custom-fields: delete definition: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: Verify AC#8**

Re-run Step 1: after deleting the definition, both `custom_field_values` and `custom_field_value_options` counts for that definition are 0.

- [ ] **Step 4: Verify AC#7 (archive/move preserve values)**

```
# archive analog: mark a task done (native API)
curl -s -X PUT -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"id":<taskid>,"project_id":<projectid>,"done":true,...}' http://127.0.0.1:4176/api/v2/tasks/<taskid>
sqlite3 db/vikunja.db "select count(*) from custom_field_values where task_id=<taskid>"  # unchanged
# move: change the task's project_id (native API)
# (create a 2nd project first; PUT the task with the new project_id)
sqlite3 db/vikunja.db "select count(*) from custom_field_values where task_id=<taskid>"  # unchanged
# verify the value reads back (AC#4 may filter it if the field isn't assigned to the new project — that's correct: persist-and-don't-surface)
```
Expected: values persist (count unchanged) across both done-toggle and move. (If the move target project lacks the field assignment, the value is absent from the read response but still in the DB — verify via `sqlite3`.)

- [ ] **Step 5: Commit**

```
git add main.go
git commit -m "feat(s3): cascade-delete values on definition delete (AC#8); verify AC#7"
```

---

### Task 8: Test-harness seeding additions + AC sweep

**Files:**
- Modify: `scripts/run-test-env.sh` — add a custom field definition (whitelist-gated, via the S2 API) and a task, so the AC verification in Tasks 4-7 has data to work against without manual setup.
- Test: re-run all ACs end-to-end against a fresh instance.

**Interfaces:**
- Consumes: the S2 definitions API + the S3 values API + the native tasks API.
- Produces: a test instance that boots with a field definition + a task pre-seeded, so AC verification is repeatable with a single `run-test-env.sh`.

**Required Skills:** `golang-testing` (integration-test-harness design), `golang-continuous-integration` (repeatable local verification).
**Recommended Skills:** (none specific — bash test-harness work).

- [ ] **Step 1: Add the field-definition + task seed to `run-test-env.sh`**

After Step 5 (the project create, `scripts/run-test-env.sh:89-101`), add:

```bash
# ── Step 5b: Seed a custom field definition + a task ────────────
echo "==> Creating a custom field definition (whitelist-gated)..."
FIELD_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v1/plugins/custom-fields/definitions" \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"name":"Priority","type":"integer","project_ids":['"$PROJECT_ID"']}')
FIELD_ID=$(echo "$FIELD_RESPONSE" | jq -r .id)
if [ -z "$FIELD_ID" ] || [ "$FIELD_ID" = "null" ]; then
  echo "WARNING: field create response: $FIELD_RESPONSE"
else
  echo "   Custom field created (id $FIELD_ID)"
fi

echo "==> Creating a task in the test project..."
TASK_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v2/tasks" \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"title":"seeded task","project_id":"'"$PROJECT_ID"'"}')
TASK_ID=$(echo "$TASK_RESPONSE" | jq -r .id)
if [ -z "$TASK_ID" ] || [ "$TASK_ID" = "null" ]; then
  echo "WARNING: task create response: $TASK_RESPONSE"
else
  echo "   Task created (id $TASK_ID)"
fi
export FIELD_ID TASK_ID
```

And add `FIELD_ID`/`TASK_ID` to the result banner (Step 6, `scripts/run-test-env.sh:103-122`).

- [ ] **Step 2: Re-run the full AC sweep against the seeded instance**

```
./scripts/run-test-env.sh
# (the banner now prints JWT, JWT_OTHERUSER, PROJECT_ID, FIELD_ID, TASK_ID)
# Run the AC#1-#8 verifications from Tasks 4-7 using these seeded ids.
```
Expected: all 8 ACs pass (AC#1 map; AC#2 bulk write; AC#3 validation 400s; AC#4 absent field; AC#5 non-member 403; AC#6 task-delete cascade; AC#7 archive/move preserve; AC#8 definition-delete cascade).

- [ ] **Step 3: Commit**

```
git add scripts/run-test-env.sh
git commit -m "test(s3): seed field definition + task in run-test-env.sh; AC sweep"
```

---

### Task 9: Resolution section in the story doc + dependency-graph check-off

**Files:**
- Modify: `docs/stories/S3-task-field-values-api.md` — add a `## Resolution` section per `CLAUDE.local.md`.
- Modify: `docs/stories/story-dependency-graph.md` — check off S3 in the critical path.

**Interfaces:**
- Consumes: all the prior tasks' outcomes + the AC verification results.
- Produces: the durable record (story doc + dependency graph).

**Required Skills:** (none — documentation per the CLAUDE.local.md Story Resolution rules).
**Recommended Skills:** `golang-documentation` (the Resolution section's "How it was built" / "Notable deviations" / "Key decisions" / "What was left open" structure).

- [ ] **Step 1: Add the Resolution section to the S3 story doc**

Per `CLAUDE.local.md`, cover: Status (done + which ACs passed and how verified), How it was built (files/tables/endpoints, keyed decisions), Notable deviations (with reasons), Key decisions (grounded in the spec), What was left open (self-contained items). Flip the front-matter `status:` to `done`.

- [ ] **Step 2: Check off S3 in the dependency graph**

In `docs/stories/story-dependency-graph.md`, change `- [ ] **S3**` to `- [x] **S3**` in the critical-path checklist.

- [ ] **Step 3: Commit**

```
git add docs/stories/S3-task-field-values-api.md docs/stories/story-dependency-graph.md
git commit -m "docs(s3): add resolution section; check off S3 in dependency graph"
```

---

### Task 10: Finish the feature branch (git-flow)

**Files:** (none — git workflow)

**Interfaces:** Consumes: all prior commits on `feature/s3-task-field-values-api`.

**Required Skills:** `git-flow` (MANDATORY — `git flow feature finish`, never plain `git merge`).
**Recommended Skills:** (none).

- [ ] **Step 1: Confirm all ACs pass on a fresh instance**

```
./scripts/run-test-env.sh
# (run the full AC sweep from Task 8 once more)
```

- [ ] **Step 2: Finish the feature branch with git-flow**

```
git flow feature finish s3-task-field-values-api
```
This merges `feature/s3-task-field-values-api` → `develop` and deletes the feature branch. (Per the memory `git-flow-mandatory-not-substitutable`: never substitute `git merge`/`git checkout` for `git flow feature finish`.)

- [ ] **Step 3: Confirm develop is clean + pushed if applicable**

```
git log develop --oneline -3
```

---

## Self-Review

**Spec coverage:** Each spec section → task:
- §1 API surface (AC#1) → Task 4 (read/write handlers + routes)
- §2 Data model (value column, child table, validation) → Task 1 (schema), Task 2 (validation)
- §3 Authorization (AC#5) → Task 3
- §4 Cascade-delete (AC#6) → Task 5
- §5 Read filter/archive/move (AC#4, AC#7) → Task 4 (read filter), Task 7 (AC#7 verify)
- §6 Spikes → Task 0
- §7 Migration → Task 1
- §8 Definition lifecycle (AC#8) → Task 7
- setOptions fix (N4) → Task 6
- Testing strategy / seeding → Task 8
- Resolution → Task 9
All 8 ACs covered: AC#1-#5 (Task 4), AC#6 (Task 5), AC#7 (Task 7), AC#8 (Task 7).

**Placeholder scan:** Task 4 Step 4's `updateOneValueHandler` has an inline `[implementer: factor the validate+insert...]` note — this is a deliberate DRY instruction, not a placeholder (the code is shown inline in `createOneValueHandler`/`bulkUpsertHandler`; the implementer extracts the shared helper). Task 0 Steps 2/4 have `[fill in]` for the recorded outcomes — those are *gated on the spike running*, which is the point of Task 0; the executor fills them from the spike results. No other TBD/TODO.

**Type consistency:** `CustomFieldValueOption` (Task 1), `validateValue`/`resolveOptionIDs` (Task 2), `CanRead/CanCreate/CanUpdate/CanDelete` (Task 3), `readValuesForTask`/`fieldAppliesToProject`/`coerceReadValue`/`valueToMap` (Task 4), `taskDeletedListener` (Task 5), `setOptions` (Task 6), `Delete` cascade (Task 7) — names match across tasks. The `valueItem`/`bulkValueRequest`/`singleValueRequest` structs (Task 4) are used consistently. The `{definition_id: {value, field}}` map shape is consistent between the spec and Task 4's `readValuesForTask`.

Two notes for the implementer (not blockers):
- Task 4's write handlers share the validate-then-insert-then-child-rows block; the implementer should extract a `writeValue` helper to DRY (the plan notes this).
- Task 4 Step 3's skeleton uses `s_safe(c)` then corrects to "open `s` first" — the implementer opens `s := db.NewSession(); defer s.Close()` first in every handler, then `user.GetCurrentUserFromDB(s, c)`.