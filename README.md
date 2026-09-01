# Custom Fields for Vikunja

A work-in-progress [Vikunja](https://vikunja.io) plugin to add **custom fields**
to tasks — text, numbers, dates, selects, checkboxes, URLs — defined by a
whitelisted user and assigned to projects.

> [!WARNING]
> **Status: early / not usable yet.** The backend API is built and works against
> stock Vikunja, but the feature isn't user-facing until the frontend rendering
> and management UI land. See *Status* below.

## Two repos

The feature is split across two repos:

- **This repo** — the backend plugin (`main.go`), a yaegi plugin that owns its
  own DB tables for field definitions and values and exposes a REST API. No
  changes to Vikunja core; runs against stock Vikunja.
- **The [Vikunja fork](https://github.com/itrt4176/vikunja)** — the frontend
  half. Custom fields rendered on the task detail view (S5) is patches to the
  Vue frontend there, which Vikunja embeds into its binary at compile time
  (`//go:embed all:dist`). So the backend plugin is a live-mount, no-recompile
  affair, but the visible half is a fork-binary rebuild — and it's not built
  yet.

## Status

The backend API is built; what makes the feature real to a user — the frontend
rendering (S5) and management UI (S9) — is still pending, along with build/deploy
(S7). Story status and dependencies live in
[`docs/stories/story-dependency-graph.md`](docs/stories/story-dependency-graph.md)
(the Critical Path checklist tracks what's done).

## Trying it locally

A Docker test instance is included. It runs stock Vikunja 2.6 with the plugin
source mounted live, seeds a test user, and prints a JWT.

```bash
./scripts/run-test-env.sh                       # start, seed, print a JWT
docker compose -f compose.test.yml restart      # apply edits to main.go
docker compose -f compose.test.yml down         # stop
```

Then call the API (routes live under `/api/v1/plugins/custom-fields`):

```bash
curl -s -H "Authorization: Bearer $JWT" \
  http://127.0.0.1:4176/api/v1/plugins/custom-fields/definitions | jq .
```

Port, image tag, and the testing token are defined in `compose.test.yml` and
`config.test.yml`.

## Development notes

The one non-obvious thing: Vikunja's `web` package isn't in yaegi's symbol
table, so the plugin uses `*user.User` instead of `web.Auth` and plugin-local
9000s error codes instead of `web.HTTPError` — deliberate seams for a future
upstream move, not bugs. See `CLAUDE.md` for the full architecture note, and
`vikunja/pkg/yaegi_symbols/` to check what's available to the plugin.

## Documentation

- `docs/PRD.md` — requirements, motivation, and design principles.
- `docs/stories/` — the S1–S9 story breakdown and dependency graph.
- `docs/superpowers/specs/` — per-story design specs (API detail, data models).

## License

AGPL-3.0, same as Vikunja. See `LICENSE`.