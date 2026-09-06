---
title: "Management UI"
description: "A temporary web interface served by the plugin for managing custom field definitions."
status: done
priority: 60
labels: ["frontend"]
position: 9
---

# Management UI

## Outcome

A whitelisted user (from the management whitelist, S8) can manage custom field definitions through a web interface served by the plugin — no licensed admin panel required. They open the page in the browser, create fields, pick a type, set constraints, assign them to projects, and edit or delete them, all without leaving the browser.

## What & Why

The Field Definition API (S2) works, but requiring whitelisted users to manage fields through curl or an API client is a poor experience. This story provides a web interface served by the plugin that manages field definitions against the same database the API reads.

This is a **temporary** stand-in. It is served from a plugin route so it bypasses Vikunja's licensed admin feature entirely. Authenticating through the user's existing Vikunja browser session, it is gated by the management whitelist (S8). A future epic replaces this with native custom-field management integrated into Vikunja's own interface, for both licensed and unlicensed instances.

## Design Principles

- **Works without a license** — the UI is served by the plugin and whitelist-gated, never touching Vikunja's licensed admin feature.
- **Centrally governed** — the UI is the whitelisted user's tool; it checks the whitelist (S8) before allowing changes.
- **API-first, UI-second** — the UI is a consumer of the S2 API. It adds no new backend capabilities beyond the definition-edit-impact queries (AC#9, plus the delete-impact count) and the management-access capability check: detecting whether a pending definition edit would invalidate existing values requires querying the values table (S3's table), so S9 adds that one query; all other UI functionality consumes existing S2 endpoints.
- **Plugin as proving ground, not permanent home** — the UI is a temporary stand-in; the future epic is native integration.

## Dependencies

- **Must come after:** S2 (Field Definition API), S8 (Config Whitelist)
- **Must come before:** S7 (Build, Deploy & Document)
- **Can run in parallel with:** S3 (Task Field Values API), S5 (Custom Fields on Task Detail)
  > **Note:** the definition-edit-impact warning (AC#9) queries the values table S3 owns. S3 must land first for that feature; the rest of S9's management UI can proceed in parallel with S3.

## Acceptance Criteria

1. A whitelisted user can reach the management UI via a browser URL served by the plugin.
2. The UI lists all defined fields with their type, constraints, and project assignments.
3. A whitelisted user can create a new field definition through a form (name, type, constraints, project assignment).
4. A whitelisted user can edit an existing field definition's properties.
5. A whitelisted user can delete a field definition with a confirmation step.
6. Users not on the whitelist cannot access the management UI.
7. Validation errors from the API are displayed inline on the form.
8. The UI works without any licensed admin feature being enabled.
9. When editing a field definition in a way that would invalidate existing stored values (e.g., removing select options that are in use, or changing a field's type), the UI warns the user with a count of affected values before saving.

## Scope

**In scope:**
- Web interface served by the plugin on a plugin route
- Create, edit, delete forms consuming the S2 API
- List view of existing field definitions
- Inline validation error display
- Whitelist-gated access (via S8)
- Browser-session authentication
- Backend support for detecting definition edits that would invalidate existing values (a count query on the values table by `definition_id`, using the index S3 provides — the query is definitions-API-shaped but touches S3's values table)
- Unauthenticated shell route serving `ui/` assets from disk
- The `management-access` capability endpoint
- The delete-impact count in the delete confirmation (beyond AC#5's letter)
- Web Awesome via pinned CDN (`@3.12.0`)
- Mobile usability (stacked cards, single-column form)

**Out of scope:**
- Integration into Vikunja's native interface — future epic
- A non-whitelisted, self-service field management UI
- Management of field values from the UI (values are managed on tasks — S5)
- Batch operations (bulk delete, import/export)
- Visual preview of how a field will render on a task

## Resolution

**Status:** Done. All 9 acceptance criteria pass, verified by an automated
Playwright walkthrough (headless Chromium against the live Docker test
instance) with curl and `sqlite3` cross-checks on every claim a database can
settle — the impact-dialog counts equal the true row counts, deletion leaves
zero rows, ReadOne matches what the cards rendered, and a non-whitelisted
token gets the not-authorized view plus a server-side 403. Two real bugs were
caught only because a real browser was used — curl-level verification had
shipped both: a relative `app.js` path that 404ed from the no-trailing-slash
`/ui` route (dead page; fixed `3655322`) and an inert `wa-option` `selected`
attribute that silently saved `project_ids: []` (definitions created global
instead of project-assigned; fixed `3245ba2`). The walkthrough also confirmed
the `wa-dialog` Escape/light-dismiss hang in `askConfirm` live; fixed
`b1c7e84`. Lesson recorded for future UI stories: include a minimal Playwright
smoke at implementation time, not only at the AC walkthrough — both curl-blind
bugs were UI-breaking.

**How it was built:** One plugin file (`main.go`) plus two static assets
(`ui/index.html`, `ui/app.js`) — no fork changes, no build step. Backend:
`RegisterUnauthenticatedRoutes` (typed factory `NewUnauthenticatedRouterPlugin`)
mounts `GET /custom-fields/ui` (`uiIndexHandler`, `text/html`) and
`GET /custom-fields/ui/app.js` (`uiAppJSHandler`, `text/javascript`) on the
host's no-JWT route group; files resolve under `uiDir()` (the plugin's
directory on disk), a missing file returns a clean 404, and the group's
inherited `no-store` keeps edited assets fresh. `managementAccessHandler`
answers `GET management-access` with `{"is_manager": bool}` for any
authenticated user. `POST /definitions/:id/impact` (`editImpactHandler`) diffs
the candidate definition — the exact JSON the subsequent PUT will send —
server-side via `computeImpact` and returns `{"affected_values": n}`;
`GET /definitions/:id/impact` (`deleteImpactHandler`) returns the total value
count the `Delete` cascade would destroy. Three narrow counting helpers back
these: `countValuesForDefinition` (type change → all values via the
`definition_id` index), `countValuesUsingRemovedOptions` (removed select
options → distinct `custom_field_value_options` rows), `countValuesOutOfRange`
(tightened numeric range → values outside it). Both impact endpoints are
whitelist-gated like the other writes. Frontend: `ui/index.html` loads Web
Awesome from the pinned CDN (`@3.12.0`); `ui/app.js` (vanilla JS) renders the
auth states (loading → app / not-authorized / expired-token) driven by
`GET management-access` and the token in `localStorage['token']`; a fetch
layer (`api`, `fetchJSONv1`) attaches the Bearer header, normalizes errors to
`{status, message}`, and routes 401 to the expired-token view. The list
renders stacked cards sorted by `display_order`, enriching each with a
per-definition ReadOne for `options`/`project_ids`. The form persists drafts
to `localStorage` (keyed by definition id), swaps the constraint/options
blocks on type change, offers assignment via an all-projects checkbox, a
project picker (`fillProjects`), and add-by-ID, fires the impact dialog
before saving when `affected_values > 0`, and maps S2's stable error messages
to inline field errors (`showFormError`) with a form-level callout fallback.
Deletion runs the GET impact first and shows the count in the confirmation
dialog (`askConfirm`). All API data renders via `textContent`/DOM creation —
never `innerHTML`.

**Notable deviations (with reasons):**
- **Three new endpoints, not the story's "one query."** The story scoped
  backend support as "a count query on the values table by `definition_id`".
  Design verification generalized it: edit-impact needs three distinct
  counting behaviors (type change / removed options / tightened range), the
  delete confirmation needs its own count, and the UI needs a capability
  check (`management-access`) to render its states gracefully. All are
  narrow, management-surface-only queries; the design-principles line above
  is amended to match.
- **N+1 ReadOne in the list view.** S2's list response carries definition
  fields only (relations live on the single-resource ReadOne), so each card
  fetches ReadOne for `options`/`project_ids`. Enriching S2's list contract
  was rejected — it would change a shipped API shape; deferred to
  upstreaming, where list-embeds-relations is the native pattern. Over a
  handful of definitions the cost is nil, and "the UI consumes existing S2
  endpoints" stays true.
- **Web Awesome adopted** (the story named no component library): free-tier
  components from the pinned CDN, no framework, no build. Accepted
  documented trade: third-party JS from `ka-f.webawesome.com` on a
  JWT-handling page; self-hosting under `ui/vendor` (via the
  `data-webawesome` base path) is the offline escape hatch and is on S7's
  pickup list.
- **Corrected definition-read gating fact.** Every S2 definition handler —
  reads included — is whitelist-gated at the handler layer (all four `Can*`
  methods return `IsManager`). AC#6 is therefore enforced server-side on
  every definition call; `GET management-access` is a UX affordance for
  graceful state rendering, not the security boundary, and the
  unauthenticated shell itself carries no data.
- **xorm `IN (?)` slice expansion does not work in this yaegi/SQLite
  environment.** `countValuesUsingRemovedOptions` could not use
  `Where("col IN (?)", []int64)` — the driver receives the slice as a single
  bind parameter and errors. It builds the `IN (?,?,…)` placeholder list
  manually (`strings.Join` + variadic scalar args), with a comment marking
  the idiomatic form for upstream revert.
- **`fetchJSONv1` error normalization.** The spec's error-handling rule
  (401 from any call → expired-token view) required the project-picker fetch
  path to carry the same normalization as `api()`; the original bare
  `Error('HTTP ' + status)` shape with no 401 routing was amended before
  shipping.
- **List-error visibility.** The list-load failure originally rendered into
  the hidden loading state, leaving list errors silent; per the spec
  ("Network/5xx → wa-callout with the message; list/actions stay usable")
  `boot()`'s catch renders into the visible `#list-error` callout instead.
- **Walkthrough fixes (`3655322`, `3245ba2`, `b1c7e84`).** Absolute `app.js`
  path; add-by-ID assigns the `wa-select`'s `value` property instead of
  setting the `selected` attribute on an already-connected option (Web
  Awesome ignores that attribute post-registration); `askConfirm` resolves
  `false` on dialog hide/Escape with a settled guard, so Escape/light-dismiss
  can no longer leave a suspended save forever or leak ok/cancel listeners
  into the next invocation.

**Key decisions (grounded in the spec's upstream evidence):**
- **Plugin-served page (Option A)** — the story's wording, stress-tested
  against hosting the route in the fork's frontend (Vue route / router
  redirect / iframe): all rejected for adding edits to the fork's
  highest-churn shared files in exchange for a surface the native-management
  epic discards wholesale.
- **Unauthenticated shell + `localStorage` token model.** Verified that a
  browser navigation sends no `Authorization` header and Vikunja has no
  session-cookie auth for the API (the JWT lives in `localStorage['token']`,
  attached per-request by the SPA's axios interceptor) — so no backend-served
  page can be token-gated on navigation. The shell is public and
  authenticates its own API calls from JS, the same trust model as Vikunja's
  own SPA.
- **Server-side impact diffing.** The UI sends the candidate definition (the
  exact PUT body) and the server counts — the UI never re-implements
  validation semantics to guess impact.
- **Capability endpoint as a check, not a gate** — `GET management-access`
  always answers 200 for an authenticated user; the real gate stays on every
  definition handler.
- **Count-aware delete confirmation** — one COUNT query powers the AC#5
  dialog; delete is the most destructive operation on the surface, and the
  count is what makes "are you sure" honest.

**What was left open (self-contained):**
- **Unguarded post-success refresh in `saveForm`/`confirmDelete`**
  (`ui/app.js`): after a successful PUT/DELETE, the final `await loadList()`
  runs inside the try whose catch shows form errors — a transient
  list-refresh failure after a *successful* save/delete surfaces as an
  unhandled rejection, a stale list/card, and (in the form path) an error
  rendered into the already-hidden form. Left as polish: the mutation itself
  is committed and durable. Fix: move the refresh outside the try (or wrap it
  in its own catch that surfaces into `#list-error`).
- **Edit-path fetch without catch in `openForm`** (`ui/app.js`): the
  `GET /definitions/:id` that prefills the Edit form has no error handler at
  either call site — a failed read is a silent unhandled rejection and the
  form opens partially filled. Left because no failing ReadOne occurred
  during verification. Fix: catch → form-level error callout and return to
  the list state.
- **`saveForm` re-entrancy on double-click** (`ui/app.js`): no guard prevents
  two concurrent save continuations (double-click Save = two PUTs).
  Pre-existing; neither caused nor worsened by the `askConfirm` fix. Fix:
  disable the save button while a save is in flight.
- **`showFormError` couples inline placement to S2's exact message wording**
  (`ui/app.js`): validation errors are routed to fields by keyword-matching
  S2's stable message strings. Works today, but an S2 wording change would
  silently demote errors to the form-level fallback (still visible, just not
  inline). Fix: have S2 return machine-readable field keys — an upstreaming
  concern.
- **Non-JSON body handling in `api`/`fetchJSONv1`** (`ui/app.js`): error
  responses with non-JSON bodies (e.g. a proxy's HTML 502) surface the raw
  body text (textContent-safe, just ugly), and `fetchJSONv1`'s `res.json()`
  still throws an unnormalized SyntaxError on 204/empty/non-JSON 2xx bodies.
  Left as the sanctioned scope of the 401-routing amendment; no live path
  hits it. Fix: guard the parse and synthesize `{status, message}`.
- **`countValuesOutOfRange` is an in-memory O(n) scan** (`main.go`): the
  tightened-range preview materializes all value rows and parses each one.
  Preview-only path with handfuls of values; the design mandated this shape.
  Fix if value sets ever grow: a SQL-side count (DB-dialect numeric casting)
  or a streaming scan.
- **`editImpactHandler` treats a missing `type` as a full count**
  (`main.go`): a candidate body without `type` is interpreted as a type
  change and counts all values instead of 400. The endpoint's contract is
  "the body is the exact PUT JSON", which always carries `type`; preview-only
  impact. Fix: validate the candidate and 400 on a missing `type`.
- **Project picker is first-page-only** (`fillProjects`, `ui/app.js`): the
  assignment picker lists page 1 of `GET /api/v1/projects` only. Projects
  beyond page 1 remain targetable via add-by-ID (assignment validation is
  existence-only) and already-assigned ones survive as picker tags, but they
  cannot be picked by name. By design; typical instances have handfuls of
  projects. Fix: paginate or search when populating.
- **Erroneous "All projects" on enrichment-failed cards** (`cardFor`,
  `ui/app.js`): when a card's ReadOne enrichment fails, the fallback card
  still renders the affirmative "All projects" line though the assignment is
  unknown — "unknown" would be more truthful. Cosmetic; flagged for the
  browser walkthrough and not reached. Fix: render "Assignment: unknown" on
  the fallback card.
- **Add-by-ID relies on Web Awesome lazy-registration reconciliation**
  (`ui/app.js`): assigning the select's `value` synchronously after
  `appendChild` works per Web Awesome's documented lazy-registration
  semantics; a future Web Awesome behavior change would surface here first.
  Fix if it ever regresses: set the value after the custom element upgrades
  (e.g. in an upgrade callback or microtask).