---
title: "Build, Deploy & Document"
description: "The plugin and forked frontend ship as a single Docker image with setup docs for other admins."
status: pending
priority: 50
labels: ["infrastructure", "documentation"]
position: 7
---

# Build, Deploy & Document

## Outcome

The custom fields feature — backend plugin and frontend changes together — ships as a single, buildable Docker image from the fork. Another Vikunja admin can clone the repo, build the image, mount the plugin directory, and have custom fields running on their instance by following the published documentation.

## What & Why

All the code from S1–S3, S5, S8, S9 only matters if it can be deployed. This story wires everything together: the Docker build produces one image containing the forked Vikunja binary (with the modified frontend embedded), and the plugin directory is mounted at runtime. A README and setup guide walk other admins through installation, configuration, and usage.

This story also validates the end-to-end experience: build, deploy, configure, use. Any integration issues discovered here get fixed or fed back to earlier stories.

## Design Principles

- **Works without a license** — the documentation covers management via the plugin-served UI and the config whitelist, with no licensed feature involved.
- **Plugin as proving ground, not permanent home** — the deployment model works today with the plugin system, but the documentation notes that the feature may be proposed upstream, at which point deployment simplifies to a stock Vikunja binary.

## Dependencies

- **Must come after:** all other stories (S1–S3, S5, S8, S9)
- **Must come before:** none
- **Can run in parallel with:** none

## Acceptance Criteria

1. ✅ Running the existing Dockerfile from the fork produces a single image containing both the modified frontend and the stock Vikunja API.
2. ✅ Mounting the plugin source directory into the container at the configured path loads the plugin on startup.
3. ✅ The plugin loads successfully in a running container and custom fields are functional end-to-end (whitelist → definition → value → task detail display).
4. ✅ A README exists in the plugin repo with clear setup instructions covering: enabling plugins in config, mounting the plugin directory, configuring the management whitelist, and managing fields via the plugin-served UI.
5. ✅ The documentation notes that native management is a future epic, but the current setup needs no license.
6. A Vikunja admin unfamiliar with the project can follow the documentation and get custom fields running without external help.

(1–3 verified 2026-09-06; 4–5 delivered 2026-09-06 — see Progress. 6 awaits a stranger-admin walkthrough.)

## Scope

**In scope:**
- Verification that the existing Dockerfile builds a working image from the fork
- Plugin mount configuration and validation
- End-to-end smoke testing (whitelist → definition → value → display)
- Plugin README with setup and usage documentation
- Whitelist and management-UI setup paths in documentation
- Release automation for both repos and publishing (added by scope revision, 2026-09-06)

**Out of scope:**
- Creating new Docker images or build processes (the existing Dockerfile is sufficient)
- Publishing the plugin to a package registry or marketplace (GitHub release zips are the distribution)
- Automated integration tests in CI

### Scope revision (2026-09-06)

The original scope excluded CI/CD pipeline setup, publishing the Docker image
to a registry, and any plugin distribution. Nick overruled those exclusions;
the work was then built and is now in scope:

- **CI/CD** — built for both repos (see Progress). The fork gets a
  docker-only release pipeline on free GitHub-hosted runners; the plugin gets
  a tag-triggered release-zip workflow.
- **Registry publishing** — the fork publishes to GHCR only
  (`ghcr.io/itrt4176/vikunja`, no Docker Hub, no S3), using the fork's
  `v<upstream>.<fork release>` tag scheme (`v2.6.0.1` → tags `2`, `2.6`,
  `2.6.0`, `2.6.0.1`, `latest`; `cf-main` pushes → `unstable`).
- **Plugin distribution** — GitHub release zips rather than a registry:
  `custom-fields-<tag>.zip` with a fixed `custom-fields/` top folder, so one
  `unzip` into the parent plugins dir serves both fresh install and in-place
  upgrade.

## Progress — 2026-09-06

### Done

- **Verification & smoke testing (AC 1–3): complete.** All verification steps
  and the end-to-end smoke test (whitelist → definition → value → task detail
  display) finished by Nick.
- **Fork release pipeline** (vikunja repo, branch `feature/gh-release-workflow`,
  unmerged). Single `docker` job on `ubuntu-latest`, GHCR only, no secrets
  required: `cf-main` pushes → `unstable`; annotated `v<upstream>.<rel>` tags →
  `2`, `2.6`, `2.6.0`, exact, `latest`. The S3/R2-dependent upstream jobs are
  commented out in `release.yml` with written revival steps. Survived an
  independent adversarial review, which caught a critical tag-rule regex defect
  (releases would have pushed only `:latest`, silently) — fixed and verified
  against real tag formats. Versioning convention and release process
  documented in the fork's `AGENTS.md`/`README.md`.
- **Plugin release workflow** (this repo, branch `feature/release-workflow`,
  unmerged). `v*` tag pushes package `README.md`, `LICENSE`, `main.go`, `ui/`
  via `git archive` under a fixed `custom-fields/` top folder into
  `custom-fields-<tag>.zip` and attach it to a draft GitHub release with
  generated notes. Draft so the tested fork+plugin pairing is written before
  publishing.
- **Documentation shipped (AC 4–5)** (this repo, branch
  `feature/release-workflow`, plus a fork-README change on the fork's
  `feature/gh-release-workflow`). Written from the shipped code, in the voice
  of a released product:
  - `README.md` — rewritten as project overview + quick start + development
    pointer. The stale "not usable yet" status and pending-feature text are
    gone; the versioning/pairing statement and the no-license /
    future-native-management note (AC 5) are here.
  - `docs/installation.md` — requirements (fork image, pairing, Web Awesome
    CDN), release-zip install with the fixed `custom-fields/` folder, config
    reference (the three `plugins.*` keys with their defaults — `enabled:
    false`, `loader: native`, both of which leave the plugin inactive — and
    `customfields.whitelist` semantics), Docker Compose example with the
    wholesale-mount warning, start/verify steps, a managing-fields section
    (impact previews, drafts, assignment rules), the upgrade procedure with
    its clean-up caveat, and a troubleshooting table.
  - `docs/api-reference.md` — the complete REST reference: auth model
    (user JWT only; **API tokens are rejected** — their route-permission table
    cannot cover plugin routes), the two authorization gates (whitelist for
    definitions, task permissions + assignment check for values), the error
    catalog with wire-accurate messages (internal 9000s codes are not
    exposed), field-type/value format tables, all 15 API routes with request/
    response schemas and examples, behavior notes (cascades, atomicity,
    read-as-null policy), and a worked curl example.
  - `docs/development.md` — repo layout, test instance, architecture, the
    yaegi constraints and upstream-conversion checklist, release process.
  - Fork `README.md` — a fork notice is now the first thing in the file (what
    the fork is, its purpose, pointer to this repo for the full docs), with
    the upstream content below kept intact.

### Outstanding

1. **Merge both feature branches** — `feature/release-workflow` (here) and
   `feature/gh-release-workflow` (fork) are written and reviewed but unmerged.
2. **First releases.** Plugin: `git flow release finish <version>` (tag prefix
   `v`, GPG-signed per repo config) → workflow posts the draft → write pairing
   notes → publish; initial version number still to be chosen. Fork: cut
   `v2.6.0.1`, but only after the documented metadata-action dry-run gate from
   the release process in `AGENTS.md`. First GHCR push creates the package
   private — flip it to public.
3. **AC 6 (stranger-admin walkthrough)** — an admin unfamiliar with the
   project follows the finished docs end-to-end without help. Now meaningful:
   the docs it tests exist.
4. **Management-UI offline fallback (discovered while writing the docs).** The
   S9 handoff note below describes a `ui/vendor` Web Awesome fallback; the
   shipped UI has none — `index.html` hardcodes the CDN URL and the plugin
   serves only `index.html` and `app.js`. If air-gapped instances are a real
   requirement, self-hosting Web Awesome is new work: a static-asset route
   plus a base-path override in the UI.

## From S9 (management UI) — document in build/deploy docs

- The management UI URL: `/api/v1/plugins/custom-fields/ui` (bookmarkable; served by the plugin's unauthenticated route group).
- The page loads Web Awesome from `https://ka-f.webawesome.com/webawesome@3.12.0/` — browsers need internet access to it. **Correction (2026-09-06, while writing the docs):** the shipped UI has no `ui/vendor` fallback — the CDN URL is hardcoded in `index.html` and the plugin serves only `index.html` and `app.js`. The fallback this note described was never implemented; see Outstanding #4 if offline-capable management UI becomes a requirement.
- The deployment mount must include the plugin directory **wholesale** (so `ui/` rides along) — a file-only mount breaks the UI.
