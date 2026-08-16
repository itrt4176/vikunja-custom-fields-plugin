# S8 — Config Whitelist: Design Spec

**Date:** 2026-08-16
**Story:** [S8 — Config Whitelist](../../stories/S8-config-whitelist.md)
**Status:** Draft (pending user spec review)

## Summary

S8 declares who may manage custom fields. A whitelist of usernames, read by the
plugin at startup, defines the users permitted to create, edit, and delete field
definitions. It is exposed to the field-definition API (S2) and the management UI
(S9) as a single predicate. Users not on the whitelist are denied management
operations — enforced by S2/S9, not by S8.

This is the license-free governance surface the PRD calls for: it lives in
configuration the admin edits, never touches Vikunja's licensed admin feature,
and is consumed by both the API and the temporary UI.

## Context — why the whitelist is an env var, not a config-file section

S8's story and the PRD both say the whitelist lives "in Vikunja's config file."
That is the intent this spec honors. The mechanism differs, and the reason is a
hard constraint verified against the live `vikunja/` fork source (and recorded in
the S1 spec, line 25):

> **The yaegi symbol table does not register `pkg/config`.** Plugins can import
> `db`, `log`, `models`, `user`, `events`, `plugins`, echo, and watermill — but
> not `config` (and not `web`). A plugin cannot call `config.Key("…").GetStringSlice()`.

Reading the whitelist from Vikunja's config file would therefore require a fork
modification: adding `pkg/config` to the yaegi symbol table (one generated file
plus one entry in `yaegiSymbolPackages` in `magefile.go`, regenerated via
`mage generate:yaegi-symbols`). S1 deliberately kept the fork untouched for its
scope; exposing an existing package to plugins is a behavior-neutral enabler, but
it is still a fork change worth making deliberately rather than by accident.

**Decision (user-approved): read the whitelist from an environment variable,
with no fork change.** The plugin reads `VIKUNJA_CUSTOMFIELDS_WHITELIST` with the
stdlib `os` package in `Init()`. This departs from the story's literal "config
file" wording — the story, PRD, and S2/S9 docs are updated to say "env var" so
docs and implementation agree. The feature's name ("management whitelist") is
unchanged; only the mechanism changes.

### Forward compatibility

The env-var name is chosen so that a future swap to `pkg/config` needs no
renaming. Vikunja's viper uses prefix `VIKUNJA_` and replaces `.` with `_`
(`config.go:631-633`), so `config.Key("customfields.whitelist")` is overridden by
`VIKUNJA_CUSTOMFIELDS_WHITELIST`. The config-source accessor is isolated in one
function (`loadWhitelist`); switching the source later is a one-function edit.

## Independently verified facts (live `vikunja/` fork source)

Checked against the current source, not inherited from the abandoned branches:

- **`pkg/config` is not in the yaegi symbol table.** `magefile.go:1426`
  (`yaegiSymbolPackages`) registers `db`, `events`, `log`, `models`, `plugins`,
  `user`, echo, watermill — not `config`. Confirmed by `grep config
  pkg/yaegi_symbols/*.go` (no hits).
- **`user.GetCurrentUser(c *echo.Context) (*user.User, error)` IS available** to
  plugins (`vikunja_user.go:65`, wrapping `pkg/user/user.go:490`). It resolves
  the authenticated caller from the JWT in the echo context. The handler uses it
  to read the requesting username.
- **`user.User` has a `Username string` field** (`xorm:"varchar(250) not null
  unique"`, `user.go:93`). This is the identifier the whitelist matches against.
- **Username comparison is case-sensitive at the DB layer.** Vikunja's
  `getUser` does a direct `username = ?` (`user.go:354`) with no forced
  lowercasing on read; case sensitivity then depends on the DB collation (SQLite
  ASCII-case-insensitive, Postgres case-sensitive). The whitelist comparison is
  therefore **normalized to lowercase on both sides**, so behavior is consistent
  and admin-friendly regardless of database.
- **Viper maps `customfields.whitelist` → `VIKUNJA_CUSTOMFIELDS_WHITELIST`.**
  `config.go:631-633`: `SetEnvPrefix("vikunja")`, `SetEnvKeyReplacer(".", "_")`.
  This is the basis for the forward-compatibility claim above.

### The one unverified assumption

That the authenticated route group's echo context carries the JWT in the shape
`user.GetCurrentUser` expects (`c.Get("user").(*jwt.Token)`, `user.go:498`).
S1's health route already proves the authenticated group is JWT-gated and a valid
JWT reaches it; `GetCurrentUser` is the standard way handlers read that user.
This is confirmed during S8 implementation with the test route — if it returns
401 unexpectedly, the context-shape assumption is the place to look.

## Architecture

S8 adds three things to the existing single `main.go` (package `main`):

1. **A whitelist set**, populated in `Init()` from the env var, held in a
   package-level variable.
2. **`IsManager(username string) bool`** — the sole seam S2 and S9 call.
3. **A temporary verification route** proving the predicate resolves correctly
   for the authenticated caller, removed once S2 exercises the predicate on real
   endpoints.

No core Vikunja files are modified. The fork is untouched. Imports added to
`main.go`: `os`, `strings` (stdlib), and `code.vikunja.io/api/pkg/user` — all
verified available to yaegi.

## Config schema

- **Env var: `VIKUNJA_CUSTOMFIELDS_WHITELIST`** — comma-separated usernames,
  e.g. `alice,bob,carol`.
- Whitespace around the list and around individual entries is trimmed and
  tolerated (`alice, bob , carol` is equivalent to `alice,bob,carol`).
- **Absent or empty → empty whitelist → no one may manage fields** (AC#5). Not an
  error; logged at info.
- No DB existence check on entries. A whitelisted username that does not yet
  exist is a valid pre-provisioning state, not an error — and existence checks at
  startup would couple S8 to user data it does not own.

## Parsing, malformed entries, and the accessor

Parsing happens once, in `Init()`:

```go
// loadWhitelist reads the management whitelist from configuration and returns
// a lowercase-normalized set of permitted usernames. Source is isolated here so
// a future swap to pkg/config is a one-function change.
func loadWhitelist() map[string]struct{} {
	set := map[string]struct{}{}
	raw := os.Getenv("VIKUNJA_CUSTOMFIELDS_WHITELIST")
	if raw == "" {
		return set
	}
	for i, entry := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(entry))
		if name == "" {
			log.Errorf("[custom-fields] whitelist: ignoring empty entry at position %d", i)
			continue
		}
		set[name] = struct{}{}
	}
	return set
}
```

- A **malformed entry** is one that is empty after trimming (e.g. `alice,,bob` —
  the middle entry, or a trailing comma `alice,bob,`). It logs a clear error
  naming the position and is skipped. **Vikunja does not crash** (AC#4): `Init`
  continues and returns nil; the malformed entry simply is not in the set.
- Empty/absent var logs at info: `[custom-fields] whitelist empty — no users may
  manage custom fields`.
- A non-empty whitelist logs the count: `[custom-fields] whitelist loaded: N
  manager(s)` (usernames are not logged — they are identifiers, but logging them
  adds nothing and risks noise).

The set is stored in a package-level variable populated in `Init()` before routes
register, so `IsManager` reads a fully-initialized map with no per-request
parsing and no concurrency concerns (write once at startup, read-only after).

## The seam

```go
// IsManager reports whether username is on the management whitelist. It is the
// single authorization check S2 (field-definition API) and S9 (management UI)
// call before allowing field-definition changes. Deny-by-default: an empty
// whitelist denies everyone.
func IsManager(username string) bool {
	if username == "" {
		return false
	}
	_, ok := whitelist[strings.ToLower(username)]
	return ok
}
```

- **Deny-by-default** (AC#3): empty set → `false` for everyone; empty/blank
  username → `false`.
- Case-insensitive on both sides (see verified facts).
- S2 and S9 import and call this directly. S8 does not wire it into any
  field-definition operation — that enforcement is S2's and S9's responsibility.

## Temporary verification route

Mounted alongside the S1 health route, authenticated (any valid JWT). Its purpose
is to prove the predicate resolves correctly for the authenticated caller so S8
can be verified end-to-end in isolation. It is **not** a management surface.

```go
// managerHandler is a temporary S8 verification route: it proves the whitelist
// predicate resolves correctly for the authenticated caller. It is not a
// management surface — S2/S9 enforce IsManager on the real endpoints. Remove
// this route once S2 is in place and the predicate is exercised there.
func managerHandler(c *echo.Context) error {
	u, err := user.GetCurrentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"username":   u.Username,
		"is_manager": IsManager(u.Username),
	})
}

func (p *CustomFieldsPlugin) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.GET("/custom-fields/health", healthHandler)          // S1 throwaway
	g.GET("/custom-fields/manager", managerHandler)        // S8 temporary, remove after S2
}
```

- 200 with `{ "username": …, "is_manager": bool }` for a valid JWT.
- 401 if the user cannot be resolved from context (covers the unverified
  context-shape assumption above).
- Removal is a tracked, named step: once S2 enforces `IsManager` on its CRUD
  endpoints and the predicate's allow/deny is exercised there, this route and
  its handler are deleted. The doc comment records both facts.

## Scope boundary — what S8 does and does not own

S8 **provides the predicate, parsing, logging, and the temporary proof route.**
It does **not** enforce the whitelist on field-definition operations. That
enforcement is S2's (on the field-definition API) and S9's (on the management
UI), per the story's own wording ("checked by S2/S9"). Concretely:

- S8 verifies its **predicate is correct**: whitelisted → `is_manager: true`,
  non-whitelisted → `is_manager: false` (via the temporary route).
- S2/S9 verify **enforcement**: returning 403 on actual field-definition
  operations for non-managers, by calling `IsManager`.
- AC#6 ("shared with both S2 and S9") is proven when S2 imports and calls
  `IsManager`, not by anything S8 does alone.

The acceptance criteria are read with this split in mind so S8 does not claim
enforcement it does not own.

## Testing strategy

The plugin source cannot be `go test`ed standalone (its imports resolve only
inside the vikunja module), so verification is **integration via the existing test
instance** (`compose.test.yml`, SQLite) — the same approach as S1.

The env var is set in `compose.test.yml`'s `environment:` block for the test
container (e.g. `VIKUNJA_CUSTOMFIELDS_WHITELIST=testuser`), so the test harness
exercises a populated whitelist. The test user is seeded by `run-test-env.sh` as
today.

### Acceptance-criteria verification

| AC | How verified |
|---|---|
| 1. Whitelist can be declared | Set `VIKUNJA_CUSTOMFIELDS_WHITELIST` in `compose.test.yml`, restart — startup log shows the loaded-count line. |
| 2. Whitelisted user allowed | `curl /api/v1/plugins/custom-fields/manager` with the whitelisted test user's JWT → `{ "is_manager": true }`. |
| 3. Non-whitelisted user denied | Seed/log in a second user not on the whitelist; same route → `{ "is_manager": false }`. (Enforcement on field-definition ops is S2/S9 — out of S8's scope.) |
| 4. Malformed entry → error, no crash | Set `VIKUNJA_CUSTOMFIELDS_WHITELIST=alice,,bob`, restart — startup log shows the empty-entry error; Vikunja stays healthy; `alice` and `bob` (if seeded) still resolve as managers. |
| 5. Absent → empty, no crash | Unset the var, restart — empty-whitelist info line; route returns `is_manager: false` for everyone; Vikunja healthy. |
| 6. Shared with S2 and S9 | Proven when S2 imports and calls `IsManager` (S2's verification, not S8's). |

AC#3 requires a second, non-whitelisted test user. `run-test-env.sh` currently
seeds one regular test user; a second user is added as part of S8's test-harness
update (via the existing `/api/v2/test/users` seed endpoint) so the deny path can
be demonstrated. This is a harness change, not a plugin-source change.

## Doc updates (consistency with the env-var decision)

Switching from "config file" to "env var" departs from current wording in
several places. These are updated as part of S8 so docs and implementation agree:

- **S8 story** (`docs/stories/S8-config-whitelist.md`): outcome and AC#1 say
  "Vikunja's config file" → "the `VIKUNJA_CUSTOMFIELDS_WHITELIST` env var". Title
  stays "Config Whitelist" (the concept is unchanged).
- **PRD.md** (`docs/PRD.md`): the "Config file — single responsibility" line and
  the "config-declared whitelist" phrasings → "env-var-declared whitelist".
- **S2 story** and **S9 story**: "config whitelist (S8)" references →
  "env-var whitelist (S8)" (or keep "whitelist (S8)" where the mechanism is not
  the point).

Only mechanism references change; the whitelist concept and its governance role
are unchanged.

## Git workflow

Per `CLAUDE.local.md`: git-flow is mandatory, Conventional Commits. S8's spec
and implementation plan are committed on `develop` (matching S1's flow — the S1
spec `f08a6a2` and plan `0a81e29` were committed on `develop`). The actual S8
code lands on a `feature/s8-config-whitelist` branch off `develop`.

A worktree decision is raised before any development work, per `CLAUDE.local.md`.
The implementation plan (next step) details the branch/commit structure and
includes Required/Recommended skills lists per `CLAUDE.local.md`.

## Out of scope

- Enforcing the whitelist on field-definition CRUD — S2.
- The management UI that consumes the whitelist — S9.
- Wiring the whitelist into field-value operations — values are per-task, not
  management; out of scope for S8 entirely.
- Reading the whitelist from Vikunja's config file (would require exposing
  `pkg/config` to yaegi — a fork change; deferred; the accessor is isolated so
  this remains a one-function swap).
- Hot-reloading the whitelist without restart.
- A persistent (non-temporary) management-status endpoint.
