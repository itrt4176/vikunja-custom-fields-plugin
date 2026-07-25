# Custom Fields for Vikunja — Epic Design

**Date:** 2026-07-25

## Summary

Add admin-defined custom fields to Vikunja tasks. An instance admin defines additional fields — text, numbers, selects, dates, etc. — and assigns them to projects. Those fields then appear on tasks alongside native fields, visually and behaviorally indistinguishable from them.

Built as a backend plugin with frontend patches to our fork: a proving ground for a feature that may eventually be proposed upstream as core functionality.

## Motivation

Vikunja's task model is fixed. Every task has the same fields. But real-world task management often needs domain-specific metadata — a cost center code, a sprint number, a safety classification. Custom fields let each Vikunja instance extend tasks to fit how the team actually works, without fragile workarounds like encoding data in descriptions or maintaining external spreadsheets.

## Design Principles

1. **Indistinguishable from native fields.** A user should not perceive any difference between a built-in field and a custom field. Same visual treatment, same interaction patterns, same API conventions.

2. **Admin-managed, centrally governed.** Field definitions are created and managed by the instance admin. Fields are assigned to all projects or specific projects. Project members use the fields they're given — they don't configure them.

3. **Plugin as proving ground, not permanent home.** The backend is a yaegi plugin requiring zero changes to Vikunja core. But every design decision — API shapes, data model, frontend integration — is made as if the feature were native. Upstreaming should be a mechanical move, not a redesign.

4. **API-first, UI-second.** The API is the authoritative surface. The frontend consumes it. Config-based field definitions (for unlicensed instances) feed into the same system.

5. **Works without a license.** Vikunja's admin UI requires a paid license. Field definitions must be expressible through the config file so the feature is available to all instances.

## User Experience

**The admin** defines fields — names them, picks a type, sets constraints, assigns them to projects. On a licensed instance this is done through the admin panel. On an unlicensed instance, through the config file.

**The project member** opens a task and sees the custom fields mixed in with the native ones. They edit them the same way they edit any other field. If they open a task in a project that doesn't have those fields assigned, the fields simply aren't there.

**The API consumer** fetches a task's custom field values alongside the task itself. The response shapes match the rest of the Vikunja API.

## Field Types

Custom fields support: single-line text, multi-line text, integer, decimal, date, datetime, single-select dropdown, multi-select, checkbox, and URL. Number fields support range constraints. Select fields support predefined option lists.

As a stretch goal, fields can be marked "API-only" — read/write through the API, but display-only in the UI. This enables other plugins or scripts to populate integration-driven or calculated values without user editing.

## Architecture (high-level)

Three parts sharing a common API contract:

**Backend plugin** — A yaegi Go plugin running inside Vikunja's process. It creates its own database tables, exposes a REST API for field definitions and values, and enforces authorization (admin for field management, task-level for values).

**Frontend patches** — Modifications to the Vue 3 frontend in our fork. The task detail view fetches and renders custom fields data-driven from the API response. An admin panel page handles field creation (licensed instances). No hardcoded field types — the rendering is driven by the field definition, not the field name.

**Config file** — Declarative field definitions in Vikunja's config for unlicensed instances. These sync to the same database tables and share the same API as UI-managed fields.

The plugin API is the seam between all three. The frontend talks to the plugin API. The config feeds into the plugin. When the feature moves upstream, the API moves to a different URL prefix — the frontend changes one base URL.

## Deployment

A single Docker image built from our fork using the existing multi-stage Dockerfile (frontend → embedded in binary). The plugin source directory is mounted into the container at runtime. One image, one container.

## Out of Scope

- Task creation form overhaul (separate future project)
- Calculated fields (the API-only flag enables them, but calculation logic lives elsewhere)
- Per-project field self-service by project owners
- A generic frontend plugin extension mechanism
- Quick Add Magic support for custom fields

## Constraints

- The Vikunja plugin system is experimental, backend-only, and requires Vikunja ≥ 2.3.0
- Plugin routes currently register only under `/api/v1/`; no v2 plugin mechanism exists yet
- Yaegi plugins work best as single `.go` files
