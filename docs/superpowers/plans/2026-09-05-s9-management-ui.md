# S9 Management UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A whitelisted manager manages custom field definitions at `/api/v1/plugins/custom-fields/ui` — a plugin-served Web Awesome page consuming the S2 API plus three small new endpoints.

**Architecture:** The plugin (single interpreted `main.go`) gains an unauthenticated route pair serving `ui/index.html` + `ui/app.js` from disk, a `management-access` capability endpoint, and two impact endpoints (POST = edit-diff, GET = delete count). The page is framework-free vanilla JS: inert shell → capability check → list / form / dialogs. Zero fork changes; no existing endpoint's behavior changes.

**Tech Stack:** Go (yaegi-interpreted, Vikunja symbol table: echo v5, xorm, viper, db, user), vanilla JS + Web Awesome `@3.12.0` from `ka-f.webawesome.com`, SQLite test instance via Docker.

**Spec:** `docs/superpowers/specs/2026-09-05-s9-management-ui-design.md` (read it first — the plan argues from it; decisions and evidence live there).

## Global Constraints

- ALL Go changes go in `main.go` — yaegi single-file plugin, no new `.go` files.
- No new dependencies. New imports limited to `os` and `path/filepath` (verified present in yaegi's stdlib symbols).
- Conventional Commits. Work happens on `feature/s9-management-ui` (already checked out; spec committed).
- Zero changes to the Vikunja fork. Zero behavior changes to existing S2/S3 endpoints.
- Handler errors: `echo.NewHTTPError` for handler-level (401/400/404/403/500); model-layer errors via `toHTTPError` (plugin-local 9000s convention).
- Whitelist gating follows existing handlers: definitions `Can*` methods (all `IsManager`), invoked per-endpoint like `readOneHandler`/`updateHandler` do.
- Web Awesome pinned: `https://ka-f.webawesome.com/webawesome@3.12.0/` (theme CSS + utilities CSS + `webawesome.loader.js`). Custom elements always get closing tags; component events are `wa-*`.
- UI strings hardcoded English; escape-by-default rendering — `textContent`/DOM element creation only, **never** `innerHTML` with API data.
- Test loop for `main.go` edits: `docker compose -f compose.test.yml restart`, then `docker compose -f compose.test.yml logs | grep -i "loaded plugin"` (expect success, no yaegi errors).
- `./scripts/run-test-env.sh` wipes `db/` fresh each run and prints a JWT for `testuser` (whitelisted); `otheruser` (NOT whitelisted) shares password `testpassword` — `JWT_OTHERUSER` is built by the script; re-login any time with:
  `curl -s -X POST http://127.0.0.1:4176/api/v1/login -H 'Content-Type: application/json' -d '{"username":"otheruser","password":"testpassword"}'`
- Every task's verification is live-instance (no unit harness exists); state claims with the observed output before committing.

---

### Task 1: Serve the UI shell (unauthenticated router + ui/ assets)

**Files:**
- Modify: `main.go` (import block, new section before route registration, `RegisterUnauthenticatedRoutes`, factory)
- Create: `ui/index.html` (minimal — Task 4 replaces its content)
- Create: `ui/app.js` (minimal — Tasks 4–7 replace its content)

**Interfaces:**
- Consumes: existing `CustomFieldsPlugin` struct, `singleton` var.
- Produces: `uiDir() string`, `uiIndexHandler(c *echo.Context) error`, `uiAppJSHandler(c *echo.Context) error`, `(p *CustomFieldsPlugin) RegisterUnauthenticatedRoutes(g *echo.Group)`, `NewUnauthenticatedRouterPlugin() plugins.UnauthenticatedRouterPlugin`. Later tasks rely on the URLS `/api/v1/plugins/custom-fields/ui` and `.../ui/app.js` existing.

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** golang-code-style, golang-error-handling, designing-with-upstream-precedent, golang-safety

- [ ] **Step 1: Add imports**

In `main.go`'s import block (top of file), add `"os"` after `"net/url"` and `"path/filepath"` after `"os"` — the stdlib group becomes:

```go
import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
```

(the remainder of the block — `code.vikunja.io/api/...`, echo, viper, xorm — is unchanged)

- [ ] **Step 2: Add the UI-serving section**

Insert directly above the `// RegisterAuthenticatedRoutes mounts...` comment (near the end of `main.go`):

```go
// ── Management UI (S9). The shell is served WITHOUT the JWT middleware because
// browser navigation carries no Authorization header and Vikunja has no session
// cookie for API auth; the page authenticates its own API calls from
// localStorage['token'] (S9 spec: "Auth model"). The shell is inert static
// HTML/JS — the same trust model as Vikunja's own SPA index.html.

// uiDir resolves the plugin's ui asset directory: <plugins.dir>/custom-fields/ui.
// plugins.dir is absolute in the test and deployment configurations; if a
// relative value is ever configured, resolve it against service.rootpath
// (config.go:54 — the key Vikunja itself anchors relative paths to).
func uiDir() string {
	dir := viper.GetString("plugins.dir")
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(viper.GetString("service.rootpath"), dir)
	}
	return filepath.Join(dir, "custom-fields", "ui")
}

func uiIndexHandler(c *echo.Context) error {
	b, err := os.ReadFile(filepath.Join(uiDir(), "index.html"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "management UI assets not found")
	}
	return c.Blob(http.StatusOK, "text/html; charset=utf-8", b)
}

func uiAppJSHandler(c *echo.Context) error {
	b, err := os.ReadFile(filepath.Join(uiDir(), "app.js"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "management UI assets not found")
	}
	return c.Blob(http.StatusOK, "text/javascript; charset=utf-8", b)
}
```

- [ ] **Step 3: Implement the unauthenticated router interface**

The loader looks up typed factories (`pkg/plugins/yaegi/loader.go`) — yaegi wraps return values per declared type, so a separate factory is required (same pattern as `NewAuthenticatedRouterPlugin`). Add the method next to `RegisterAuthenticatedRoutes` and the factory next to `NewAuthenticatedRouterPlugin`:

```go
// RegisterUnauthenticatedRoutes mounts the management UI shell on the
// /api/v1/plugins group's no-JWT twin (pkg/routes/routes.go:987).
func (p *CustomFieldsPlugin) RegisterUnauthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/ui", uiIndexHandler)
	g.GET("/custom-fields/ui/app.js", uiAppJSHandler)
}
```

```go
// NewUnauthenticatedRouterPlugin is the typed factory yaegi's loader requires
// for the no-JWT route group (see NewAuthenticatedRouterPlugin).
func NewUnauthenticatedRouterPlugin() plugins.UnauthenticatedRouterPlugin { return singleton }
```

- [ ] **Step 4: Create minimal assets**

`ui/index.html`:

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Custom Fields — Management</title>
</head>
<body>
<p>Management UI assets are being served. The interface arrives with the next tasks.</p>
</body>
</html>
```

`ui/app.js`:

```js
// S9 management UI. Stub — proves asset serving; replaced from Task 4 on.
console.debug('[custom-fields] management UI assets loaded');
```

- [ ] **Step 5: Verify serving live**

```bash
chmod a+r ui/index.html ui/app.js main.go   # container readability (CLAUDE.md troubleshooting)
docker compose -f compose.test.yml restart
docker compose -f compose.test.yml logs | grep -i "loaded plugin"
curl -si http://127.0.0.1:4176/api/v1/plugins/custom-fields/ui | head -8
curl -si http://127.0.0.1:4176/api/v1/plugins/custom-fields/ui/app.js | head -8
```

Expected: plugin loads without yaegi errors; first URL → `HTTP/1.1 200 OK` with `Content-Type: text/html; charset=utf-8` and the page body; second → 200 with `Content-Type: text/javascript; charset=utf-8`. Both must work **without** an Authorization header.

- [ ] **Step 6: Commit**

```bash
git add main.go ui/index.html ui/app.js
git commit -m "feat(s9): serve management UI shell from unauthenticated plugin routes"
```

---

### Task 2: management-access capability endpoint

**Files:**
- Modify: `main.go` (new handler + one route registration)

**Interfaces:**
- Consumes: `user.GetCurrentUser`, `IsManager` (`main.go:537`).
- Produces: `managementAccessHandler(c *echo.Context) error`; route `GET /api/v1/plugins/custom-fields/management-access` → `200 {"is_manager": bool}`. Task 4's `boot()` consumes this.

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** golang-code-style, designing-with-upstream-precedent

- [ ] **Step 1: Add the handler**

Insert after `uiAppJSHandler`:

```go
// managementAccessHandler answers "is the current user on the management
// whitelist?". Always 200 for any authenticated user — it is the gate check,
// not a gated operation (S9 spec: "Backend additions" #1).
func managementAccessHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	return c.JSON(http.StatusOK, map[string]bool{"is_manager": IsManager(u.Username)})
}
```

- [ ] **Step 2: Register the route**

In `RegisterAuthenticatedRoutes`, add as the first line:

```go
	g.GET("/custom-fields/management-access", managementAccessHandler)
```

- [ ] **Step 3: Verify live**

```bash
docker compose -f compose.test.yml restart
JWT=$(./scripts/run-test-env.sh 2>/dev/null | grep -o 'eyJ[^"]*' | head -1)  # or copy from the script's output
curl -s -H "Authorization: Bearer $JWT" http://127.0.0.1:4176/api/v1/plugins/custom-fields/management-access
JWT_OTHER=$(curl -s -X POST http://127.0.0.1:4176/api/v1/login -H 'Content-Type: application/json' -d '{"username":"otheruser","password":"testpassword"}' | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s -H "Authorization: Bearer $JWT_OTHER" http://127.0.0.1:4176/api/v1/plugins/custom-fields/management-access
curl -s http://127.0.0.1:4176/api/v1/plugins/custom-fields/management-access
```

Expected: `{"is_manager":true}` for testuser; `{"is_manager":false}` for otheruser; 401 JSON envelope with no token.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat(s9): add management-access capability endpoint"
```

---

### Task 3: Impact endpoints (edit-diff + delete count)

**Files:**
- Modify: `main.go` (three helpers, two handlers, two route registrations)

**Interfaces:**
- Consumes: `definitionRequest` (`main.go:767`), `c.Bind`, `validateProjectIDList`, `d.ReadOne(s)` (returns `(*CustomFieldDefinition, []CustomFieldOption, []int64, error)`), `CanUpdate`/`CanRead`, `isSelectLike` (`main.go:226`), `toHTTPError`.
- Produces: `countValuesForDefinition(s *xorm.Session, defID int64) (int64, error)`, `countValuesUsingRemovedOptions(s *xorm.Session, removedIDs []int64) (int64, error)`, `countValuesOutOfRange(s *xorm.Session, defID int64, fc FieldConfig) (int64, error)`, `computeImpact(s *xorm.Session, cur *CustomFieldDefinition, curOpts []CustomFieldOption, cand *definitionRequest) (int64, error)`, `editImpactHandler`, `deleteImpactHandler`; routes `POST|GET /api/v1/plugins/custom-fields/definitions/:id/impact` → `200 {"affected_values": n}`. Task 6/7 consume these.

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** golang-code-style, golang-safety, golang-database, designing-with-upstream-precedent

- [ ] **Step 1: Add the counting helpers**

Insert after `deleteHandler` (near the other definition handlers):

```go
// countValuesForDefinition counts stored values for one definition — the
// delete-impact total and the type-change edit impact (all values are
// potentially invalid when the type changes).
func countValuesForDefinition(s *xorm.Session, defID int64) (int64, error) {
	return s.Table("custom_field_values").
		Where("custom_field_definition_id = ?", defID).
		Count(&CustomFieldValue{})
}

// countValuesUsingRemovedOptions counts distinct stored values of the
// definition that reference any of the removed option IDs. Option IDs are
// definition-scoped, so no join back to definitions is needed; distinct counts
// stored VALUES (a multiselect value holding two removed options counts once —
// AC#9's "count of affected values"). Both select and multiselect store option
// linkage exclusively in custom_field_value_options (writeValue sets Value=""
// for all select-like types).
func countValuesUsingRemovedOptions(s *xorm.Session, removedIDs []int64) (int64, error) {
	if len(removedIDs) == 0 {
		return 0, nil
	}
	var rows []CustomFieldValueOption
	if err := s.Table("custom_field_value_options").
		Where("custom_field_option_id IN (?)", removedIDs).
		Find(&rows); err != nil {
		return 0, fmt.Errorf("custom-fields: count removed-option values: %w", err)
	}
	distinct := map[int64]struct{}{}
	for _, r := range rows {
		distinct[r.CustomFieldValueID] = struct{}{}
	}
	return int64(len(distinct)), nil
}

// countValuesOutOfRange counts values of an integer/decimal field that parse
// to a number outside the candidate's [min,max]. Unparseable stored values are
// not counted here — a type change already counts everything (S9 spec table).
func countValuesOutOfRange(s *xorm.Session, defID int64, fc FieldConfig) (int64, error) {
	var vals []CustomFieldValue
	if err := s.Table("custom_field_values").
		Where("custom_field_definition_id = ?", defID).
		Find(&vals); err != nil {
		return 0, fmt.Errorf("custom-fields: load values for range check: %w", err)
	}
	var n int64
	for _, v := range vals {
		f, err := strconv.ParseFloat(strings.TrimSpace(v.Value), 64)
		if err != nil {
			continue
		}
		if fc.Min != nil && f < *fc.Min {
			n++
			continue
		}
		if fc.Max != nil && f > *fc.Max {
			n++
		}
	}
	return n, nil
}
```

- [ ] **Step 2: Add the diff logic**

```go
// computeImpact returns how many stored values the candidate edit would
// invalidate — the diff semantics table in the S9 spec, verbatim. curOpts are
// the CURRENT option rows (their DB IDs are what value rows reference).
func computeImpact(s *xorm.Session, cur *CustomFieldDefinition, curOpts []CustomFieldOption, cand *definitionRequest) (int64, error) {
	if cur.Type != cand.Type {
		return countValuesForDefinition(s, cur.ID)
	}
	if isSelectLike(cur.Type) {
		candVals := map[string]struct{}{}
		for _, o := range cand.Options {
			candVals[o.Value] = struct{}{}
		}
		var removedIDs []int64
		for _, o := range curOpts {
			if _, ok := candVals[o.Value]; !ok {
				removedIDs = append(removedIDs, o.ID)
			}
		}
		return countValuesUsingRemovedOptions(s, removedIDs)
	}
	if cur.Type == "integer" || cur.Type == "decimal" {
		tightened := false
		if cur.FieldConfig.Min == nil && cand.FieldConfig.Min != nil {
			tightened = true
		}
		if cur.FieldConfig.Max == nil && cand.FieldConfig.Max != nil {
			tightened = true
		}
		if !tightened && cur.FieldConfig.Min != nil && cand.FieldConfig.Min != nil && *cand.FieldConfig.Min > *cur.FieldConfig.Min {
			tightened = true
		}
		if !tightened && cur.FieldConfig.Max != nil && cand.FieldConfig.Max != nil && *cand.FieldConfig.Max < *cur.FieldConfig.Max {
			tightened = true
		}
		if tightened {
			return countValuesOutOfRange(s, cur.ID, cand.FieldConfig)
		}
	}
	return 0, nil
}
```

- [ ] **Step 3: Add the handlers**

```go
// editImpactHandler previews how many stored values a candidate PUT would
// invalidate. Whitelist-gated via CanUpdate — the same gate as the update whose
// result is being previewed. The body is the candidate definition, the exact
// JSON the subsequent PUT will send (S9 spec: server-side diffing is
// authoritative; the UI must not re-implement validation semantics).
func editImpactHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	var req definitionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := validateProjectIDList(req.ProjectIDs); err != nil {
		return toHTTPError(err)
	}
	d := &CustomFieldDefinition{ID: id}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanUpdate(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	cur, curOpts, _, err := d.ReadOne(s)
	if err != nil {
		return toHTTPError(err)
	}
	n, err := computeImpact(s, cur, curOpts, &req)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]int64{"affected_values": n})
}

// deleteImpactHandler previews the delete cascade: Delete destroys all stored
// values (verified), so the count is the total. Whitelist-gated via CanRead —
// the same gate as readOneHandler; the count is management-surface usage data.
func deleteImpactHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	d := &CustomFieldDefinition{ID: id}
	s := db.NewSession()
	defer s.Close()
	ok, err := d.CanRead(s, u)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "not permitted to manage custom fields")
	}
	n, err := countValuesForDefinition(s, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]int64{"affected_values": n})
}
```

- [ ] **Step 4: Register the routes**

In `RegisterAuthenticatedRoutes`, directly after the definitions block (after the `g.DELETE("/custom-fields/definitions/:id", deleteHandler)` line):

```go
	// S9 impact previews. Same path, two verbs: POST = edit-diff (candidate
	// body), GET = delete cascade count.
	g.POST("/custom-fields/definitions/:id/impact", editImpactHandler)
	g.GET("/custom-fields/definitions/:id/impact", deleteImpactHandler)
```

- [ ] **Step 5: Verify live**

```bash
docker compose -f compose.test.yml restart
JWT=$(./scripts/run-test-env.sh 2>/dev/null | grep -o 'eyJ[^"]*' | head -1)
BASE=http://127.0.0.1:4176/api/v1
# a project and a task to hang values on
PID=$(curl -s -X POST $BASE/projects -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"CF Impact"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
# three tasks — values are UNIQUE(field, task), so one task per value
T1=$(curl -s -X POST $BASE/projects/$PID/tasks -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"t1"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
T2=$(curl -s -X POST $BASE/projects/$PID/tasks -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"t2"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
T3=$(curl -s -X POST $BASE/projects/$PID/tasks -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"t3"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
# an integer field with range, three values (2 inside, one below a later min)
DID=$(curl -s -X POST $BASE/plugins/custom-fields/definitions -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"name":"Cost","type":"integer","field_config":{},"display_order":0,"project_ids":[]}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
curl -s -X POST $BASE/plugins/custom-fields/tasks/$T1/custom-fields -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "[{\"custom_field_definition_id\":$DID,\"value\":3}]"
curl -s -X POST $BASE/plugins/custom-fields/tasks/$T2/custom-fields -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "[{\"custom_field_definition_id\":$DID,\"value\":7}]"
curl -s -X POST $BASE/plugins/custom-fields/tasks/$T3/custom-fields -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "[{\"custom_field_definition_id\":$DID,\"value\":9}]"
# rename only -> 0
curl -s -X POST $BASE/plugins/custom-fields/definitions/$DID/impact -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"name":"Cost2","type":"integer","field_config":{},"display_order":0,"project_ids":[]}'
# tighten min to 5 -> the value 3 is out of range -> 1
curl -s -X POST $BASE/plugins/custom-fields/definitions/$DID/impact -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"name":"Cost2","type":"integer","field_config":{"min":5},"display_order":0,"project_ids":[]}'
# type change -> all values -> 3
curl -s -X POST $BASE/plugins/custom-fields/definitions/$DID/impact -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"name":"Cost2","type":"text","field_config":{},"display_order":0,"project_ids":[]}'
# delete impact -> total -> 3
curl -s $BASE/plugins/custom-fields/definitions/$DID/impact -H "Authorization: Bearer $JWT"
```

Expected, in order: `{"affected_values":0}`, `{"affected_values":1}`, `{"affected_values":3}`, `{"affected_values":3}`.

Then the select case (fresh script run for a clean slate):

```bash
JWT=$(./scripts/run-test-env.sh 2>/dev/null | grep -o 'eyJ[^"]*' | head -1)
BASE=http://127.0.0.1:4176/api/v1
PID=$(curl -s -X POST $BASE/projects -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"CF Impact"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
T1=$(curl -s -X POST $BASE/projects/$PID/tasks -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"t1"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
T2=$(curl -s -X POST $BASE/projects/$PID/tasks -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"t2"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
DID=$(curl -s -X POST $BASE/plugins/custom-fields/definitions -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"name":"Status","type":"select","field_config":{},"display_order":0,"options":[{"value":"a","label":"A","display_order":0},{"value":"b","label":"B","display_order":1},{"value":"c","label":"C","display_order":2}],"project_ids":[]}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
curl -s -X POST $BASE/plugins/custom-fields/tasks/$T1/custom-fields -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "[{\"custom_field_definition_id\":$DID,\"value\":\"a\"}]"
curl -s -X POST $BASE/plugins/custom-fields/tasks/$T2/custom-fields -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "[{\"custom_field_definition_id\":$DID,\"value\":\"b\"}]"
# drop option "a" (candidate keeps b, c) -> exactly 1 affected value
curl -s -X POST $BASE/plugins/custom-fields/definitions/$DID/impact -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"name":"Status","type":"select","field_config":{},"display_order":0,"options":[{"value":"b","label":"B","display_order":0},{"value":"c","label":"C","display_order":1}],"project_ids":[]}'
# non-whitelisted caller -> 403 on both verbs
JWT_OTHER=$(curl -s -X POST http://127.0.0.1:4176/api/v1/login -H 'Content-Type: application/json' -d '{"username":"otheruser","password":"testpassword"}' | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s -o /dev/null -w '%{http_code}\n' -X POST $BASE/plugins/custom-fields/definitions/$DID/impact -H "Authorization: Bearer $JWT_OTHER" -H 'Content-Type: application/json' -d '{"name":"Status","type":"select","field_config":{},"display_order":0,"options":[],"project_ids":[]}'
curl -s -o /dev/null -w '%{http_code}\n' $BASE/plugins/custom-fields/definitions/$DID/impact -H "Authorization: Bearer $JWT_OTHER"
```

Expected: `{"affected_values":1}`, then `403`, `403`.

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "feat(s9): add edit and delete impact preview endpoints"
```

---

### Task 4: UI shell and auth states (full page + fetch layer)

**Files:**
- Modify: `ui/index.html` (replace content — this is the FINAL page markup; Tasks 5–7 only add JS)
- Modify: `ui/app.js` (replace content — the fetch layer + state machine; Tasks 5–7 extend it)

**Interfaces:**
- Consumes: `GET /management-access` (Task 2), the Task 1 asset URLs.
- Produces (JS globals later tasks extend): `BASE`, `token()`, `api(path, opts)`, `fetchJSONv1(path)`, `show(stateId)`, `boot()`. DOM ids later tasks rely on: `state-loading`, `state-not-authorized`, `state-expired`, `state-app`, `btn-new`, `list`, `list-error`, `form-view`, `f-name`, `err-name`, `f-description`, `f-order`, `f-type`, `err-type`, `block-range`, `f-min`, `f-max`, `block-options`, `options-rows`, `btn-add-option`, `err-options`, `f-required`, `f-default`, `f-api-only`, `f-all-projects`, `f-projects`, `f-project-id`, `btn-add-project`, `err-assignment`, `form-banner`, `btn-save`, `btn-cancel`, `dialog-confirm`, `dialog-body`, `dialog-ok`, `dialog-cancel`.

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** webawesome, webawesome-design, server-side-rendering-without-a-vdom

- [ ] **Step 1: Replace `ui/index.html` with the final page**

```html
<!doctype html>
<html lang="en" class="wa-theme-default">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Custom Fields — Management</title>
<link rel="stylesheet" href="https://ka-f.webawesome.com/webawesome@3.12.0/styles/themes/default.css">
<link rel="stylesheet" href="https://ka-f.webawesome.com/webawesome@3.12.0/styles/utilities.css">
<script type="module" src="https://ka-f.webawesome.com/webawesome@3.12.0/webawesome.loader.js"></script>
<style>
  html, body { margin: 0; padding: 0; }
  main { max-width: 42rem; margin: 0 auto; padding: 1rem; }
  .hidden { display: none !important; }
  wa-card { margin-block-end: 0.75rem; }
  h1 { font-size: 1.3rem; margin: 0; }
  h3 { margin: 0; }
  .meta { margin: 0.15rem 0; font-size: 0.9em; }
  .meta strong { font-weight: 600; }
  .field-block { margin-block-end: 0.9rem; }
  .field-error { color: var(--wa-color-danger-on-normal, #b00020); display: block; margin-block-start: 0.25rem; }
  wa-input, wa-select, wa-number-input, wa-textarea { display: block; width: 100%; }
  .opt-row { display: flex; gap: 0.4rem; align-items: center; margin-block-end: 0.4rem; }
  .opt-row wa-input { width: auto; flex: 1 1 0; }
</style>
</head>
<body>
<main>
  <section id="state-loading"><wa-spinner></wa-spinner> <p>Loading…</p></section>

  <section id="state-not-authorized" class="hidden">
    <wa-callout variant="danger">
      <strong>Not authorized.</strong> Your user is not on the custom-fields management whitelist.
    </wa-callout>
  </section>

  <section id="state-expired" class="hidden">
    <wa-callout variant="warning">
      <strong>Session expired or missing.</strong>
      Open <a href="/">Vikunja</a>, log in again, then return here.
    </wa-callout>
  </section>

  <section id="state-app" class="hidden">
    <header class="wa-split" style="align-items:center; margin-block-end:1rem;">
      <h1>Custom Fields</h1>
      <wa-button variant="brand" id="btn-new"><wa-icon name="plus" slot="start"></wa-icon>New field</wa-button>
    </header>
    <wa-callout id="list-error" variant="danger" class="hidden"></wa-callout>
    <div id="list"></div>

    <section id="form-view" class="hidden">
      <header class="wa-split" style="align-items:center; margin-block-end:1rem;">
        <h1 id="form-title">New field</h1>
      </header>
      <wa-callout id="form-banner" variant="danger" class="hidden"></wa-callout>

      <div class="field-block">
        <wa-input id="f-name" label="Name"></wa-input>
        <small id="err-name" class="field-error hidden"></small>
      </div>
      <div class="field-block">
        <wa-input id="f-description" label="Description" placeholder="Shown as a hint on tasks (optional)"></wa-input>
      </div>
      <div class="field-block">
        <wa-number-input id="f-order" label="Display order" placeholder="0"></wa-number-input>
      </div>
      <div class="field-block">
        <wa-select id="f-type" label="Type">
          <wa-option value="text">Single-line text</wa-option>
          <wa-option value="textarea">Multi-line text</wa-option>
          <wa-option value="integer">Integer</wa-option>
          <wa-option value="decimal">Decimal</wa-option>
          <wa-option value="date">Date</wa-option>
          <wa-option value="datetime">Datetime</wa-option>
          <wa-option value="select">Select (single)</wa-option>
          <wa-option value="multiselect">Multi-select</wa-option>
          <wa-option value="checkbox">Checkbox</wa-option>
          <wa-option value="url">URL</wa-option>
        </wa-select>
        <small id="err-type" class="field-error hidden"></small>
      </div>

      <div id="block-range" class="field-block hidden">
        <div class="wa-split">
          <wa-number-input id="f-min" label="Min (empty = unset)"></wa-number-input>
          <wa-number-input id="f-max" label="Max (empty = unset)"></wa-number-input>
        </div>
      </div>

      <div id="block-options" class="field-block hidden">
        <p class="meta"><strong>Options</strong> (order = display order on save)</p>
        <div id="options-rows"></div>
        <wa-button id="btn-add-option" appearance="plain"><wa-icon name="plus" slot="start"></wa-icon>Add option</wa-button>
        <small id="err-options" class="field-error hidden"></small>
      </div>

      <div class="field-block">
        <wa-checkbox id="f-required">Required</wa-checkbox>
        <wa-checkbox id="f-api-only">API-only (display-only in the task UI)</wa-checkbox>
      </div>
      <div class="field-block">
        <wa-input id="f-default" label="Default value (optional, stored as metadata)"></wa-input>
      </div>

      <div class="field-block">
        <wa-checkbox id="f-all-projects" checked>Assign to all projects</wa-checkbox>
        <div id="block-assignment" class="hidden">
          <wa-select id="f-projects" label="Projects" multiple></wa-select>
          <div class="wa-split" style="margin-block-start:0.4rem;">
            <wa-input id="f-project-id" label="Add project by ID" placeholder="e.g. 42"></wa-input>
            <wa-button id="btn-add-project" appearance="plain">Add</wa-button>
          </div>
          <small class="meta">Assignment validates existence only — you can target projects your own account cannot see.</small>
          <small id="err-assignment" class="field-error hidden"></small>
        </div>
      </div>

      <div class="wa-split" style="margin-block-start:1.25rem;">
        <wa-button variant="brand" id="btn-save">Save</wa-button>
        <wa-button appearance="plain" id="btn-cancel">Cancel</wa-button>
      </div>
    </section>
  </section>
</main>

<wa-dialog id="dialog-confirm" label="Confirm">
  <div id="dialog-body"></div>
  <div slot="footer" class="wa-split">
    <wa-button id="dialog-cancel" appearance="plain">Cancel</wa-button>
    <wa-button id="dialog-ok" variant="brand">Confirm</wa-button>
  </div>
</wa-dialog>

<script src="app.js"></script>
</body>
</html>
```

- [ ] **Step 2: Replace `ui/app.js` with the fetch layer and state machine**

```js
// S9 management UI — vanilla JS, no build. Escape-by-default: all rendering via
// textContent/DOM APIs, never innerHTML with API data.
const BASE = '/api/v1/plugins/custom-fields';

const token = () => localStorage.getItem('token') || '';
const $ = (id) => document.getElementById(id);

function show(stateId) {
  for (const s of ['state-loading', 'state-not-authorized', 'state-expired', 'state-app']) {
    $(s).classList.toggle('hidden', s !== stateId);
  }
}

// api() talks to the plugin API with the SPA's localStorage JWT. 401 → the
// expired view (we deliberately do NOT replicate the SPA's cookie refresh —
// S9 spec). Errors normalize to Error{status, message}.
async function api(path, opts = {}) {
  const res = await fetch(BASE + path, {
    ...opts,
    headers: {
      Authorization: 'Bearer ' + token(),
      ...(opts.body ? { 'Content-Type': 'application/json' } : {}),
      ...(opts.headers || {}),
    },
  });
  if (res.status === 401) {
    show('state-expired');
    const e = new Error('unauthorized');
    e.status = 401;
    throw e;
  }
  if (res.status === 204) return null;
  const text = await res.text();
  let body = null;
  if (text) {
    try { body = JSON.parse(text); } catch { body = { message: text }; }
  }
  if (!res.ok) {
    const e = new Error((body && body.message) || ('HTTP ' + res.status));
    e.status = res.status;
    throw e;
  }
  return body;
}

// fetchJSONv1 talks to the MAIN Vikunja API (project picker) — same token.
async function fetchJSONv1(path) {
  const res = await fetch('/api/v1' + path, {
    headers: { Authorization: 'Bearer ' + token() },
  });
  if (!res.ok) throw new Error('HTTP ' + res.status);
  return res.json();
}

// List rendering arrives with the list-view task; the app state is the
// deliverable here.
async function boot() {
  if (!token()) {
    show('state-expired');
    return;
  }
  try {
    const { is_manager } = await api('/management-access');
    if (!is_manager) {
      show('state-not-authorized');
      return;
    }
    show('state-app');
  } catch (e) {
    if (e.status === 401) return; // expired view already shown by api()
    console.error(e);
    const box = document.createElement('wa-callout');
    box.setAttribute('variant', 'danger');
    box.textContent = 'Failed to load: ' + e.message;
    $('state-loading').replaceChildren(box);
  }
}

boot();
```

- [ ] **Step 3: Verify live**

```bash
chmod a+r ui/index.html ui/app.js
docker compose -f compose.test.yml restart
JWT=$(./scripts/run-test-env.sh 2>/dev/null | grep -o 'eyJ[^"]*' | head -1)
```

In a browser (needs internet for the Web Awesome CDN): open `http://127.0.0.1:4176/api/v1/plugins/custom-fields/ui`.
- Logged into Vikunja as `testuser` in the same browser (or with a valid `token` in localStorage) → app state: "Custom Fields" header + "New field" button, empty list.
- With localStorage `token` set to garbage (DevTools: `localStorage.setItem('token','garbage')`, reload) → expired view.
- Logged in as `otheruser` (or `localStorage.setItem('token','<otheruser JWT>')`) → not-authorized view.
- Web Awesome components render styled (theme loaded); no console errors besides none.

- [ ] **Step 4: Commit**

```bash
git add ui/index.html ui/app.js
git commit -m "feat(s9): management UI shell with capability-gated auth states"
```

---

### Task 5: List view (stacked cards, N+1 ReadOne for relations)

**Files:**
- Modify: `ui/app.js` (add `loadList`/`cardFor`/`metaLine`; extend `boot()`)

**Interfaces:**
- Consumes: `GET /definitions` (fields only — `definitionFieldsMap`), `GET /definitions/:id` (relations: `options`, `project_ids`), `show()`, `api()`.
- Produces: `loadList()`, `cardFor(item)`, `metaLine(label, value)`. `boot()` now calls `loadList()` after showing the app state. Task 6 extends `cardFor`'s footer with an Edit button; Task 7 adds Delete.

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** webawesome, server-side-rendering-without-a-vdom

- [ ] **Step 1: Add the list functions**

Append to `ui/app.js` (above `boot()`):

```js
// The list endpoint returns definition fields ONLY (no relations —
// definitionFieldsMap). Cards need options + project_ids, so each definition
// is followed by a ReadOne (N+1 over a handful of definitions — S9 spec).
async function loadList() {
  const defs = await api('/definitions');
  const items = await Promise.all(defs.map(async (d) => {
    try {
      const full = await api('/definitions/' + d.id);
      return { ...d, options: full.options || [], project_ids: full.project_ids || [] };
    } catch (e) {
      return { ...d, options: [], project_ids: [], relFailed: true };
    }
  }));
  items.sort((a, b) => (a.display_order || 0) - (b.display_order || 0));
  $('list').replaceChildren(...items.map(cardFor));
}

function metaLine(label, value) {
  const p = document.createElement('p');
  p.className = 'meta';
  const b = document.createElement('strong');
  b.textContent = label + ': ';
  p.appendChild(b);
  p.appendChild(document.createTextNode(value));
  return p;
}

function cardFor(item) {
  const card = document.createElement('wa-card');
  const header = document.createElement('div');
  header.className = 'wa-split';
  header.style.alignItems = 'center';
  const h = document.createElement('h3');
  h.textContent = item.name;
  const badge = document.createElement('wa-badge');
  badge.setAttribute('variant', 'neutral');
  badge.textContent = item.type;
  header.append(h, badge);

  const body = document.createElement('div');
  const fc = item.field_config || {};
  body.append(metaLine('Required', fc.required ? 'yes' : 'no'));
  if (fc.min != null || fc.max != null) {
    body.append(metaLine('Range', (fc.min ?? '−∞') + ' … ' + (fc.max ?? '∞')));
  }
  if (item.type === 'select' || item.type === 'multiselect') {
    body.append(metaLine('Options', String((item.options || []).length)));
  }
  body.append(metaLine('Assigned to', (item.project_ids || []).length === 0 ? 'All projects' : item.project_ids.length + ' project(s)'));
  if (fc.is_api_only) body.append(metaLine('API-only', 'yes'));
  if (item.description) body.append(metaLine('Description', item.description));
  if (item.relFailed) body.append(metaLine('Warning', 'details unavailable (read failed)'));

  const footer = document.createElement('div');
  footer.className = 'wa-split';
  footer.style.marginTop = '0.5rem';
  // Action buttons are added by later tasks (Edit in the form task, Delete in
  // the delete task) — the footer is intentionally empty here.

  card.append(header, body, footer);
  return card;
}
```

- [ ] **Step 2: Wire the list into boot**

Replace `show('state-app');` in `boot()` with:

```js
    show('state-app');
    await loadList();
```

- [ ] **Step 3: Verify live**

```bash
docker compose -f compose.test.yml restart
JWT=$(./scripts/run-test-env.sh 2>/dev/null | grep -o 'eyJ[^"]*' | head -1)
BASE=http://127.0.0.1:4176/api/v1
for i in 1 2; do curl -s -X POST $BASE/plugins/custom-fields/definitions -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "{\"name\":\"Field $i\",\"type\":\"text\",\"field_config\":{},\"display_order\":$i,\"project_ids\":[]}"; echo; done
```

Browser as `testuser`: two stacked cards, name + type badge, "Required: no", "Assigned to: All projects", ordered by display_order. One select-type definition with options and one assigned to a project renders the Options count and "N project(s)" line. Reload after `docker compose -f compose.test.yml restart` still renders (assets re-served, `no-store`).

- [ ] **Step 4: Commit**

```bash
git add ui/app.js
git commit -m "feat(s9): management UI list view with per-card relations"
```

---

### Task 6: Form — create, edit, drafts, impact dialog, inline validation

**Files:**
- Modify: `ui/app.js` (add form logic; extend `cardFor` footer with Edit)

**Interfaces:**
- Consumes: `POST|PUT /definitions`, `POST /definitions/:id/impact` (Task 3), `fetchJSONv1('/projects')`, DOM ids from Task 4.
- Produces: `openForm(id)`, `closeForm()`, `fillForm()`, `fillProjects(selectedIds)`, `updateBlocks()`, `collectForm()`, `collectOptions()`, `collectProjectIds()`, `parseNum(v)`, `saveForm()`, `showFormError(e)`, `clearErrors()`, `setFieldError(id, msg)`, `addOptionRow(opt)`, `askConfirm(label, buildBody, okLabel, okVariant)`, `draftKey()`, `saveDraft(candidate)`, `restoreDraft()`, `clearDraft()`, and `formId`/`formDef` module state. Task 7 consumes `askConfirm`.

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** webawesome, server-side-rendering-without-a-vdom, golang-safety

- [ ] **Step 1: Add form state, open/close, and fill logic**

Append to `ui/app.js`:

```js
// ── Form (create + edit). formId === null → create.
let formId = null;
let formDef = null;

function draftKey() {
  return formId === null ? 'cf-mgmt-draft-new' : 'cf-mgmt-draft-' + formId;
}
function saveDraft(candidate) {
  try { localStorage.setItem(draftKey(), JSON.stringify(candidate)); } catch {}
}
function restoreDraft() {
  try {
    const raw = localStorage.getItem(draftKey());
    if (raw) return JSON.parse(raw);
  } catch {}
  return null;
}
function clearDraft() {
  try { localStorage.removeItem(draftKey()); } catch {}
}

async function openForm(id) {
  clearErrors();
  formId = id;
  if (id === null) {
    formDef = { name: '', type: 'text', description: '', display_order: 0, field_config: {}, options: [], project_ids: [] };
    $('form-title').textContent = 'New field';
  } else {
    formDef = await api('/definitions/' + id);
    $('form-title').textContent = 'Edit field';
  }
  await fillForm();
  $('list').classList.add('hidden');
  $('btn-new').classList.add('hidden');
  $('form-view').classList.remove('hidden');
}

function closeForm() {
  $('form-view').classList.add('hidden');
  $('list').classList.remove('hidden');
  $('btn-new').classList.remove('hidden');
}

function parseNum(v) {
  const s = String(v ?? '').trim();
  if (s === '') return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

async function fillForm() {
  const draft = restoreDraft();
  const c = draft || {
    name: formDef.name || '',
    type: formDef.type || 'text',
    description: formDef.description || '',
    display_order: formDef.display_order || 0,
    field_config: formDef.field_config || {},
    options: (formDef.options || []).map((o) => ({ value: o.value, label: o.label })),
    project_ids: formDef.project_ids || [],
  };
  $('f-name').value = c.name;
  $('f-description').value = c.description;
  $('f-order').value = c.display_order;
  $('f-type').value = c.type;
  $('f-required').checked = !!(c.field_config && c.field_config.required);
  $('f-api-only').checked = !!(c.field_config && c.field_config.is_api_only);
  $('f-default').value = (c.field_config && c.field_config.default) || '';
  $('f-min').value = (c.field_config && c.field_config.min) ?? '';
  $('f-max').value = (c.field_config && c.field_config.max) ?? '';
  const allProjects = (c.project_ids || []).length === 0;
  $('f-all-projects').checked = allProjects;
  $('block-assignment').classList.toggle('hidden', allProjects);
  const rows = $('options-rows');
  rows.replaceChildren();
  for (const o of c.options || []) addOptionRow(o);
  await fillProjects(c.project_ids || []);
  updateBlocks();
}

async function fillProjects(selectedIds) {
  const sel = $('f-projects');
  sel.replaceChildren();
  let projects = [];
  try {
    projects = await fetchJSONv1('/projects');
  } catch (e) {
    projects = []; // picker stays empty; add-by-ID still works (existence-only validation)
  }
  for (const p of projects) {
    const o = document.createElement('wa-option');
    o.value = String(p.id);
    o.textContent = p.title;
    if (selectedIds.includes(p.id)) o.setAttribute('selected', '');
    sel.appendChild(o);
  }
  for (const pid of selectedIds) {
    if (!projects.some((p) => p.id === pid)) {
      const o = document.createElement('wa-option');
      o.value = String(pid);
      o.textContent = 'Project ' + pid;
      o.setAttribute('selected', '');
      sel.appendChild(o);
    }
  }
}

function updateBlocks() {
  const t = $('f-type').value;
  $('block-range').classList.toggle('hidden', !(t === 'integer' || t === 'decimal'));
  $('block-options').classList.toggle('hidden', !(t === 'select' || t === 'multiselect'));
}

function addOptionRow(opt) {
  const row = document.createElement('div');
  row.className = 'opt-row';
  const value = document.createElement('wa-input');
  value.className = 'opt-value';
  value.setAttribute('label', 'Value');
  value.setAttribute('placeholder', 'stored value');
  value.value = (opt && opt.value) || '';
  const label = document.createElement('wa-input');
  label.className = 'opt-label';
  label.setAttribute('label', 'Label');
  label.setAttribute('placeholder', 'shown label (optional)');
  label.value = (opt && opt.label) || '';
  const up = document.createElement('wa-button');
  up.setAttribute('appearance', 'plain');
  up.textContent = '↑';
  up.addEventListener('click', () => {
    if (row.previousElementSibling) row.previousElementSibling.before(row);
  });
  const down = document.createElement('wa-button');
  down.setAttribute('appearance', 'plain');
  down.textContent = '↓';
  down.addEventListener('click', () => {
    if (row.nextElementSibling) row.nextElementSibling.after(row);
  });
  const remove = document.createElement('wa-button');
  remove.setAttribute('variant', 'danger');
  remove.setAttribute('appearance', 'plain');
  remove.textContent = '✕';
  remove.addEventListener('click', () => row.remove());
  row.append(value, label, up, down, remove);
  $('options-rows').appendChild(row);
}

function collectOptions() {
  return [...document.querySelectorAll('#options-rows .opt-row')].map((row, i) => ({
    value: row.querySelector('.opt-value').value.trim(),
    label: row.querySelector('.opt-label').value.trim(),
    display_order: i,
  }));
}

function collectProjectIds() {
  const v = $('f-projects').value || [];
  const arr = Array.isArray(v) ? v : [v];
  return arr.map(Number).filter((n) => Number.isFinite(n));
}

function collectForm() {
  const type = $('f-type').value;
  const fc = {};
  if ($('f-required').checked) fc.required = true;
  const dflt = $('f-default').value.trim();
  if (dflt !== '') fc.default = dflt;
  const min = parseNum($('f-min').value);
  if (min !== null) fc.min = min;
  const max = parseNum($('f-max').value);
  if (max !== null) fc.max = max;
  if ($('f-api-only').checked) fc.is_api_only = true;
  return {
    name: $('f-name').value.trim(),
    type,
    description: $('f-description').value.trim(),
    field_config: fc,
    display_order: parseNum($('f-order').value) || 0,
    options: (type === 'select' || type === 'multiselect') ? collectOptions() : [],
    project_ids: $('f-all-projects').checked ? [] : collectProjectIds(),
  };
}
```

- [ ] **Step 2: Add save, validation display, and the confirm dialog**

```js
function clearErrors() {
  for (const id of ['err-name', 'err-type', 'err-options', 'err-assignment']) {
    const el = $(id);
    el.textContent = '';
    el.classList.add('hidden');
  }
  $('form-banner').classList.add('hidden');
}

function setFieldError(id, msg) {
  const el = $(id);
  el.textContent = msg;
  el.classList.remove('hidden');
}

function showFormError(e) {
  const msg = (e && e.message) || String(e);
  const m = msg.toLowerCase();
  if (m.includes('option')) setFieldError('err-options', msg);
  else if (m.includes('project')) setFieldError('err-assignment', msg);
  else if (m.includes('type')) setFieldError('err-type', msg);
  else if (m.includes('name')) setFieldError('err-name', msg);
  else {
    $('form-banner').textContent = msg;
    $('form-banner').classList.remove('hidden');
  }
}

// Promise-based confirm dialog. The body is built by buildBody(container) via
// DOM APIs — no innerHTML with data.
function askConfirm(label, buildBody, okLabel, okVariant) {
  return new Promise((resolve) => {
    const dlg = $('dialog-confirm');
    dlg.setAttribute('label', label);
    const body = $('dialog-body');
    body.replaceChildren();
    buildBody(body);
    const ok = $('dialog-ok');
    const cancel = $('dialog-cancel');
    ok.textContent = okLabel;
    ok.setAttribute('variant', okVariant);
    const done = (val) => {
      ok.removeEventListener('click', onOk);
      cancel.removeEventListener('click', onCancel);
      dlg.hide();
      resolve(val);
    };
    const onOk = () => done(true);
    const onCancel = () => done(false);
    ok.addEventListener('click', onOk);
    cancel.addEventListener('click', onCancel);
    dlg.show();
  });
}

async function saveForm() {
  clearErrors();
  const candidate = collectForm();
  saveDraft(candidate);
  try {
    if (formId !== null) {
      const imp = await api('/definitions/' + formId + '/impact', {
        method: 'POST',
        body: JSON.stringify(candidate),
      });
      if (imp.affected_values > 0) {
        const n = imp.affected_values;
        const ok = await askConfirm('Invalidating edit', (body) => {
          body.textContent = 'This edit invalidates ' + n + ' stored value(s). Save anyway?';
        }, 'Save anyway', 'brand');
        if (!ok) return;
      }
      await api('/definitions/' + formId, { method: 'PUT', body: JSON.stringify(candidate) });
      clearDraft();
    } else {
      await api('/definitions', { method: 'POST', body: JSON.stringify(candidate) });
      clearDraft();
    }
    closeForm();
    await loadList();
  } catch (e) {
    if (e.status === 401) return; // expired view already shown by api()
    showFormError(e);
  }
}
```

- [ ] **Step 3: Wire events and add the Edit button**

Append (after the functions above):

```js
$('f-type').addEventListener('wa-change', updateBlocks);
$('f-all-projects').addEventListener('wa-change', () => {
  $('block-assignment').classList.toggle('hidden', $('f-all-projects').checked);
});
$('btn-add-option').addEventListener('click', () => addOptionRow({}));
$('btn-add-project').addEventListener('click', () => {
  const pid = parseNum($('f-project-id').value);
  if (pid === null) return;
  const sel = $('f-projects');
  if (![...sel.children].some((o) => o.value === String(pid))) {
    const o = document.createElement('wa-option');
    o.value = String(pid);
    o.textContent = 'Project ' + pid;
    sel.appendChild(o);
  }
  for (const o of sel.children) {
    if (o.value === String(pid)) o.setAttribute('selected', '');
  }
  $('f-project-id').value = '';
});
$('btn-new').addEventListener('click', () => openForm(null));
$('btn-save').addEventListener('click', saveForm);
$('btn-cancel').addEventListener('click', closeForm);
// Draft persistence: every keystroke/change while the form is open.
for (const evt of ['input', 'change']) {
  $('form-view').addEventListener(evt, () => {
    try { saveDraft(collectForm()); } catch {}
  });
}
```

Then, in `cardFor`, replace the footer comment block with the Edit button:

```js
  const footer = document.createElement('div');
  footer.className = 'wa-split';
  footer.style.marginTop = '0.5rem';
  const edit = document.createElement('wa-button');
  edit.setAttribute('appearance', 'plain');
  edit.textContent = 'Edit';
  edit.addEventListener('click', () => openForm(item.id));
  footer.append(edit);
```

- [ ] **Step 4: Verify live**

```bash
chmod a+r ui/app.js
docker compose -f compose.test.yml restart
JWT=$(./scripts/run-test-env.sh 2>/dev/null | grep -o 'eyJ[^"]*' | head -1)
BASE=http://127.0.0.1:4176/api/v1
curl -s -X POST $BASE/projects -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"CF Form"}' > /dev/null
```

Browser as `testuser`:
- **Create:** "New field" → form. Name only + Save → card appears. Blank name → server 400/409 surfaces inline near the name field (or banner); submit again after filling → succeeds.
- **Type switching:** pick `integer` → Min/Max appear; pick `select` → options editor appears. Add two option rows, reorder with ↑/↓, remove one.
- **Assignment:** uncheck "All projects" → picker fills from your projects + add-by-ID works.
- **Edit:** Edit on a card loads current values; change name, Save → list refreshed.
- **Impact dialog:** edit a select definition used on a task (seed a value via the S3 API first), remove an in-use option, Save → dialog "This edit invalidates 1 stored value(s)"; Cancel → nothing saved; Save anyway → PUT succeeds.
- **Drafts:** start editing a definition, type a new name, **do not save**, navigate away/reload → Edit again → the unsaved name is restored. Save clears the draft.
- **Expired mid-edit:** set an invalid token in DevTools, Save → expired view; log into Vikunja in the same browser, return, reload → Edit → draft intact.

- [ ] **Step 5: Commit**

```bash
git add ui/app.js
git commit -m "feat(s9): management UI form with impact dialog, drafts, inline validation"
```

---

### Task 7: Delete flow (count-aware confirmation)

**Files:**
- Modify: `ui/app.js` (extend `cardFor` footer with Delete; add `confirmDelete`)

**Interfaces:**
- Consumes: `GET /definitions/:id/impact` (Task 3), `DELETE /definitions/:id` (S2), `askConfirm` (Task 6), `loadList`.
- Produces: `confirmDelete(id)`.

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** webawesome, golang-safety

- [ ] **Step 1: Add the delete flow**

Append to `ui/app.js`:

```js
async function confirmDelete(id) {
  let n = 0;
  try {
    const imp = await api('/definitions/' + id + '/impact');
    n = imp.affected_values;
  } catch (e) {
    if (e.status === 401) return;
    // Count is a nicety; the cascade still warns below if the preview fails.
  }
  const ok = await askConfirm('Delete field', (body) => {
    body.textContent = 'This permanently deletes the field and its ' + n +
      ' stored value(s). This cannot be undone.';
  }, 'Delete field', 'danger');
  if (!ok) return;
  try {
    await api('/definitions/' + id, { method: 'DELETE' });
  } catch (e) {
    if (e.status === 401) return;
    const box = $('list-error');
    box.textContent = 'Delete failed: ' + e.message;
    box.classList.remove('hidden');
    return;
  }
  await loadList();
}
```

- [ ] **Step 2: Add the Delete button to cards**

In `cardFor`, extend the footer (after `footer.append(edit);`):

```js
  const del = document.createElement('wa-button');
  del.setAttribute('variant', 'danger');
  del.setAttribute('appearance', 'plain');
  del.textContent = 'Delete';
  del.addEventListener('click', () => confirmDelete(item.id));
  footer.append(del);
```

- [ ] **Step 3: Verify live**

```bash
chmod a+r ui/app.js
docker compose -f compose.test.yml restart
JWT=$(./scripts/run-test-env.sh 2>/dev/null | grep -o 'eyJ[^"]*' | head -1)
BASE=http://127.0.0.1:4176/api/v1
PID=$(curl -s -X POST $BASE/projects -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"CF Delete"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
# three tasks — values are UNIQUE(field, task), so one task per value
T1=$(curl -s -X POST $BASE/projects/$PID/tasks -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"t1"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
T2=$(curl -s -X POST $BASE/projects/$PID/tasks -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"t2"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
T3=$(curl -s -X POST $BASE/projects/$PID/tasks -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"title":"t3"}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
DID=$(curl -s -X POST $BASE/plugins/custom-fields/definitions -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{"name":"Doomed","type":"text","field_config":{},"display_order":0,"project_ids":[]}' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
curl -s -X POST $BASE/plugins/custom-fields/tasks/$T1/custom-fields -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "[{\"custom_field_definition_id\":$DID,\"value\":\"a\"}]"
curl -s -X POST $BASE/plugins/custom-fields/tasks/$T2/custom-fields -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "[{\"custom_field_definition_id\":$DID,\"value\":\"b\"}]"
curl -s -X POST $BASE/plugins/custom-fields/tasks/$T3/custom-fields -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d "[{\"custom_field_definition_id\":$DID,\"value\":\"c\"}]"
```

Browser as `testuser`: Delete on "Doomed" → dialog reads "…deletes the field and its 3 stored value(s)." Cancel → definition intact; Delete → confirm → card gone. `sqlite3 db/vikunja.db "SELECT COUNT(*) FROM custom_field_values WHERE custom_field_definition_id = $DID;"` → `0`.

- [ ] **Step 4: Commit**

```bash
git add ui/app.js
git commit -m "feat(s9): count-aware delete confirmation"
```

---

### Task 8: Acceptance-criteria verification walkthrough

**Files:** none (verification only; fix-forward commits if a check fails)

**Interfaces:**
- Consumes: everything above; `run-test-env.sh`'s two users (`testuser` whitelisted, `otheruser` not — both password `testpassword`).

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** superpowers:systematic-debugging (if any check fails)

- [ ] **Step 1: Fresh instance, full seed**

```bash
./scripts/run-test-env.sh   # note the printed JWTs
BASE=http://127.0.0.1:4176/api/v1
```

- [ ] **Step 2: Walk all nine ACs and record observed evidence**

- **AC#1** — browser opens `http://127.0.0.1:4176/api/v1/plugins/custom-fields/ui` (plugin-served URL). Observed: page renders.
- **AC#2** — seed definitions of several types (text with description, integer with min/max, select with 3 options assigned to one project, checkbox global). Observed: cards list name, type, required/range, options count, "N project(s)"/"All projects".
- **AC#3** — create via the form (all field groups). Observed: card appears; `curl $BASE/plugins/custom-fields/definitions | jq '.[] | {name, type, field_config, project_ids}'` shows the definition with its assignment.
- **AC#4** — edit properties of an existing definition. Observed: `ReadOne` reflects the change after save.
- **AC#5** — delete with confirmation. Observed: dialog with count; after confirm the definition is gone (`curl` 404 on ReadOne) and its values rows are gone (sqlite count 0).
- **AC#6** — in the same browser set `localStorage.token` to `otheruser`'s JWT (script prints `JWT_OTHERUSER`; or log in as otheruser). Observed: not-authorized view; additionally `curl -s -o /dev/null -w '%{http_code}' $BASE/plugins/custom-fields/definitions -H "Authorization: Bearer $JWT_OTHER"` → `403`.
- **AC#7** — submit invalid forms: options on a `text` type (temporarily unhide the block via DevTools, or use curl to confirm the message: `curl -s -X POST …/definitions -d '{"name":"x","type":"text","options":[…]}'` → error message mentions options); blank name; nonexistent project ID via add-by-ID (`{"project_ids":[99999]}` → project-not-found message). Observed: each error renders inline on the matching field, or in the banner when unmatched.
- **AC#8** — the test instance runs with no license (`license.key` unset). Observed: everything above worked — nothing touched `/api/v1/admin/*` or `/api/v2/admin/*` at any point.
- **AC#9** — with a seeded select definition and 2 values on tasks (one using option "a"), edit removing "a" → dialog "This edit invalidates 1 stored value(s)"; sqlite proof: `sqlite3 db/vikunja.db "SELECT COUNT(DISTINCT r.custom_field_value_id) FROM custom_field_value_options r JOIN custom_field_options o ON o.id = r.custom_field_option_id WHERE o.custom_field_definition_id = $DID AND o.value = 'a';"` → `1`. Also type-change → count equals total values (sqlite `SELECT COUNT(*) FROM custom_field_values WHERE custom_field_definition_id = $DID;`).

- [ ] **Step 3: Fix-forward anything that fails** (root-cause first, per systematic-debugging), re-verify, and commit fixes with `fix(s9): …`.

- [ ] **Step 4: Final commit checkpoint** — `git status` clean; all AC evidence recorded in the task log.

---

### Task 9: Story-doc amendments, Resolution, dependency graph, S7 pickup

**Files:**
- Modify: `docs/stories/S9-management-ui.md` (design-principles line, scope additions, Resolution section, front-matter `status:`)
- Modify: `docs/stories/story-dependency-graph.md` (check off S9)
- Modify: `docs/stories/S7-build-deploy-document.md` (pickup list)

**Interfaces:**
- Consumes: the spec's "Downstream amendments" section; the implementation's actuals.

**Required Skills:** superpowers:verification-before-completion
**Recommended Skills:** git-flow (for the eventual `git flow feature finish`, which happens separately per the finishing-a-development-branch workflow)

- [ ] **Step 1: Amend the S9 story doc body**

- Design-principles bullet "API-first, UI-second": replace "it adds no new backend capabilities beyond a definition-edit-impact query (AC#9)" with "it adds no new backend capabilities beyond the definition-edit-impact queries (AC#9, plus the delete-impact count) and the management-access capability check".
- **In scope:** add — unauthenticated shell route serving `ui/` assets from disk; the `management-access` capability endpoint; the delete-impact count in the delete confirmation (beyond AC#5's letter); Web Awesome via pinned CDN (`@3.12.0`); mobile usability (stacked cards, single-column form).
- Front-matter: `status: pending` → `status: done`.

- [ ] **Step 2: Write the Resolution section** (per CLAUDE.md "Story Resolution" — Status + AC verification evidence, how it was built, notable deviations with reasons, key decisions grounded in the spec, what was left open). Mandatory deviations to record: the three endpoints vs the story's "one query" wording; N+1 ReadOne for card relations; Web Awesome adoption; the corrected definition-read gating fact. Self-contained items only — no pointers to ephemeral artifacts.

- [ ] **Step 3: Check off S9 in `docs/stories/story-dependency-graph.md`.**

- [ ] **Step 4: Add the S7 pickup list** to `docs/stories/S7-build-deploy-document.md` (new section at the end):

```markdown
## From S9 (management UI) — document in build/deploy docs

- The management UI URL: `/api/v1/plugins/custom-fields/ui` (bookmarkable; served by the plugin's unauthenticated route group).
- The page loads Web Awesome from `https://ka-f.webawesome.com/webawesome@3.12.0/` — browsers need internet access to it. Self-hosting via `ui/vendor` (`data-webawesome` base path) is the offline fallback and the recommended default for instances handling sensitive data.
- The deployment mount must include the plugin directory **wholesale** (so `ui/` rides along) — a file-only mount breaks the UI.
```

- [ ] **Step 5: Commit**

```bash
git add docs/stories/S9-management-ui.md docs/stories/story-dependency-graph.md docs/stories/S7-build-deploy-document.md
git commit -m "docs(s9): record Resolution, amend story scope, hand pickup list to S7"
```

---

## Spec coverage map (self-review aid)

| Spec section | Task |
|---|---|
| Asset serving (unauth routes, uiDir, 404, no-store) | 1 |
| management-access | 2 |
| Impact POST diff table + GET delete count, both gated | 3 |
| Auth model, states, fetch layer, XSS discipline | 4 |
| List view (cards, N+1 ReadOne, AC#2) | 5 |
| Form (create/edit, options editor, assignment, impact dialog, drafts, inline validation) | 6 |
| Delete confirmation with count | 7 |
| Testing & verification (all 9 ACs) | 8 |
| Downstream amendments (S9 doc, dependency graph, S7) | 9 |
