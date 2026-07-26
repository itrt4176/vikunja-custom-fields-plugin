---
title: "Config File Field Definitions"
description: "Custom field definitions can be declared in Vikunja's config file, enabling unlicensed instances."
status: pending
priority: 70
labels: ["backend", "configuration"]
position: 4
---

# Config File Field Definitions

## Outcome

An instance admin can declare custom field definitions in Vikunja's YAML configuration file and have them available in the system on startup — no license required, no admin UI needed. These config-declared fields coexist with any API-managed fields (S2) and are queryable through the same endpoints.

## What & Why

Vikunja's admin panel requires a paid license, which would lock many instances out of using custom fields. This story provides a license-free path: define the fields in the config file, restart Vikunja, and they're available. This directly serves the PRD's design principle of working without a license.

Config-declared fields are synced to the database on startup and become indistinguishable from API-created fields. The same API that serves S2 field definitions serves config-declared ones. The same frontend (S5) renders both.

## Design Principles

- **Works without a license** — this is the story that delivers on that principle. Config files are the mechanism.
- **API-first, UI-second** — config-declared fields feed into the same API surface; the API doesn't care where the definition came from.
- **Admin-managed, centrally governed** — config files are the admin's domain.

## Dependencies

- **Must come after:** S2 (Field Definition API)
- **Must come before:** S7 (Build, Deploy & Document)
- **Can run in parallel with:** S3 (Task Field Values API)

## Acceptance Criteria

1. Custom field definitions can be declared in Vikunja's YAML config file under a `custom-fields` key.
2. On startup, config-declared fields are synced to the database — new fields are created, removed fields are deleted, changed fields are updated.
3. Config-declared fields appear in the field definitions API (S2) just like API-managed fields.
4. Config-declared fields work with field values (S3) — values can be stored and retrieved for config-declared fields.
5. A malformed config entry logs a clear error on startup but does not crash Vikunja.
6. Config-declared fields are validated on startup with the same rules as API-managed fields (valid type, non-empty name, etc.).
7. Vikunja still starts successfully when no `custom-fields` config key is present.

## Scope

**In scope:**
- Config schema for declaring field definitions (name, type, constraints, project assignment)
- Startup sync logic: config → database
- Validation parity with S2's API validation
- Clear error logging for bad config entries

**Out of scope:**
- Hot-reloading config changes without restart
- Bidirectional sync (API changes reflected back to config file)
- Any frontend changes
- Config support for defining field values — values belong to tasks, not config