---
title: "Management UI"
description: "A temporary web interface served by the plugin for managing custom field definitions."
status: pending
priority: 60
labels: ["frontend"]
position: 6
---

# Management UI

## Outcome

A whitelisted user (from the config whitelist, S4) can manage custom field definitions through a web interface served by the plugin — no licensed admin panel required. They open the page in the browser, create fields, pick a type, set constraints, assign them to projects, and edit or delete them, all without leaving the browser.

## What & Why

The Field Definition API (S2) works, but requiring whitelisted users to manage fields through curl or an API client is a poor experience. This story provides a web interface served by the plugin that manages field definitions against the same database the API reads.

This is a **temporary** stand-in. It is served from a plugin route so it bypasses Vikunja's licensed admin feature entirely. Authenticating through the user's existing Vikunja browser session, it is gated by the config whitelist (S4). A future epic replaces this with native custom-field management integrated into Vikunja's own interface, for both licensed and unlicensed instances.

## Design Principles

- **Works without a license** — the UI is served by the plugin and whitelist-gated, never touching Vikunja's licensed admin feature.
- **Centrally governed** — the UI is the whitelisted user's tool; it checks the whitelist (S4) before allowing changes.
- **API-first, UI-second** — the UI is a consumer of the S2 API. It adds no new backend capabilities, only a browser interface to existing ones.
- **Plugin as proving ground, not permanent home** — the UI is a temporary stand-in; the future epic is native integration.

## Dependencies

- **Must come after:** S2 (Field Definition API), S4 (Config Whitelist)
- **Must come before:** S7 (Build, Deploy & Document)
- **Can run in parallel with:** S5 (Custom Fields on Task Detail)

## Acceptance Criteria

1. A whitelisted user can reach the management UI via a browser URL served by the plugin.
2. The UI lists all defined fields with their type, constraints, and project assignments.
3. A whitelisted user can create a new field definition through a form (name, type, constraints, project assignment).
4. A whitelisted user can edit an existing field definition's properties.
5. A whitelisted user can delete a field definition with a confirmation step.
6. Users not on the whitelist cannot access the management UI.
7. Validation errors from the API are displayed inline on the form.
8. The UI works without any licensed admin feature being enabled.

## Scope

**In scope:**
- Web interface served by the plugin on a plugin route
- Create, edit, delete forms consuming the S2 API
- List view of existing field definitions
- Inline validation error display
- Whitelist-gated access (via S4)
- Browser-session authentication

**Out of scope:**
- Integration into Vikunja's native interface — future epic
- A non-whitelisted, self-service field management UI
- Management of field values from the UI (values are managed on tasks — S5)
- Batch operations (bulk delete, import/export)
- Visual preview of how a field will render on a task