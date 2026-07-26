---
title: "Plugin Foundation"
description: "Plugin loads on startup, registers routes, and creates database tables for custom fields."
status: pending
priority: 90
labels: ["backend", "infrastructure"]
position: 1
---

<!--
  Story template — fill out each section before the story enters its own
  brainstorming → planning → implementation cycle.

  GUIDELINES:
  - Describes WHAT and WHY. Never HOW.
  - No implementation details: no routes, no schemas, no component names,
    no file paths, no code, no pseudocode, no library choices.
  - Each story delivers one concrete, user-verifiable outcome.
  - Anything that applies to every story (e.g. "zero core changes") stays
    in the PRD — don't repeat it here.
-->

# Plugin Foundation

## Outcome

The plugin loads when Vikunja starts, registers its route group with the server, and creates the database tables needed by all subsequent stories. An admin can enable the plugin in config, restart, and see it in the logs — the foundation is in place.

## What & Why

This story establishes the plugin skeleton: the minimum viable plugin that compiles under yaegi, implements the required interfaces, and wires itself into Vikunja's lifecycle. It runs database migrations to create the tables for field definitions and field values. It registers an API route group so later stories have somewhere to attach their handlers.

Without this, no other story can start. Every subsequent story assumes the plugin loads, tables exist, and routes are available. This story delivers that baseline.

## Design Principles

- **Plugin as proving ground, not permanent home** — the plugin loads as a yaegi plugin with zero core changes, but its internal structure is designed as if it were native code.
- **API-first, UI-second** — this story establishes the API route group that all later stories build on.

## Dependencies

- **Must come after:** none
- **Must come before:** S2 (Field Definition API), and all subsequent stories
- **Can run in parallel with:** none

## Acceptance Criteria

1. Vikunja starts successfully with the plugin enabled (yaegi loader) and logs the plugin name and version.
2. Database migrations run automatically on first load, creating all tables needed for field definitions and field values.
3. No core Vikunja source files are modified — the plugin lives entirely in its own directory.
4. The plugin registers an authenticated route group under the plugin API prefix.
5. Vikunja shuts down cleanly with the plugin loaded (no panics, no leaked resources).
6. The plugin passes Vikunja's existing test suite without regressions.

## Scope

**In scope:**
- Plugin directory structure and source files
- Implementation of the required plugin interfaces (Name, Version, Init, Shutdown, NewPlugin)
- Database migrations for field definitions and values tables
- Route group registration

**Out of scope:**
- Any API handler logic beyond a health-check or placeholder route
- Field definition CRUD operations — S2
- Field value CRUD operations — S3
- Config file parsing — S4
- Any frontend changes
