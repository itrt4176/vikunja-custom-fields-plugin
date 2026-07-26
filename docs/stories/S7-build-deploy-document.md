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

All the code from S1–S6 only matters if it can be deployed. This story wires everything together: the Docker build produces one image containing the forked Vikunja binary (with the modified frontend embedded), and the plugin directory is mounted at runtime. A README and setup guide walk other admins through installation, configuration, and usage.

This story also validates the end-to-end experience: build, deploy, configure, use. Any integration issues discovered here get fixed or fed back to earlier stories.

## Design Principles

- **Works without a license** — the documentation covers both config-file and admin-UI paths for defining fields.
- **Plugin as proving ground, not permanent home** — the deployment model works today with the plugin system, but the documentation notes that the feature may be proposed upstream, at which point deployment simplifies to a stock Vikunja binary.

## Dependencies

- **Must come after:** all other stories (S1–S6)
- **Must come before:** none
- **Can run in parallel with:** none

## Acceptance Criteria

1. Running the existing Dockerfile from the fork produces a single image containing both the modified frontend and the stock Vikunja API.
2. Mounting the plugin source directory into the container at the configured path loads the plugin on startup.
3. The plugin loads successfully in a running container and custom fields are functional end-to-end (config → definition → value → task detail display).
4. A README exists in the plugin repo with clear setup instructions covering: enabling plugins in config, mounting the plugin directory, and defining fields via config file or admin UI.
5. The documentation distinguishes between licensed and unlicensed setup paths.
6. A Vikunja admin unfamiliar with the project can follow the documentation and get custom fields running without external help.

## Scope

**In scope:**
- Verification that the existing Dockerfile builds a working image from the fork
- Plugin mount configuration and validation
- End-to-end smoke testing (definition → value → display)
- Plugin README with setup and usage documentation
- License-aware setup paths in documentation

**Out of scope:**
- CI/CD pipeline setup
- Publishing the Docker image to a registry
- Creating new Docker images or build processes (the existing Dockerfile is sufficient)
- Publishing the plugin to a package registry or marketplace
- Automated integration tests in CI