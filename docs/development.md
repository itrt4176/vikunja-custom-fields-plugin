# Development guide

Everything needed to work on the plugin itself: the repo layout, the local
test instance, the architectural constraints that make the code look the way
it does, and the release process.

## Repo layout

```
main.go              the entire plugin — models, permissions, CRUD, HTTP handlers,
                     migrations, event listener, UI routes
ui/                  the management UI: index.html + app.js, vanilla JS, no build
scripts/             test-instance helpers (run-test-env.sh)
compose.test.yml     test instance: stock Vikunja image, plugin mounted live
config.test.yml      test config: yaegi loader enabled, whitelist, testing token
db/                  test instance SQLite database (bind mount, git-ignored)
docs/
├── PRD.md           product requirements, motivation, design principles
├── installation.md  admin-facing setup guide
├── api-reference.md the REST API reference
├── stories/         the S1–S9 story breakdown, dependency graph, progress
└── superpowers/     per-story design specs and implementation plans
.github/workflows/   release.yml — packages the plugin zip on v* tags
```

There is **no build step**: the plugin is interpreted at runtime by Vikunja's
yaegi loader from the mounted source. A Go toolchain is not needed to develop
it — only to work on the fork itself.

## Test instance

A Docker-based Vikunja instance with the plugin mounted live. From the repo
root:

```bash
./scripts/run-test-env.sh                       # start, wait healthy, seed a user, print a JWT
docker compose -f compose.test.yml restart      # apply edits to main.go / ui/
docker compose -f compose.test.yml down         # stop (DB persists in ./db, wiped on next start)
```

- The container runs the **fork's image** with the repo bind-mounted at
  `/app/vikunja/plugins/custom-fields` — no recompile, restart to reload.
- Confirm loads (or errors) with
  `docker compose -f compose.test.yml logs | grep -i "loaded plugin"`.
- The SQLite database is directly readable on the host: `sqlite3 db/vikunja.db`.
- `config.test.yml` enables the `service.testingtoken` seed endpoint for
  fixture data (`PUT /api/v2/test/{table}` with the raw token from that file
  — authorization is the raw token string, not a JWT).
- Port (4176), image tag, and secrets are defined in `compose.test.yml` and
  `config.test.yml` — those files are the authority.

`compose.test.override.yml` builds the fork image locally instead of pulling,
for frontend work that runs alongside plugin work.

## Architecture

The plugin is a single `package main` file interpreted by yaegi. The moving
parts:

- **Data model** — five xorm tables created by one migration
  (`20260829160000-create-custom-field-tables`): `custom_field_definitions`,
  `custom_field_values`, `custom_field_options`,
  `custom_field_value_options`, `custom_field_projects`. Vikunja runs plugin
  migrations automatically on startup, after core migrations and before
  `Init()`. The migration is modified in place while the feature is
  unreleased (the `project_views` precedent); once in production, schema
  changes become append-only migrations.
- **Permissions** — `CanCreate/CanRead/CanUpdate/CanDelete` on definitions
  (whitelist-gated via `IsManager`) and on values (delegating to the host's
  `models.Task.CanRead/CanUpdate`). Deny-by-default: an empty whitelist
  denies everyone.
- **Validation** — pure functions (`validateDefinition`, `validateValue`,
  `validateAssignment`) with no DB access; option IDs are resolved
  separately by `resolveOptionIDs`.
- **CRUD** — model methods take an `*xorm.Session` and never open or commit
  it; handlers own the session (`db.NewSession()`, `defer s.Close()`, explicit
  `Commit()`), which is what makes the bulk value write atomic.
- **Handlers** — thin: parse → permission check → model call → commit → JSON.
  Every write re-reads the resource afterwards for a canonical response.
- **Events** — a `task.deleted` listener cascades a task's custom field
  values (the host dispatches asynchronously after its own commit, so the
  listener opens its own session).
- **Impact previews** — `computeImpact` implements the diff semantics (type
  change → all values; removed options → values using them; tightened numeric
  range → out-of-range values) used by both the management UI and the API.

### Yaegi constraints (why the code looks like this)

The plugin runs inside Vikunja's interpreter, and several host conveniences
are unavailable to it. These are **deliberate seams for a future upstream
conversion, not bugs**:

- **The `web` package is not in the symbol table** (`web.Auth`,
  `web.HTTPError`). Consequences:
  - Permission methods take `*user.User` instead of `web.Auth` — yaegi
    accepts it where the host expects a `web.Auth` at runtime.
  - Errors use plugin-local 9000s codes translated to
    `echo.NewHTTPError(code, message)`.
  - `toHTTPError` discriminates error kinds by **message prefix** — yaegi
    wraps interpreted errors so `switch err.(type)` never matches, and a
    multi-expression `case a, b, c:` clause evaluates only its first
    expression, so each prefix gets its own case clause.
- **Interpreted structs serialize as `{}` through `c.JSON`** — responses are
  built as `map[string]interface{}` field by field (`definitionToMap`,
  `fieldConfigMap`), never by echoing a struct. xorm DB reads/writes of
  interpreted structs work fine.
- **Table names must be passed explicitly** — `TableName()` methods are
  invisible to xorm on interpreted structs, so migrations sync with
  `tx.Table("name").Sync2(&T{})` (upstream PR #3549).
- **Sliceargs in `IN (?)`** — under yaegi a slice bind parameter passes
  through as one argument and the driver rejects it, so placeholder lists are
  expanded manually (`countValuesUsingRemovedOptions`).
- **Typed factory functions** — yaegi wraps return values per declared type,
  so the loader requires `NewAuthenticatedRouterPlugin`,
  `NewUnauthenticatedRouterPlugin`, and `NewMigrationPlugin` as separate
  entry points alongside `NewPlugin`.

The host symbol table lives at `vikunja/pkg/yaegi_symbols/` in the fork repo
— check it before assuming any Vikunja symbol is unavailable to the plugin.

### Upstream conversion checklist

If the feature is proposed upstream:

1. Restore `web.Auth` in permission signatures.
2. Replace `echo.NewHTTPError` translation with the host's
   `HTTPError()`/`ErrCode` convention (`pkg/models/error.go`;
   https://vikunja.io/docs/custom-errors/), which re-enables `switch err.(type)`.
3. Revert to xorm's native slice expansion for `IN (?)`.
4. Fold the frontend patches into the frontend repo.

## Release process

The plugin follows **git flow** with `develop` as trunk; releases and hotfixes
are finished with `git flow release finish` / `git flow hotfix finish`, which
cut `v`-prefixed, GPG-signed tags on `main` (per this repo's gitflow config).

1. Finish the release — the tag push triggers
   `.github/workflows/release.yml`.
2. The workflow packages `README.md`, `LICENSE`, `main.go`, and `ui/` via
   `git archive` under a fixed top-level `custom-fields/` folder into
   `custom-fields-<tag>.zip`, and attaches it to a **draft** GitHub release
   with generated notes.
3. Edit the draft before publishing: state the tested fork version pairing
   (the fork and plugin are versioned independently; only the current pairing
   is supported) and anything an upgrading admin must know. Then publish —
   the zip installs with a single unzip into the plugins directory.
