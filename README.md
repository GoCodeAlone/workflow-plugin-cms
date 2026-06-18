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

## Static Page Overlays

CMS overlays can clone a static bundle page by recording the source path, source
hash, CSS selectors, and draft block document. Publishing requires the current
source hash to match the clone hash unless the caller has an explicit force
permission. A mismatch moves the overlay to `conflict_review` so updated static
content is reviewed before CMS changes override it.

Disabling an overlay never deletes or mutates the source static bundle; it only
marks the overlay inactive for render hooks.

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
