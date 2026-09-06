# S9 Management UI — Design Spec

**Date:** 2026-09-05
**Story:** `docs/stories/S9-management-ui.md`
**Status:** Approved design (brainstormed 2026-09-05; revised same day after an independent adversarial review)
**Repos touched:** `vikunja-custom-fields-plugin` only — zero changes to the Vikunja fork.

## Summary

A whitelisted manager opens `/api/v1/plugins/custom-fields/ui` in a browser and manages
custom field definitions: list, create, edit (with an invalidation warning), delete (with a
count-aware confirmation). The plugin serves the page as static assets from disk, gated by a
capability endpoint backed by the management whitelist (S8). The UI consumes the existing S2
definition API plus three small new plugin endpoints. Built with Web Awesome components from
a pinned CDN — no framework, no build step, no fork changes.

## Serving decision (assessed, not assumed)

The story's "served by the plugin" wording was stress-tested against three frontend-involved
alternatives before confirming it:

| Option | Verdict |
|---|---|
| **A. Plugin-served page (story as written)** | **Chosen.** Zero fork diff; all code merge-isolated in the plugin repo; fully discarded by the native-management epic. |
| B. Fork frontend hosts a Vue route | Rejected. Adds edits to the fork's highest-churn shared files (`en.json`: 34 upstream commits in 90 days; `router/index.ts` and `Navigation.vue`: 3 each; fork currently 10 commits behind upstream). The one-time build saving does not amortize against a permanent merge tax. Reuse claim is weak: the native epic's management surface is expected to be distributed (per-project creation in project edit + global/admin-adjacent management, labels-style), so a standalone page's view would be discarded wholesale. |
| C. Fork router redirect to the plugin URL | Rejected. Cosmetic only — the address bar still ends on the `/api/…` URL, and it requires option A underneath plus a fork change. |
| D. Fork route iframe of the plugin page | Rejected. Keeps a nice URL and app chrome but still requires A's hand-rolled page, adds iframe quirks (theme inheritance, framing headers), and the wrapper is discarded too. |

Option A keeps the PRD unchanged — "temporary web interface served by the plugin" remains
true in every clause.

## Verified mechanics the design rests on

- Plugin routes mount at `/api/v1/plugins/<plugin-chosen-path>` for **both** the
  authenticated and unauthenticated groups (`pkg/routes/routes.go:984,987`). The
  unauthenticated group is created before the JWT middleware is added to the API group
  (`routes.go:497` vs `:556`), so it needs no token — exactly what a browser GET needs.
  (Note: `vikunja-docs` claims unauthenticated plugin routes live at root `/plugins/` — the
  code says otherwise; the docs are wrong.)
- A plugin can serve non-JSON responses: `echo.Context` is exported to yaegi as a concrete
  struct type (echo v5 declares `type Context struct`; `pkg/yaegi_symbols/echo.go:155`
  exports it via `reflect.ValueOf((*echo.Context)(nil))`),
  so `c.HTML`/`c.Blob`/`c.NoContent` all work. No middleware transforms plugin responses:
  the plugin groups inherit only `noStoreCacheControl` from the API group (`pkg/routes/routes.go`),
  nothing JSON-wrapping. Both groups send `no-store` cache control.
- `go:embed` is unavailable in interpreted code (no `embed` package in yaegi's stdlib), but
  `os` and `path/filepath` are (`stdlib/go1_21_os.go`, `go1_21_path_filepath.go` —
  `os.ReadFile` is exported). Yaegi code runs unsandboxed in-process; disk reads work.
- A browser navigating to any URL sends no `Authorization` header, and Vikunja has no
  session-cookie auth for the API — the JWT lives in `localStorage['token']`
  (fork `frontend/src/helpers/auth.ts`) and is attached per-request by the SPA's axios
  interceptor (`frontend/src/helpers/fetcher.ts`). The
  only cookie is the HttpOnly refresh token, scoped to the refresh endpoints
  (`pkg/modules/auth/auth.go:57-108`). Therefore **no page served by this backend can be
  token-gated on navigation**; the shell must be served unauthenticated and authenticate its
  own API calls from JS. This is the same trust model as Vikunja's own SPA (`index.html` is
  public; auth happens client-side).
- Plugin facts: `IsManager(username)` exists (`main.go:537`); handlers get the user via
  `user.GetCurrentUser(c)`; `validateAssignment` checks project **existence only** — no
  permission check; definition `Delete` **cascade-destroys** values, value options, options,
  and assignments (`main.go`, verified); values are stored in one `text` column
  (`CustomFieldValue.Value`) plus the `custom_field_value_options` join table for
  multi-select; `FieldConfig` = `{required, default, min*, max*, is_api_only}` (min/max are
  pointers so 0 ≠ unset); options carry `{value, label, display_order}`.
- Every S2 definition handler — reads included — is whitelist-gated at the handler layer
  (all four `Can*` methods return `IsManager`, `main.go:451-466`); S5's task rendering gets
  definitions through the S3 values response (task-permission-gated), never the definitions
  endpoints. AC#6 is therefore enforced server-side on every definition call; the capability
  check below is what lets the UI render the correct state gracefully before any data call.

## Architecture

```
/api/v1/plugins/custom-fields/
  ui                      GET  (unauth) → serves ui/index.html from disk
  ui/app.js               GET  (unauth) → serves ui/app.js from disk
  management-access       GET  (auth)   → {"is_manager": bool}            [new]
  definitions/…           (existing S2 CRUD, auth, whitelist-gated writes)
  definitions/:id/impact  POST (auth)   → edit-impact count (AC#9)         [new]
  definitions/:id/impact  GET  (auth)   → delete-impact count              [new]
  tasks/:task/…           (existing S3 values, untouched)
```

**Asset serving.** `main.go` gains two handlers on the **unauthenticated** group. Paths are
fixed constants (no route params — no traversal surface). Files resolve via
`filepath.Join(viper.GetString("plugins.dir"), "custom-fields", "ui", <file>)`;
`plugins.dir` is absolute in both the test and deployment configurations, but if a relative
value is ever configured, resolve it against Vikunja's rootpath (exact viper key confirmed at
implementation). `index.html` goes out with
`text/html; charset=utf-8` (via `c.HTML`), `app.js` with `text/javascript; charset=utf-8`
(`c.Blob`, explicit MIME string). A missing/unreadable file returns a clean 404 — this is the
degenerate case of a plugin loaded without its assets. Both plugin groups already send
`no-store`, so edited assets never linger. The compose test mount
(`./:/app/vikunja/plugins/custom-fields`, wholesale) carries `ui/` automatically. The S7
deployment mount is **pending** (S7 status: pending): it must mount the plugin directory
wholesale as its story describes, or `ui/` will be absent in production — confirming this is
on S7's pickup list.

**Auth model.** The shell is inert static HTML/JS. On load, `app.js` reads
`localStorage['token']`; missing/blank → "open Vikunja and log in" view. It then calls
`GET management-access` with a Bearer header: `is_manager: false` renders the not-authorized
view (AC#6); `true` renders the app. Every subsequent call is the same Bearer pattern curl
users make today. No cookies, no CSRF surface, no session machinery. A 401 from any call
renders "your session has expired — open Vikunja and log in again" with a link to `/`; the
temp page does **not** replicate the SPA's cookie-based token refresh.

**Security posture.** Field names/options are user-controlled data rendered by hand-rolled
JS: everything renders via `textContent`/DOM element creation, never `innerHTML` with API
data. The page loads third-party JS (Web Awesome CDN — below) on a page that handles the
user's JWT; accepted as a bounded, documented trade at a pinned version. For instances
handling sensitive data, self-hosting (the `ui/vendor` path) is the recommended default;
the CDN is the accepted trade for this project's proving-ground deployment, revisitable
before S7.

## Backend additions

Three small endpoints, all in `main.go`, following the existing handler conventions
(`user.GetCurrentUser`, `db.NewSession()`, plugin-local 9000s error codes translated via
`toHTTPError`).

1. **`GET /management-access`** — `user.GetCurrentUser(c)` → `IsManager(u.Username)` →
   `200 {"is_manager": bool}`. Always 200 for any authenticated user: it is the gate *check*,
   not a gated operation.
2. **`POST /definitions/:id/impact`** (whitelist-gated like the CUDs) — body is the
   **candidate** definition, the exact JSON the subsequent PUT will send. Server-side diffing
   is authoritative; the UI must not re-implement validation semantics to guess impact.
   Response: `200 {"affected_values": n}`. Diff semantics:

   | Candidate edit | Counted |
   |---|---|
   | Type changed | **All** values for the definition (one count on the `definition_id` index — S3's) |
   | Select or multi-select, options removed | Distinct `custom_field_value_options` rows for the definition whose option IDs are among the removed options — **both** types store option linkage exclusively in that join table (`writeValue` sets `Value = ""` for all select-like types); "removed" is diffed by option `Value` strings, the key `setOptions` reconciles on |
   | Integer/decimal, range tightened | Values parsing to a number outside the new range |
   | Rename, description, `display_order`, `is_api_only`, `required`, added options, default | 0 — no stored value can be invalidated |

   At most three narrow count queries per call; the story's sanctioned "one query on the
   values table by `definition_id`" is the type-change case, generalized. Unparseable stored
   values are surfaced only by a type change, which already counts everything.
3. **`GET /definitions/:id/impact`** (**whitelist-gated** via `IsManager`, same as the POST —
   the count is management-surface usage data) — same response shape, meaning "if deleted":
   the total value count, since `Delete` cascade-destroys values. This powers the AC#5 confirmation
   dialog. Explicitly flagged as a small scope addition beyond AC#5's letter (a bare "are you
   sure" is the alternative); adopted because the query is one COUNT and delete is the most
   destructive operation on the surface.

## UI

**Files:** `ui/index.html` (inline `<style>`, `<meta name="viewport"
content="width=device-width, initial-scale=1">`, `wa-cloak` on the app root) and `ui/app.js`
(vanilla JS). No framework, no build, no i18n — hardcoded English. Neutral light styling from
the Web Awesome default theme; no dark-mode sync.

**Web Awesome via pinned CDN.** `index.html` loads, from
`https://ka-f.webawesome.com/webawesome@<pinned>`: `styles/themes/default.css`,
`styles/utilities.css`, and `webawesome.loader.js` (autoloader). The version pin (3.12.0 as
of this spec) is confirmed against the current stable at implementation and recorded in the
pinned URL. Components used (all free-tier): `wa-button`, `wa-input`, `wa-number-input`,
`wa-select`/`wa-option`, `wa-checkbox`, `wa-textarea`, `wa-badge`, `wa-callout`, `wa-dialog`,
plus `wa-stack`/`wa-split` utilities and the `wa-cloak` CSS class (FOUCE prevention — a
class, not a component). Custom elements always get closing
tags; events are the `wa-*` custom events. Costs, accepted: third-party JS from a pinned
immutable CDN path on a JWT-handling page (the autoloader's lazy chunks can't carry SRI
hashes, so the trust boundary is Fonticons + their CDN at that version); browsers need
internet access to the CDN — an air-gapped instance renders a broken page, documented in S7,
no offline fallback built. Escape hatch if the dependency ever needs to go: `npm pack` the
package and self-host `dist-cdn` under `ui/vendor` — no build
step either way; the asset base path is set via the `data-webawesome` script attribute (or
the `setBasePath()` method).

**States:** loading (spinner) → not-authorized (`wa-callout`, AC#6) / expired-token (link to
`/`) / app (list + form). Show/hide between two app states — no client-side routing, no
history management.

**List view.** `GET /definitions` returns definition fields **only** — id, name, type,
description, `field_config`, `display_order` (`definitionFieldsMap`, "no relations"); the
relations-rich shape (`options`, `project_ids`, `[]` = global) is the single-resource
ReadOne response. The card therefore renders from the list response and **fetches ReadOne
per definition** to obtain `options` and `project_ids` — an N+1 over a handful of
definitions, which keeps the story's "the UI consumes existing S2 endpoints" principle
intact (AC#2's project-assignment display comes from the ReadOne data). Enriching S2's list
response was rejected: it would change S2's shipped contract — deferred to upstreaming,
where list endpoints embedding relations is the native shape. Cards render as **stacked
definition cards** — deliberately not a
`<table>` for mobile friendliness (a card reflows by construction; typical instance has a
handful of definitions): name + type badge on the first line, then compact label/value lines
(required, min–max when set, option count, "All projects"/"N projects", API-only badge),
Edit/Delete buttons per card. Sorted by `display_order`.

**Form (create/edit).**
- Identity: name, description, `display_order`, type (`wa-select`, the 10 S2 types).
- `field_config`: required, default, min/max (`wa-number-input`, shown for integer/decimal;
  empty = unset, preserving pointer semantics), `is_api_only`.
- Options editor (select/multi-select only): rows of value + label with add/remove/up/down
  buttons; row order becomes `display_order` on save.
- Assignment: "All projects" checkbox (`project_ids: []`) or a project list populated from
  the standard `GET /api/v1/projects` with the same token, **plus** an "add project by ID"
  input — assignment validation is existence-only, so managers can target projects their own
  account cannot see, and the picker must not pretend otherwise.
- **Create:** `POST /definitions`. Validation errors render inline: S2's error messages are
  stable strings, mapped to their field (name/type/options/assignment); unrecognized
  messages fall back to a form-level `wa-callout` (AC#7).
- **Edit:** the in-progress form state is persisted to `localStorage` (keyed by definition
  id) on every change and restored on load, so the expired-token detour — which ends in a
  page reload — does not destroy mid-edit work. Save click first fires
  `POST /definitions/:id/impact` with the candidate body;
  `affected_values > 0` raises a `wa-dialog` — "This edit invalidates N stored value(s)" —
  Save anyway / Cancel — then `PUT` (full-replace, S2 semantics) and list refresh (AC#9).
- **Delete:** `GET /definitions/:id/impact` first, then the confirm `wa-dialog` — "This
  permanently deletes the field and its N stored values" — then `DELETE` and refresh (AC#5).
- Changing the type in the form swaps the constraint/options sections; the impact check
  catches an invalidating save.

**Fetch layer.** ~40-line wrapper in `app.js`: Bearer from `localStorage['token']`, JSON
in/out, normalizes errors to `{status, message}`, routes 401 to the expired view.

## Error handling

- Non-manager → not-authorized view; every server-side definition call remains 403
  regardless (defense in depth — the UI is convenience, the API is the gate).
- 401 (any call) → expired-token view.
- Network/5xx → `wa-callout` with the message; list/actions stay usable.
- Missing asset file → 404 from the plugin (documented degenerate case).
- Malformed candidate on impact POST → 400 with the S2 validation message, surfaced inline.

## Testing & verification

No unit-test harness exists or is warranted: the plugin has no Go test suite (S1–S3 were
verified live), and the UI is framework-free with no build tooling. Verification follows the
established live-instance pattern:

- `./scripts/run-test-env.sh` (seeds whitelisted user); projects/tasks seed via the
  established script flow, and **values seed via the S3 API or direct `sqlite3` inserts** —
  the generic `PUT /api/v2/test/{table}` endpoint addresses Vikunja's native tables and has
  no established path to plugin-created tables; `curl` drives the endpoints;
  `sqlite3 db/vikunja.db` proves counts.
- Headed-browser AC walkthrough on the test instance. AC#6 uses a **non**-whitelisted user's
  token (not-authorized view + server-side 403 on writes). AC#7 via known-invalid submissions
  (options on a non-select type, blank name). AC#9's `affected_values` must equal the sqlite
  count for a seeded table (type change and option-removal cases both exercised).
- Browser internet access to `ka-f.webawesome.com` is part of the documented contract.

## Downstream amendments

**S9 story doc** (applied when the story resolves, per the Resolution convention):
- Design-principles line "adds no new backend capabilities beyond a definition-edit-impact
  query" becomes "…beyond the definition-edit-impact queries (edit + delete count) and the
  management-access capability check."
- Scope additions: unauthenticated shell route serving `ui/` assets from disk; the
  capability endpoint; the delete-impact count (beyond AC#5's letter); Web Awesome via
  pinned CDN; mobile usability (stacked cards, single-column form).

**S7 pickup list:** document the UI URL (`/api/v1/plugins/custom-fields/ui`), the CDN
requirement, and that `ui/` requires no deployment change.

**PRD:** unchanged.

## Out of scope (in addition to the story's list)

- Any fork/frontend change — including nice-URL redirects or iframes (assessed, rejected).
- Offline/CDN fallback for the page.
- i18n, dark-mode sync, visual polish beyond the default theme.
- JS unit tests; browser-automation e2e for the temp page.
- Replicating the SPA's token-refresh flow in the page.
