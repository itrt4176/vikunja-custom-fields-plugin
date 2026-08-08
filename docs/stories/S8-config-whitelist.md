---
title: "Config Whitelist"
description: "The config file defines which users are permitted to manage custom fields."
status: pending
priority: 70
labels: ["backend", "configuration"]
position: 8
---

# Config Whitelist

## Outcome

An instance admin declares who may manage custom fields. A whitelist of usernames (or user identifiers) in Vikunja's config file defines the users permitted to create, edit, and delete field definitions. Users not on the whitelist cannot manage custom fields. No license or admin panel is involved.

## What & Why

Custom-field management must not depend on Vikunja's licensed admin feature. The config file — freely editable by any instance admin — is the natural license-free surface for declaring the management authority. This story makes the config file hold a single thing: the whitelist of users who may manage custom fields. It is explicitly **not** a source of field definitions.

The whitelist is consumed by any management surface: the field definition API (S2) and the temporary management UI (S9) both check it before allowing field-definition changes.

## Design Principles

- **Works without a license** — the whitelist lives in config, so management authority never hinges on the licensed admin feature.
- **Centrally governed** — the whitelist centralizes who gets to manage fields; it is the admin's domain, expressed in config.
- **API-first, UI-second** — the whitelist is read by the API and UI alike; it is a config concern, not a UI concern.

## Dependencies

- **Must come after:** S1 (Plugin Foundation)
- **Must come before:** S2 (Field Definition API), S9 (Management UI)
- **Can run in parallel with:** S3 (Task Field Values API)

## Acceptance Criteria

1. A whitelist of permitted users can be declared in Vikunja's config file.
2. A whitelisted user is allowed to manage custom fields.
3. A user not on the whitelist is denied field-definition operations (checked by S2/S9).
4. A malformed whitelist entry logs a clear error on startup but does not crash Vikunja.
5. Vikunja starts successfully when the whitelist is absent (empty — no users may manage fields).
6. The whitelist is read from config and shared with both the field definition API (S2) and the management UI (S9).

## Scope

**In scope:**
- Config schema for the management whitelist
- Reading and exposing the whitelist to field-definition management (S2, S9)
- Clear error logging for malformed entries

**Out of scope:**
- Field definition CRUD — S2
- The management UI that consumes the whitelist — S9
- Defining field values — values belong to tasks, not config
- Hot-reloading config changes without restart