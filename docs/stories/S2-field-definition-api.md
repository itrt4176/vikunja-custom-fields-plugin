---
title: "Field Definition API"
description: "Whitelisted users can create, read, update, and delete custom field definitions via the plugin API."
status: pending
priority: 80
labels: ["backend", "api"]
position: 2
---

# Field Definition API

## Outcome

A whitelisted user can manage custom field definitions through the plugin API: create a field with a name, type, and constraints; list all defined fields; update a field's properties; and delete a field. Each field definition includes which project(s) it applies to.

## What & Why

This story builds the CRUD API for field definitions on top of the tables created in S1. Field definitions are the schema layer — they describe what custom fields exist, what type they are, what constraints they have, and which projects they're assigned to. Without them, there's nothing to attach values to.

The API follows Vikunja's conventions for request/response shapes, authentication, and error handling. Access is governed by the config whitelist (S8): only users on the whitelist can manage field definitions. This deliberately bypasses Vikunja's licensed admin feature.

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
6. Field definitions are validated: type must be one of the supported types, name must be non-empty and unique within a project, constraints must be valid for the chosen type.
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