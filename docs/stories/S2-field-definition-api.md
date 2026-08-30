---
title: "Field Definition API"
description: "Whitelisted users can create, read, update, and delete custom field definitions via the plugin API."
status: done
priority: 80
labels: ["backend", "api"]
position: 2
---

# Field Definition API

## Outcome

A whitelisted user can manage custom field definitions through the plugin API: create a field with a name, type, and constraints; list all defined fields; update a field's properties; and delete a field. Each field definition includes which project(s) it applies to.

## What & Why

This story builds the CRUD API for field definitions on top of the tables created in S1. Field definitions are the schema layer — they describe what custom fields exist, what type they are, what constraints they have, and which projects they're assigned to. Without them, there's nothing to attach values to.

The API follows Vikunja's conventions for request/response shapes, authentication, and error handling. Access is governed by the management whitelist (S8, read from Vikunja config): only users on the whitelist can manage field definitions. This deliberately bypasses Vikunja's licensed admin feature.

## Design Principles

- **API-first, UI-second** — this story delivers the API surface for field definitions before any management UI exists. The management UI (S9) and frontend (S5) are both consumers of this same API contract.
- **Centrally governed** — field definitions can only be created or modified by a config-whitelisted user.
- **Plugin as proving ground, not permanent home** — the API is designed as if it were a `/api/v2/custom-fields` resource, just served from the plugin route prefix.

## Dependencies

- **Must come after:** S1 (Plugin Foundation), S8 (Config Whitelist)
- **Must come before:** S3 (Task Field Values API), S9 (Management UI)
- **Can run in parallel with:** none (S5 is a downstream consumer of this API)

## Acceptance Criteria

1. A whitelisted user can create a field definition with a name, type, optional constraints, and project assignment via the API.
2. A whitelisted user can list all field definitions, optionally filtered by project.
3. A whitelisted user can update a field definition's properties (name, type, constraints, project assignment).
4. A whitelisted user can delete a field definition.
5. Users not on the whitelist receive an authorization error when attempting any field definition operation.
6. Field definitions are validated: type must be one of the supported types, name must be non-empty, constraints must be valid for the chosen type.
   > **Amended 2026-08-29:** the original "unique within a project" requirement was dropped. The name is a display label — field values join by `definition_id`, never by name — so name uniqueness is not a data-integrity requirement. This matches upstream Vikunja convention, where no reusable named entity (labels, teams, saved filters) enforces name uniqueness. The "two fields with the same name is confusing" worry is a management-time concern left to the management UI (S9), not an API-level constraint. See the S2 design spec for the full rationale.
7. The API response shapes follow Vikunja conventions (snake_case, consistent envelope).

## Scope

**In scope:**
- Full CRUD API for field definitions
- Validation (types, required fields, constraints)
- Whitelist-based authorization (dependency: S8)
- Project assignment (one field → one or more projects, or all projects)

**Out of scope:**
- Storing or retrieving field values on tasks — S3
- The management UI that consumes this API — S9
- Any frontend task-detail changes — S5
- Soft-delete or archival of fields
- "API-only" field flag — defer to S3

## Resolution

**Status:** Done. All 7 acceptance criteria pass on the Docker test instance (SQLite), verified end-to-end with `curl` + JWTs for both a whitelisted manager (`testuser`) and a non-whitelisted user (`otheruser`). Branch `feature/s2-field-definition-api-design` (git-flow, off `develop`); not yet merged. Full design in `docs/superpowers/specs/2026-08-29-s2-field-definition-api-design.md`; task-by-task plan in `docs/superpowers/plans/2026-08-29-s2-field-definition-api.md`.

### How it was built

A single-file yaegi plugin (`main.go`) with internal layering that mirrors Vikunja's model/permissions/handler split, so upstreaming is a mechanical move (register the same methods with `pkg/web/handler`, swap the route prefix). Three tables: `custom_field_definitions` (expanded from S1), `custom_field_options` (select option rows), `custom_field_projects` (assignment M2M, `project_id=0` sentinel = "all projects"). The S1 migration was modified in place (pattern B — unreleased feature, `project_views` precedent); it `Sync2`s all four tables via explicit-table form (`tx.Table(name).Sync2(&T{})`).

Five endpoints under `/api/v1/plugins/custom-fields/definitions[/:id]`: POST (create), GET (list, optional `?project_id=` filter), GET/:id, PUT (full-replace), DELETE. Authorization: every handler calls `CanX` → `IsManager` → 403 for non-whitelisted users. Validation: type/name/options/constraints/project-assignment checks return 400 (404 for not-found). The temporary S8 `managerHandler` route was removed; `IsManager` is now exercised on the real endpoints.

### Notable deviations from the original design (all grounded in spike evidence)

1. **Events seam deferred.** The spec originally specified on-commit events (`FieldDefinition{Created,Updated,Deleted}Event`) as a seam for S3. Task 1 spike 3 found that **plugin-defined event types cannot be published under yaegi**: the interpreted struct bridges to the host `events.Event` interface (`DispatchOnCommit` queues it, `Name()` is called), but the host's `json.Marshal(event)` fails on the yaegi method-bridge field (`json: unsupported type: func() string`), so the event is never published and listeners receive nothing (HTTP 200 masks it). A host-defined event type works end-to-end (spike 3b proved `models.TaskUpdatedEvent`), but no host custom-field event type exists, so that path needs a fork core change + image rebuild. Decision (user): **defer the seam** rather than add core event types — keep S2 a zero-core-change proving ground. S3 will provide its own value-cleanup trigger. No `events.*` calls, no `pkg/events` import. The reference event design + the verified two-call dispatch mechanism are retained in the spec for whatever S3 builds.

2. **Response shapes are hand-built maps, not serialized structs.** Spike 1 found interpreted structs serialize as `{}` through `c.JSON`/`encoding/json` (yaegi wrapper fields aren't json-visible). Handlers build response maps field-by-field (`definitionToMap`/`definitionFieldsMap`/`fieldConfigMap`); xorm DB read/write of interpreted structs works fine (only the HTTP `c.JSON` path is affected).

3. **`toHTTPError` discriminates by message prefix, not type assertion.** `switch err.(type)` never matches under yaegi (interpreted errors wrap as `interp._error`); `toHTTPError` was rewritten to match `err.Error()` prefixes via `strings.HasPrefix` (404 for not-found, 400 for the 8 validation errors, 500 default for wrapped DB errors). A second yaegi deviation: the multi-expression `case a, b, c:` form evaluates only the first expression under yaegi, so each prefix is its own `case` clause. Both are documented yaegi workarounds — upstream conversion reverts to `switch err.(type)`.

### Key decisions (from the spec, grounded in upstream evidence)

- **`field_config` as `xorm:"json null"`** (typed `FieldConfig` struct) — matches `api_tokens.APIPermissions`; spike 1 confirmed it round-trips under yaegi (TEXT under sqlite, JSON/JSONB under mysql/postgres).
- **No name uniqueness** — matches labels/teams/saved-filters; the name is a display label, values join by `definition_id`. AC#6 was amended to drop "unique within a project."
- **Hard-delete own rows, defer values to S3** — `Delete` cascades definition + options + assignment in one transaction; it does NOT touch `custom_field_values` (S3's table). Matches the team-style manual cascade; no soft-delete (reserved for Task content).
- **PUT only** (full-replace); PATCH deferred to upstreaming (`EnableAutoPatch` generates it correctly against huma; echo binding can't do merge-patch).
- **`*user.User` not `web.Auth`** in `CanX`/model signatures — `web` is unavailable to yaegi; an upstream-conversion point.
- **Modify the S1 migration in place** — unreleased feature, one upstream PR; the append-only switch fires when the plugin runs in production.

### What was left open (deferred, none block merge, all triaged in the final whole-branch review)

- The **S3 event seam** (above) — S3 builds its own value-cleanup trigger; `CanUpdate`/`CanDelete` are clean guard-insertion points for a future "block if values exist" check.
- **Known code-level issues (left as-is, all minor):**
  - `validateDefinition`'s option-duplicate check keys on raw `o.Value`, not a trimmed value, so `"a"` and `" a "` are treated as distinct options. Left as-is because trimming could break options with intentional surrounding whitespace; if trimming is wanted, it's a one-line change plus a write-time normalization decision that belongs to S3.
  - `validateAssignment` issues one `Exist` query per supplied project ID (N queries for N ids). Justified for small N (it gives per-ID `ErrCustomFieldProjectNotFound` attribution); revisit only if assignment batches grow large.
  - The `toHTTPError` prefix `"constraint "` is broad enough to catch an *unwrapped* driver error like `"constraint failed: ..."` (would map to 400 instead of 500). Left as-is because every DB error the model returns is wrapped with `fmt.Errorf("custom-fields: ...: %w", err)` and falls to the 500 default; no bare `return err` reaches the handler. The whole function reverts to `switch err.(type)` on upstreaming.
  - `fieldConfigMap` omits the `min`/`max` keys when the `*float64` pointers are nil (rather than emitting `null`), so the `field_config` object's key set varies per resource. By design — matches the `FieldConfig` struct's `json:"min,omitempty"` tags; the response-shape checks expect this shape.
  - The migration-rationale comment in `main.go` (`Migrations()`) cites upstream PR #3549 for the explicit-table-form rule, but that rule is PR #3501 (#3549 is the symbol-table registration). Pre-existing from S1; fix when that comment is next touched.
- **Fixed before merge (not deferred):** `gofmt -w main.go` (const-block + response-map literal alignment); and `setOptions` zeroes `options[i].ID` before insert, so a crafted `{"options":[{"id":999,...}]}` body cannot inject an option PK (the definition PK is likewise not client-settable — `definitionRequest` has no `ID` field).
- **Test-harness notes:** `scripts/run-test-env.sh` surfaces `$PROJECT_ID` via the ready banner because the in-script `export` doesn't reach the caller (the script runs as a child process); the project-create step warns-and-continues on failure rather than failing fast; and AC#4's "values untouched" evidence is vacuous in S2 (no values exist) — S3 should seed a `custom_field_values` row before a delete to make it falsifiable.
- **`created`-timestamp preservation** confirmed: xorm's `created` tag is insert-only — `updated` bumps on PUT, `created` unchanged (spot-checked).
- **How production-era append-migrations translate upstream** — TBD when the feature upstreams.