# Custom Fields for Vikunja

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0--or--later-blue.svg)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/itrt4176/vikunja-custom-fields-plugin)](https://github.com/itrt4176/vikunja-custom-fields-plugin/releases)

A [Vikunja](https://vikunja.io) plugin that adds **custom fields** to tasks —
defined per project by a whitelisted manager, rendered and edited on the task
detail view by the [custom-fields fork](https://github.com/itrt4176/vikunja),
and fully scriptable over REST.

## What you get

- **Ten field types:** single-line text, multi-line text (rich text), integer,
  decimal, date, datetime, single select, multi-select, checkbox, and URL.
- **Per-project assignment** — a field applies to specific projects or to all
  projects, with a display order you control.
- **A management UI served by the plugin** — no extra tooling; access is gated
  by a config whitelist of usernames.
- **Native-feeling task integration** — the fork renders custom fields on the
  task detail view with the same styling, icons, and sidebar actions as
  Vikunja's own fields.
- **A complete REST API** for definitions and values — see
  [`docs/api-reference.md`](docs/api-reference.md).
- **No core changes** — the plugin owns its own database tables (created
  automatically on startup) and runs on Vikunja's plugin loader.

## How it works

The feature is split across two independently versioned repos:

- **This repo** — the backend: a single `main.go` plus a small `ui/` folder,
  interpreted at runtime by Vikunja's [yaegi](https://github.com/traefik/yaegi)
  plugin loader. It owns the database tables, the REST API under
  `/api/v1/plugins/custom-fields/`, and the management UI. Installing and
  upgrading requires no compilation — drop the directory in and restart.
- **The [custom-fields fork](https://github.com/itrt4176/vikunja)** — the
  frontend: a patched Vue app (custom fields on the task detail view) compiled
  into the fork's Docker image, published as
  [`ghcr.io/itrt4176/vikunja`](https://github.com/itrt4176/vikunja/pkgs/container/vikunja).

The two are separate software with separate version numbers. Only the current
pairing is tested and supported — each fork release's notes state the plugin
version it was tested with, so check both before upgrading.

## Quick start

Full detail — including config reference, Docker Compose examples, upgrade
procedure, and troubleshooting — lives in
[`docs/installation.md`](docs/installation.md). The short version:

1. Run the fork's image instead of stock Vikunja (`ghcr.io/itrt4176/vikunja:2.6`).
2. Download the latest release zip from
   [Releases](https://github.com/itrt4176/vikunja-custom-fields-plugin/releases)
   and unzip it into your plugins directory. It extracts to a `custom-fields/`
   folder, so unzipping directly into the parent plugins directory is all it
   takes — the same command installs fresh and upgrades in place.
3. Enable the yaegi loader in your Vikunja config:

   ```yaml
   plugins:
     enabled: true
     dir: /app/vikunja/plugins
     loader: yaegi

   customfields:
     whitelist: "alice,bob"   # usernames allowed to manage fields
   ```

4. Restart Vikunja. The plugin's tables are created automatically; look for
   `Loaded plugin custom-fields v0.1.0` in the startup log.
5. Open the management UI at
   `/api/v1/plugins/custom-fields/ui` (on your instance's URL), log in to
   Vikunja in the same browser, and create your first field.
6. Open a task in a project the field is assigned to, and set a value from the
   sidebar — it appears on the task detail view like any native field.

## Documentation

| Document | Contents |
| --- | --- |
| [`docs/installation.md`](docs/installation.md) | Install, configure, upgrade, and troubleshoot |
| [`docs/api-reference.md`](docs/api-reference.md) | Complete REST API reference |
| [`docs/development.md`](docs/development.md) | Development setup, architecture, and the test instance |

## Development

The plugin is a single interpreted Go file — there is no build step. Edit
`main.go`, restart the test container, and the change is live. A Docker test
instance with a seeded user and JWT is included:

```bash
./scripts/run-test-env.sh                       # start, seed, print a JWT
docker compose -f compose.test.yml restart      # apply edits to main.go
docker compose -f compose.test.yml down         # stop
```

See [`docs/development.md`](docs/development.md) for the full development
setup, the yaegi architecture constraints, and the release process.

## Status and roadmap

Management happens through the plugin-served UI and the REST API. Native field
management inside Vikunja's own settings is a future epic — proposing the
feature upstream would fold both halves into a stock Vikunja install. The
plugin uses no licensed Vikunja features and works on any instance.

## License

AGPL-3.0-or-later, same as Vikunja. See [`LICENSE`](LICENSE).
