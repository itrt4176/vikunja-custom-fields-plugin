# S5 — Throwaway `mage test:e2e` Wiring (plugin ↔ Playwright)

The vikunja repo's `mage test:e2e` builds the API from the vikunja fork and runs
the Playwright suite in `frontend/tests/e2e/`. The plugin lives in a sibling repo
(`../vikunja-custom-fields-plugin`) and is **not** loaded by the e2e API instance
by default. To exercise the custom-fields UI via Playwright, the e2e API instance
must load the plugin — point the e2e harness at the sibling plugin the same way
`compose.test.override.yml` points at `../vikunja`:

1. The e2e API build (mage) must include the plugin source mounted at
   `/app/vikunja/plugins/custom-fields` (mirror `compose.test.yml:7`) and the
   yaegi loader enabled in the e2e config (mirror `config.test.yml`).
2. Seed a custom field definition + a task in the e2e setup (mirror
   `scripts/run-test-env.sh` Step 5b/5c) so a Playwright spec can open the task
   and assert the custom-fields section renders.

This wiring is **throwaway** — it depends on the local relative-path layout
(`../vikunja-custom-fields-plugin`). A robust, portable e2e (self-contained
image / CI-grade harness that doesn't require recreating the exact local setup)
is **out of scope for S5** and deferred to a future project. This note records the
wiring so it can be reused until the robust solution lands.

## Concrete recipe (no magefile changes needed)

`Test.E2E` in the fork's `magefile.go` starts the e2e API with
`apiCmd.Env = append(os.Environ(), ...)`, so any `VIKUNJA_*` variable exported in
the shell propagates into the e2e API process — the plugin can be enabled purely
via environment, no `magefile.go` or e2e-config edit required:

```bash
# The plugin manager yaegi-loads every *subdirectory* of plugins.dir
# (pkg/plugins/manager.go, loader "yaegi" branch), so plugins.dir must be a
# directory whose only child is the plugin — do NOT point it at the workspace
# root, or yaegi will try to interpret the whole vikunja fork tree.
mkdir -p /tmp/e2e-plugins
cp -r ../vikunja-custom-fields-plugin /tmp/e2e-plugins/custom-fields

export VIKUNJA_PLUGINS_ENABLED=true
export VIKUNJA_PLUGINS_LOADER=yaegi
export VIKUNJA_PLUGINS_DIR=/tmp/e2e-plugins
# config.test.yml sets customfields.whitelist for the docker test instance. The
# whitelist is exact comma-separated usernames, deny-all when empty, no wildcard —
# and e2e factory usernames are random (faker), so the spec must create its
# definition-manager with a FIXED username via `UserFactory.create({username: ...})`
# (password TEST_PASSWORD = '1234') and that name goes here:
export VIKUNJA_CUSTOMFIELDS_WHITELIST=customfields-manager

mage test:e2e ""
```

The plugin's routes register under `/api/v1/plugins/custom-fields/...` once the
loader picks it up. Definition seeding needs a whitelisted manager (only
create/update/delete are whitelist-gated — definition read and value
read/write follow task permissions). The Playwright suite gets the testing
token via `TEST_SECRET` / `VIKUNJA_SERVICE_TESTINGTOKEN` and has
`authenticatedPage` / `ProjectFactory` / `TaskFactory` fixtures in
`frontend/tests/support/` and `frontend/tests/factories/`.

## What a Playwright spec would assert

- Open a task in a project with assigned custom fields → the custom-fields
  section appears (`[data-cy]` or the `.custom-fields` class).
- Each field type renders the right input.
- Editing a value persists (reload the task; the value remains).
- Clearing a value removes it.
- An API-only field is disabled.
- A project with no custom fields shows no section.

A committed spec must **not** assume the plugin is loaded: `mage test:e2e`
without the exports above serves no custom-fields API, so the spec would fail
for everyone else. Gate it (e.g. `test.skip(!process.env.VIKUNJA_PLUGINS_ENABLED, ...)`)
until the robust portable e2e lands.
