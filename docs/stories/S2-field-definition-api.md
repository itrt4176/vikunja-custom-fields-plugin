---
title: "Field Definition API"
description: "Admin can create, read, update, and delete custom field definitions via the plugin API."
status: pending
priority: 80
labels: ["backend", "api"]
position: 2
---

# Field Definition API

## Outcome

An admin can manage custom field definitions entirely through the plugin API: create a field with a name, type, and constraints; list all defined fields; update a field's properties; and delete a field. Each field definition includes which project(s) it applies to.

## What & Why

This story builds the CRUD API for field definitions on top of the tables created in S1. Field definitions are the schema layer — they describe what custom fields exist, what type they are, what constraints they have, and which projects they're assigned to. Without them, there's nothing to attach values to.

The API follows Vikunja's conventions for request/response shapes, authentication, and error handling. Admin-only authorization is enforced: only instance admins can manage field definitions.

## Design Principles

- **API-first, UI-second** — this story delivers the API surface for field definitions before any UI exists. The config file parser (S4) and admin UI (S6) are both consumers of this same API contract.
- **Admin-managed, centrally governed** — field definitions can only be created or modified by an admin.
- **Plugin as proving ground, not permanent home** — the API is designed as if it were a `/api/v2/custom-fields` resource, just served from the plugin route prefix.

## Dependencies

- **Must come after:** S1 (Plugin Foundation)
- **Must come before:** S3 (Task Field Values API), S4 (Config File), S6 (Admin Field Management UI)
- **Can run in parallel with:** none

## Acceptance Criteria

1. An admin can create a field definition with a name, type, optional constraints, and project assignment via the API.
2. An admin can list all field definitions, optionally filtered by project.
3. An admin can update a field definition's properties (name, type, constraints, project assignment).
4. An admin can delete a field definition.
5. Non-admin users receive an authorization error when attempting any field definition operation.
6. Field definitions are validated: type must be one of the supported types, name must be non-empty and unique within a project, constraints must be valid for the chosen type.
7. The API response shapes follow Vikunja conventions (snake_case, consistent envelope).

## Scope

**In scope:**
- Full CRUD API for field definitions
- Validation (types, required fields, constraints)
- Admin-only authorization
- Project assignment (one field → one or more projects, or all projects)

**Out of scope:**
- Storing or retrieving field values on tasks — S3
- Config file parsing — S4
- Any frontend changes
- Soft-delete or archival of fields
- "API-only" field flag — defer to S3