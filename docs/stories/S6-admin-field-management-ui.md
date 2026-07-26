---
title: "Admin Field Management UI"
description: "Admins can create, edit, and delete custom field definitions through a UI in Vikunja's admin panel."
status: pending
priority: 60
labels: ["frontend"]
position: 6
---

# Admin Field Management UI

## Outcome

An admin can open Vikunja's admin panel, navigate to a custom fields section, and manage field definitions through a UI instead of raw API calls. They can create a field, pick its type, set its constraints, assign it to projects, edit it, and delete it — all without leaving the browser.

## What & Why

The Field Definition API (S2) works, but requiring admins to manage fields through curl or an API client is a poor experience. This story provides a proper UI in Vikunja's admin panel for managing field definitions. It consumes the same API endpoints built in S2.

This is also what separates config-declared fields from UI-managed ones: the admin panel requires a license, so this story only serves licensed instances. Config-based definitions (S4) remain the path for unlicensed instances.

## Design Principles

- **Admin-managed, centrally governed** — this story is the admin-facing UI for that governance.
- **API-first, UI-second** — the UI is a consumer of the S2 API. It adds no new backend capabilities, only a browser interface to existing ones.
- **Indistinguishable from native fields** — the admin panel should feel like a native Vikunja admin page, not a bolted-on plugin page.

## Dependencies

- **Must come after:** S2 (Field Definition API), S5 (Custom Fields on Task Detail)
- **Must come before:** S7 (Build, Deploy & Document)
- **Can run in parallel with:** none

## Acceptance Criteria

1. An admin can navigate to a custom fields admin page from the existing admin panel navigation.
2. The page lists all defined fields with their type, constraints, and project assignments.
3. An admin can create a new field definition through a form (name, type, constraints, project assignment).
4. An admin can edit an existing field definition's properties.
5. An admin can delete a field definition with a confirmation step.
6. Validation errors from the API are displayed inline on the form.
7. The admin panel page requires a license (consistent with Vikunja's existing admin panel behavior).
8. The page follows Vikunja's existing admin panel visual patterns and conventions.

## Scope

**In scope:**
- Admin panel page for field definition management
- Create, edit, delete forms consuming the S2 API
- List view of existing field definitions
- Inline validation error display

**Out of scope:**
- A non-admin, self-service field management UI (per PRD: "out of scope")
- Management of field values from the admin panel (values are managed on tasks — S5)
- Batch operations (bulk delete, import/export)
- Visual preview of how a field will render on a task