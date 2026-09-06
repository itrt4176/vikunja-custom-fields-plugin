# Installation and administration

This guide covers installing the custom-fields plugin on the
[custom-fields fork](https://github.com/itrt4176/vikunja) of Vikunja,
configuring it, upgrading it, and fixing common problems. For everything the
API can do, see [`api-reference.md`](api-reference.md).

## Requirements

- **The fork's Docker image** — `ghcr.io/itrt4176/vikunja`. The plugin itself
  runs on stock Vikunja's plugin loader, but the task detail view renders and
  edits custom fields only in the fork's frontend. On stock Vikunja the API
  and management UI work, but fields are invisible on tasks.
- **A matching pairing** — the plugin and the fork are versioned
  independently, and only the current pairing of the two is tested. Each fork
  release's notes state the plugin version it was tested with.
- **Internet access from admin browsers** to
  `https://ka-f.webawesome.com/webawesome@3.12.0/` — the management UI loads
  its component library (Web Awesome) from that CDN. Without it the page loads
  but renders unstyled and non-functional.

## Installing

### From a release zip (recommended)

Download the latest `custom-fields-<version>.zip` from the plugin's
[Releases](https://github.com/itrt4176/vikunja-custom-fields-plugin/releases)
page and unzip it into your plugins directory:

```bash
unzip custom-fields-v0.1.0.zip -d /srv/vikunja/plugins
```

The zip extracts to a single `custom-fields/` folder:

```
plugins/
└── custom-fields/
    ├── main.go          # the plugin, interpreted by Vikunja at startup
    ├── ui/              # the management UI (index.html + app.js)
    ├── README.md
    └── LICENSE
```

That directory is the install. `main.go` is read and evaluated by Vikunja's
[yaegi](https://github.com/traefik/yaegi) interpreter at every startup — there
is nothing to compile.

### From a source checkout (development)

Cloning this repo and pointing the mount at the checkout works identically —
the loader only cares that `<plugins.dir>/custom-fields/` contains `main.go`
(and `ui/` for the management UI). See
[`development.md`](development.md) for the live-reload workflow.

## Configuring

The plugin needs the host's plugin loader enabled plus its own whitelist
setting. Add to your Vikunja config (e.g. `/etc/vikunja/config.yml`):

```yaml
plugins:
  enabled: true
  dir: /app/vikunja/plugins      # the directory containing custom-fields/
  loader: yaegi

customfields:
  whitelist: "alice,bob"         # usernames allowed to manage custom fields
```

All four keys matter, and all four have defaults that leave the plugin
inactive:

| Key | Default | Meaning |
| --- | --- | --- |
| `plugins.enabled` | `false` | Master switch. Nothing loads while this is `false`. |
| `plugins.dir` | `<rootpath>/plugins` | The single directory scanned for plugin subfolders. Each subdirectory is one plugin. |
| `plugins.loader` | `native` | Must be `yaegi` for this plugin. `native` loads compiled `.so` files and is deprecated. |
| `customfields.whitelist` | *(empty)* | Comma-separated usernames permitted to manage custom fields. |

Any key can instead be set through environment variables using Vikunja's
`VIKUNJA_` convention (e.g. `VIKUNJA_PLUGINS_ENABLED=true`); the whitelist is
`VIKUNJA_CUSTOMFIELDS_WHITELIST`.

### The management whitelist

- Comparison is **case-insensitive**; entries are trimmed of whitespace.
- An **empty or absent whitelist denies everyone** — no user can manage
  fields until at least one name is configured.
- Entries that are empty after trimming (e.g. `"alice,,bob"`) are logged and
  skipped, never fatal.
- Whitelisted users can create, edit, and delete field *definitions*. They
  gain no access to other users' data: *values* on tasks follow the task's own
  read/write permissions, and the plugin adds no license-gated features.

The whitelist is read once at startup — restart Vikunja after changing it.

### Docker Compose example

```yaml
services:
  vikunja:
    image: ghcr.io/itrt4176/vikunja:2.6
    ports:
      - "3456:3456"
    volumes:
      - ./config.yml:/etc/vikunja/config.yml:ro
      - ./plugins:/app/vikunja/plugins
    environment:
      VIKUNJA_SERVICE_PUBLICURL: http://localhost:3456/
      VIKUNJA_DATABASE_TYPE: sqlite
      VIKUNJA_DATABASE_PATH: /db/vikunja.db
      VIKUNJA_FILES_BASEPATH: /db/files
```

**Mount the plugin directory wholesale.** The management UI is served from
`<plugins.dir>/custom-fields/ui/`, so the `custom-fields/` folder — not just
`main.go` — must exist inside the container. A file-only mount of `main.go`
loads the API but breaks the UI.

If the container cannot read the mounted files, `chmod a+r` the plugin's
`main.go` and `ui/*` (a common issue when the files were extracted by root).

## Starting and verifying

Restart Vikunja after installing and configuring. On startup Vikunja loads
plugins, runs the plugin's migration (which creates its five tables), and
initializes it. Verify:

1. **The log line:**

   ```
   docker compose logs vikunja | grep "Loaded plugin"
   # INF ... Loaded plugin custom-fields v0.1.0
   ```

2. **The health endpoint** (any logged-in user's JWT):

   ```bash
   curl -s -H "Authorization: Bearer $JWT" \
     http://localhost:3456/api/v1/plugins/custom-fields/health
   # {"name":"custom-fields","version":"0.1.0","status":"ok"}
   ```

3. **The management UI** at
   `http://localhost:3456/api/v1/plugins/custom-fields/ui`. It shows one of
   three states: the field list (you are whitelisted), a *Not authorized*
   notice (you are not), or a *Session expired* notice (no valid Vikunja
   session in that browser — open the main Vikunja app, log in, and return).

## Managing fields

Everything is managed from the plugin-served UI
(`/api/v1/plugins/custom-fields/ui`); every action is also available over the
REST API ([`api-reference.md`](api-reference.md)).

- **Create** — *New field*, then name, type, optional description, and display
  order. The form adapts to the type: integer/decimal fields offer a
  min/max range; select/multiselect fields offer an option list where row
  order is the display order.
- **Assign** — a field is either *global* (all projects) or assigned to
  specific projects. Assignment validates that a project ID exists, nothing
  more — you can target projects your own account cannot see.
- **Constraints** — `Required` rejects empty values on save. `API-only`
  renders the field as display-only on the task detail view; the API itself
  still accepts writes. `Default` is stored as metadata.
- **Edit** — the form re-reads the definition and submits the full replacement
  (PUT). Before saving an edit, the UI asks the server to preview how many
  stored values the change would invalidate — a type change invalidates all of
  them, removing a select option invalidates values using it, and tightening a
  numeric range invalidates out-of-range values — and asks for confirmation
  when that count is non-zero.
- **Delete** — the UI previews the cascade (a deleted field destroys all its
  stored values) and requires confirmation. Deletion is permanent and
  immediate.
- **Drafts** — while the form is open, every keystroke is saved to the
  browser's localStorage and restored if you navigate away and back.

Field values themselves are set on tasks, in the fork's task detail view,
exactly like Vikunja's native fields: fields with a value always appear;
valueless fields appear once activated from the sidebar's *Set \<field\>*
button, and disappear again when cleared.

## Upgrading

Because the zip always extracts to the same `custom-fields/` folder, upgrading
is the same command as installing — unzip the new release over the old
directory and restart. Two cautions:

- **Check the pairing first** — the new plugin release's notes state which
  fork version it was tested with. Upgrade the fork image if needed.
- **For a clean upgrade, remove the old directory before unzipping.** Files
  extracted over the top are replaced, but files *deleted* between releases
  would linger — and every `.go` file in the directory is evaluated at
  startup, so leftovers are not harmless.

Database tables migrate themselves on startup; no manual step is ever needed.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| No `Loaded plugin` line in the log | `plugins.enabled` is not `true`, `plugins.loader` is not `yaegi`, or `plugins.dir` does not contain the `custom-fields/` folder. All three keys are needed; see the table above. |
| Log shows `Failed to load yaegi plugin` | The error text names the problem — usually unreadable files (`chmod a+r main.go`) or a syntax error in edited source. |
| UI returns 404 `management UI assets not found` | The `ui/` folder is missing inside the container. Mount the `custom-fields/` directory wholesale, not a file-only bind of `main.go`. |
| UI loads but is unstyled and inert | The browser cannot reach `https://ka-f.webawesome.com/webawesome@3.12.0/`. The management UI loads its component library from that CDN. |
| UI shows *Session expired or missing* | The browser has no valid Vikunja session. Open the main Vikunja app in the same browser, log in, and return to the UI. |
| UI shows *Not authorized* / API returns 403 | Your username is not in `customfields.whitelist` (or it changed without a restart). Comparison is case-insensitive. |
| API returns 401 on every call | No valid user JWT. Plugin endpoints accept the same `Authorization: Bearer <jwt>` the web app uses; Vikunja API tokens are **not** accepted (their route permissions cannot cover plugin routes). |
| A field's value cannot be set on a task | The field is not assigned to that task's project — assignment is checked on every write. Global fields apply everywhere. |
| A stored value reads back as `null` | Values that no longer match their field — e.g. after a removed select option or a type change — read as `null` rather than erroring. The stored data is unaffected until the field is deleted. |
