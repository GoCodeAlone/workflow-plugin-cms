# workflow-plugin-cms

> ⚠️ **Experimental** — This plugin compiles and passes its unit tests but has not been validated in any active GoCodeAlone-internal production deployment. Use with caution. Please [open an issue](https://github.com/GoCodeAlone/workflow-plugin-cms/issues/new) if you adopt it so we can promote it to **verified** status.

[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/GoCodeAlone/workflow-plugin-cms.svg)](https://pkg.go.dev/github.com/GoCodeAlone/workflow-plugin-cms)

Multi-tenant CMS engine for the [workflow engine](https://github.com/GoCodeAlone/workflow). Foundation plugin of [gocodealone-multisite](https://github.com/GoCodeAlone/gocodealone-multisite).

## What it provides

- **Tenant resolver** (`cms.tenant_resolver`) — Host header → tenant_id; unknown domain → 404 neutral.
- **Static-wins routing** (`cms.static_serve_before_dynamic`) — static files match before any CMS route is considered.
- **CMS engine** (`cms.engine`) — page CRUD, dynamic-section render, theme resolver, bundle fetcher, ingest webhook, upload handler.
- **Analytics injection** (`analytics.injection`) — per-tenant Google Analytics injection delegated to `workflow-plugin-analytics`.
- **Pipeline steps** — `step.cms_render_page`, `step.cms_bundle_activate`.

## Status

`v0.1.0` — first releasable CMS plugin build. Includes tenant resolution, static-before-dynamic serving, CMS page CRUD/rendering, bundle activation, analytics HTML injection helpers, audit-chain recording for admin writes, and strict plugin contracts.

## Admin integration

The `cms.engine` module exposes the strict service method
`CMSEngine.AdminContribution`. Hosts such as `gocodealone-multisite` can call it
to register the CMS site manager inside the extensible admin shell.

The contribution requires the multisite admin scopes:

- `admin:multisite.sites:read`
- `admin:multisite.sites:update`
- `admin:multisite.pages:read`
- `admin:multisite.pages:update`
- `admin:multisite.publish:update`
- `admin:multisite.onboarding:plan`

API route metadata is returned only when the caller passes `authorized: true`.
That keeps route discovery behind the host's authz check while preserving the
strict protobuf contract declared in `plugin.contracts.json`.

## Persistence and backup

CMS page documents are durable application state. Operators backing this plugin
with Postgres must include the `pages` table in backup and restore runs,
including `body_blocks`, `template_id`, `publish_at`, and `unpublish_at`.
`body_blocks` is the canonical editor document when present; `body_html` remains
the backward-compatible fallback for older pages.

## Install

```yaml
# wfctl.yaml
plugins:
  - name: workflow-plugin-cms
    version: v0.1.0
    source: github.com/GoCodeAlone/workflow-plugin-cms
```

```sh
wfctl plugin install
```

## Local development

```sh
git clone https://github.com/GoCodeAlone/workflow-plugin-cms.git
cd workflow-plugin-cms
GOWORK=off go build ./...
GOWORK=off go test ./...
```

## License

MIT. See [LICENSE](LICENSE).
