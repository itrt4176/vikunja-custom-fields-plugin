# S8 — Config Whitelist: Design Spec

**Date:** 2026-08-16 (mechanism amended 2026-08-29)
**Story:** [S8 — Config Whitelist](../../stories/S8-config-whitelist.md)
**Status:** Approved (mechanism amended 2026-08-29)

## Amendment note (2026-08-29)

The original spec chose an **environment variable** as the whitelist source
because, at the time, `pkg/config` and `github.com/spf13/viper` were **not** in
Vikunja's yaegi symbol table, so a plugin could not read Vikunja's config file.
Two things removed that constraint:

1. Upstream PRs [#3502](https://github.com/go-vikunja/vikunja/pull/3502) (expose
   `pkg/config` + `viper` to yaegi) and [#3549](https://github.com/go-vikunja/vikunja/pull/3549)
   (expose `xorm` + `xormigate`) are merged and backported to the project's
   `itrt4176/vikunja:2.5-plugin-fix-backport` test image. `develop`'s S1 has been
   redone to create tables via an `xormigrate` migration (no longer raw SQL in
   `Init()`).
2. The first execution pass discovered `os.Getenv` is **infeasible under yaegi**:
   the loader (`pkg/plugins/yaegi/loader.go:56`) builds `interp.New(interp.Options{})`
   with no `Env` field, and yaegi shims `os.Getenv` to an empty internal map, so
   no host env var ever reaches interpreted plugin code. This independently
   forced a mechanism change.

**Amended decision: read the whitelist from Vikunja's native config system via
`viper.GetString("customfields.whitelist")`.** This honors the story's original
"config file" intent, uses the mechanism the
[Plugin Development](https://vikunja.io/docs/plugin-development/#plugin-configuration)
docs document, and is overridable by the `VIKUNJA_CUSTOMFIELDS_WHITELIST` env var
(Vikunja's viper sets prefix `vikunja` + `.`→`_` replacer + `AutomaticEnv`). The
env-var override also gives the test harness its malformed-entry probe (AC#4)
without editing the config file.

Everything else — the `IsManager` seam, deny-by-default semantics, the
parsing/malformed-entry behavior, the temporary verification route, the scope
boundary, the ACs — is unchanged. Only the **source** of the whitelist changes
(`os.Getenv` → `viper.GetString`), a one-function edit isolated in `loadWhitelist`.

## Summary

S8 declares who may manage custom fields. A whitelist of usernames, read by the
plugin at startup from Vikunja's config, defines the users permitted to create,
edit, and delete field definitions. It is exposed to the field-definition API
(S2) and the management UI (S9) as a single predicate. Users not on the whitelist
are denied management operations — enforced by S2/S9, not by S8.

This is the license-free governance surface the PRD calls for: it lives in
configuration the admin edits, never touches Vikunja's licensed admin feature,
and is consumed by both the API and the temporary UI.

## Context — why the whitelist is read via Vikunja's config (viper)

S8's story and the PRD both say the whitelist lives "in Vikunja's config file."
That is the intent this spec honors, now achievable directly. The plugin reads
`customfields.whitelist` through the same global `viper` instance Vikunja's own
`pkg/config` uses (see verified facts), so a plugin `viper.GetString(...)` call
sees exactly what an admin puts in `config.yml` — and the
`VIKUNJA_CUSTOMFIELDS_WHITELIST` env var overrides it via viper's
`AutomaticEnv()`.

This is the documented plugin-configuration mechanism (Plugin Development →
Plugin Configuration), requires **no fork change** (the symbol-table exposure is
already upstream + backported), and keeps the accessor isolated in one function
so the source remains a one-function swap.

### Forward compatibility

The config key `customfields.whitelist` is chosen to match the env-var override
Vikunja's viper derives from it: prefix `VIKUNJA_`, `.`→`_`
(`config.go:635-637`), so `customfields.whitelist` ↔ `VIKUNJA_CUSTOMFIELDS_WHITELIST`.
The config-source accessor is isolated in one function (`loadWhitelist`); if a
later story prefers the typed `config.Key("customfields.whitelist").GetString()`
wrapper, switching is a one-function edit. (Both resolve to the same global
viper call; `viper.GetString` is used here for directness.)

## Independently verified facts (live `vikunja/` fork source, `plugin-fix-backport` branch)

Checked against the current source, not inherited from the abandoned branches:

- **`pkg/config` and `viper` ARE now in the yaegi symbol table.** Upstream PR
  #3502 added `code.vikunja.io/api/pkg/config` (`vikunja_config.go`) and
  `github.com/spf13/viper` (`viper.go`) to `yaegiSymbolPackages` in `magefile.go`;
  both generated symbol files are present in `pkg/yaegi_symbols/`. PR #3549
  likewise added `xorm.io/xorm` and `src.techknowlogick.com/xormigrate`. All four
  are in the backport image. (The S1 spec's claim that these were absent was true
  at the time; it is now superseded.)
- **Vikunja uses the GLOBAL default viper instance.** `pkg/config/config.go`
  configures the package-global viper directly: `viper.SetEnvPrefix("vikunja")`,
  `viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))`, `viper.AutomaticEnv()`
  (lines 635-637); `viper.AddConfigPath(...)`, `viper.SetConfigName("config")`,
  `viper.ReadInConfig()` (lines 642-655). There is no private `viper.New()`.
  Therefore a plugin's `viper.GetString("customfields.whitelist")` reads the same
  loaded config `pkg/config` reads, and the env var `VIKUNJA_CUSTOMFIELDS_WHITELIST`
  overrides it.
- **`config.Key(k).GetString()` delegates to the global viper**
  (`config.go:250-251`: `return viper.GetString(string(k))`). Either accessor
  works; this spec uses `viper.GetString` directly.
- **`user.GetCurrentUser(c *echo.Context) (*user.User, error)` IS available** to
  plugins (`vikunja_user.go:65`). It resolves the authenticated caller from the
  JWT in the echo context. **Verified at runtime in the first execution pass**:
  the `/manager` route returned 200 with correct usernames and no 401s for both
  seeded users — the echo context carries the JWT in the shape `GetCurrentUser`
  expects. (This was the spec's former "one unverified assumption"; it is now
  confirmed.)
- **`user.User` has a `Username string` field** (`xorm:"varchar(250) not null
  unique"`, `user.go:93`). This is the identifier the whitelist matches against.
- **Username comparison is case-sensitive at the DB layer.** Vikunja's
  `getUser` does a direct `username = ?` (`user.go:354`) with no forced
  lowercasing on read; case sensitivity then depends on the DB collation (SQLite
  ASCII-case-insensitive, Postgres case-sensitive). The whitelist comparison is
  therefore **normalized to lowercase on both sides**, so behavior is consistent
  and admin-friendly regardless of database.
- **Viper maps `customfields.whitelist` → `VIKUNJA_CUSTOMFIELDS_WHITELIST`.**
  `config.go:635-637`: `SetEnvPrefix("vikunja")`, `SetEnvKeyReplacer(".", "_")`,
  `AutomaticEnv()`. This is the basis for the env-override probe in AC#4.

### The residual runtime check

Static evidence says `viper.GetString("customfields.whitelist")` resolves inside
a yaegi plugin for a custom (non-core) key: the global viper is loaded with the
config file, the symbol table exposes `viper`, and the PR #3502 author verified a
`viper.GetString(...)` call resolves in a plugin. The runtime confirmation for
*our specific key* is Task 5's integration test. If `viper.GetString` returns `""`
unexpectedly, the place to look is whether the config-file key is spelled
`customfields.whitelist` and whether the env override is interfering.

## Architecture

S8 adds three things to the existing single `main.go` (package `main`):

1. **A whitelist set**, populated in `Init()` from the config key, held in a
   package-level variable.
2. **`IsManager(username string) bool`** — the sole seam S2 and S9 call.
3. **A temporary verification route** proving the predicate resolves correctly
   for the authenticated caller, removed once S2 exercises the predicate on real
   endpoints.

No core Vikunja files are modified. The fork is untouched (the symbol-table
exposure is upstream + backport, not a per-feature fork change). Imports added to
`main.go`: `strings` (stdlib), `github.com/spf13/viper`, and
`code.vikunja.io/api/pkg/user` — all verified available to yaegi. (`os` is no
longer needed — it was only for the abandoned `os.Getenv` path.)

## Config schema

- **Config key: `customfields.whitelist`** — a comma-separated string of
  usernames, e.g. `alice,bob,carol`, placed under a `customfields:` section in
  `config.yml`:
  ```yaml
  customfields:
    whitelist: "alice,bob,carol"
  ```
- **Env override: `VIKUNJA_CUSTOMFIELDS_WHITELIST`** — same comma-separated
  format; overrides the config-file value (viper `AutomaticEnv`). This is the
  mechanism AC#4 uses to inject a malformed value without editing the config
  file.
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
// loadWhitelist reads the management whitelist from Vikunja's config
// (the customfields.whitelist key, overridable by the VIKUNJA_CUSTOMFIELDS_WHITELIST
// env var) and returns a lowercase-normalized set of permitted usernames. Source
// is isolated here so a future swap to config.Key(...) is a one-function change.
//
// Malformed entries (empty after trimming, e.g. "alice,,bob") are logged and
// skipped — never fatal. An absent/empty value yields an empty set (deny-all).
func loadWhitelist() map[string]struct{} {
	set := map[string]struct{}{}
	raw := viper.GetString("customfields.whitelist")
	if raw == "" {
		log.Infof("[custom-fields] whitelist empty — no users may manage custom fields")
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
	log.Infof("[custom-fields] whitelist loaded: %d manager(s)", len(set))
	return set
}
```

- A **malformed entry** is one that is empty after trimming (e.g. `alice,,bob` —
  the middle entry, or a trailing comma `alice,bob,`). It logs a clear error
  naming the position and is skipped. **Vikunja does not crash** (AC#4): `Init`
  continues and returns nil; the malformed entry simply is not in the set.
- Empty/absent value logs at info: `[custom-fields] whitelist empty — no users may
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
// whitelist denies everyone. Comparison is case-insensitive.
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
- 401 if the user cannot be resolved from context.
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

The whitelist is declared in `config.test.yml` under a `customfields:` section
(e.g. `whitelist: "testuser"`), so the test harness exercises a populated
whitelist read from the config file. The test users are seeded by
`run-test-env.sh`.

### Acceptance-criteria verification

| AC | How verified |
|---|---|
| 1. Whitelist can be declared | Set `customfields.whitelist` in `config.test.yml`, restart — startup log shows the loaded-count line. |
| 2. Whitelisted user allowed | `curl /api/v1/plugins/custom-fields/manager` with the whitelisted test user's JWT → `{ "is_manager": true }`. |
| 3. Non-whitelisted user denied | Seed/log in a second user not on the whitelist; same route → `{ "is_manager": false }`. (Enforcement on field-definition ops is S2/S9 — out of S8's scope.) |
| 4. Malformed entry → error, no crash | Set env override `VIKUNJA_CUSTOMFIELDS_WHITELIST=testuser,,otheruser`, restart — startup log shows the empty-entry error; Vikunja stays healthy; `testuser` and `otheruser` both resolve as managers. Revert the env override afterward. |
| 5. Absent → empty, no crash | Remove the `customfields:` section (and any env override), restart — empty-whitelist info line; route returns `is_manager: false` for everyone; Vikunja healthy. |
| 6. Shared with S2 and S9 | Proven when S2 imports and calls `IsManager` (S2's verification, not S8's). |

AC#3 requires a second, non-whitelisted test user. `run-test-env.sh` currently
seeds one regular test user; a second user is added as part of S8's test-harness
update (via the existing `/api/v2/test/users` seed endpoint) so the deny path can
be demonstrated. This is a harness change, not a plugin-source change.

AC#4 uses the env override rather than editing the config file: viper's
`AutomaticEnv` means `VIKUNJA_CUSTOMFIELDS_WHITELIST` overrides
`customfields.whitelist`, so a malformed value can be injected via the compose
`environment:` block (or shell env) for the probe and reverted without touching
the committed config file.

## Doc updates (consistency with the config mechanism)

The stories and PRD originally worded the whitelist as living in "Vikunja's config
file." That wording is now correct again (the whitelist IS read from the config
file via viper). Doc updates are a light touch: ensure the mechanism is named
consistently as Vikunja config under `customfields.whitelist` (overridable by the
`VIKUNJA_CUSTOMFIELDS_WHITELIST` env var), and remove any stray "environment
variable" wording the first (abandoned) execution pass may have introduced into
the spec/plan (the spec/plan are amended directly; the stories are checked in
Task 6). The whitelist *concept* and *name* ("Config Whitelist") are unchanged.

## Git workflow

Per `CLAUDE.local.md`: git-flow is mandatory, Conventional Commits. S8's spec
and implementation plan are committed on `develop` (matching S1's flow). The
actual S8 code lands on a `feature/s8-config-whitelist` branch off `develop`.

A worktree decision is raised before any development work, per `CLAUDE.local.md`.
The implementation plan (next step) details the branch/commit structure and
includes Required/Recommended skills lists per `CLAUDE.local.md`.

The first execution pass's stale commits (built on `os.Getenv` against the
pre-xormigrate base) are superseded: the feature branch is reset onto current
`develop` (which has the xormigrate refactor + the backport image) and the S8
work is re-applied with the viper mechanism. The old tip is preserved as a backup
ref for recovery.

## Out of scope

- Enforcing the whitelist on field-definition CRUD — S2.
- The management UI that consumes the whitelist — S9.
- Wiring the whitelist into field-value operations — values are per-task, not
  management; out of scope for S8 entirely.
- Hot-reloading the whitelist without restart.
- A persistent (non-temporary) management-status endpoint.