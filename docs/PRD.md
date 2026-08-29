# Custom Fields for Vikunja — Project Requirements

**Date:** 2026-08-08

## Summary

Add custom fields to Vikunja tasks. A whitelisted user defines additional fields — text, numbers, selects, dates, etc. — and assigns them to projects. Those fields appear on tasks alongside native fields, visually and behaviorally indistinguishable from them.

Field definitions are stored in the database and managed through a temporary web interface served by the plugin. A future epic replaces that interface with native management in Vikunja's own UI, for both licensed and unlicensed instances.

Built as a backend plugin with frontend patches to our fork: a proving ground for a feature that may eventually be proposed upstream as core functionality.

## Motivation

Vikunja's task model is fixed. Every task has the same fields. But real-world task management often needs domain-specific metadata — a cost center code, a sprint number, a safety classification. Custom fields let each Vikunja instance extend tasks to fit how the team actually works, without fragile workarounds like encoding data in descriptions or maintaining external spreadsheets.

## Design Principles

1. **Indistinguishable from native fields.** A user should not perceive any difference between a built-in field and a custom field. Same visual treatment, same interaction patterns, same API conventions.

2. **Centrally governed.** Field definitions are created and managed by a config-declared whitelist of users. Fields are assigned to all projects or specific projects. Project members use the fields they're given — they don't configure them.

3. **Plugin as proving ground, not permanent home.** The backend is a yaegi plugin requiring zero changes to Vikunja core. The temporary management UI is likewise a stand-in. Every design decision — data model, API shapes, frontend integration — is made as if the feature were native. Upstreaming should be a mechanical move, not a redesign.

4. **API-first, UI-second.** The API is the authoritative surface. Both the temporary management UI and the frontend consume it. The API is designed as if it were a native `/api/v2/custom-fields` resource, just served from the plugin route prefix. When the feature moves upstream, the API moves to a different URL prefix and the consumers change one base URL.

5. **Works without a license.** Field management must not depend on Vikunja's licensed admin feature, which is locked behind a subscription. The management surface is served by the plugin and governed by a config whitelist, so the feature is available to all instances.

## User Experience

**The whitelisted users** define fields — names them, pick a type, set constraints, assign them to projects — through a temporary management interface served by the plugin and reachable in the browser.

**The project member** opens a task and sees the custom fields mixed in with the native ones. They edit them the same way they edit any other field. If they open a task in a project that doesn't have those fields assigned, the fields simply aren't there. The management interface is invisible to them.

**The API consumer** fetches a task's custom field values alongside the task itself. The response shapes match the rest of the Vikunja API.

## Field Types

Custom fields support: single-line text, multi-line text, integer, decimal, date, datetime, single-select dropdown, multi-select, checkbox, and URL. Number fields support range constraints. Select fields support predefined option lists.

As a stretch goal, fields can be marked "API-only" — read/write through the API, but display-only in the UI. This enables other plugins or scripts to populate integration-driven or calculated values without user editing.

## Architecture (high-level)

**Backend plugin** — A yaegi Go plugin running inside Vikunja's process. It owns its own database tables for field definitions and values, exposes the API for both, and enforces authorization (whitelist for management, task-level for values). It also serves the temporary management UI.

**Temporary management UI** — A web interface served by the plugin on a plugin API route. It manages field definitions against the same database the API reads. It reuses the user's existing Vikunja browser session for authentication and is gated by the config whitelist. It is a stand-in for native management, which is a future epic.

**Frontend patches** — Modifications to the Vue 3 frontend in our fork. The task detail view fetches and renders custom fields data-driven from the API response. No hardcoded field types — rendering is driven by the field definition, not the field name.

**Config file** — A single responsibility: the whitelist of users permitted to manage custom fields, read from the `customfields.whitelist` config key (overridable by the `VIKUNJA_CUSTOMFIELDS_WHITELIST` env var). It is not a source of field definitions.

**Future epic — native management.** Replace the temporary management UI with custom-field management integrated into Vikunja's own interface, for both licensed and unlicensed instances. This is the destination the temporary UI points toward; it is out of scope now.

The plugin API is the seam. The frontend and the temporary management UI both talk to it. When the feature moves upstream, the API moves to a different URL prefix and both consumers change one base URL.

## Deployment

A single Docker image built from our fork using the existing multi-stage Dockerfile (frontend → embedded in binary). The plugin source directory is mounted into the container at runtime. One image, one container.

## Out of Scope

- Native management in Vikunja's interface (future epic)
- Task creation form overhaul (separate future project)
- Calculated fields (the API-only flag enables them, but calculation logic lives elsewhere)
- Per-project field self-service by project owners
- A generic frontend plugin extension mechanism
- Quick Add Magic support for custom fields

## Constraints

- The Vikunja plugin system is experimental, backend-only, and requires Vikunja ≥ 2.3.0
- Plugin routes currently register only under `/api/v1/`; no v2 plugin mechanism exists yet
- Yaegi plugins work best as single `.go` files
- The management surface must not depend on the licensed admin feature
