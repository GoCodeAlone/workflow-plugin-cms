# workflow-plugin-cms

The editor accepts harmless HTML normalization (entities, attribute quoting and
empty data attributes) without forcing source mode. Its original source remains
authoritative until a visual edit. Removed or advanced markup stays in the
source editor; the visual DOM always uses the pinned local sanitizer.

The opt-in native editor browser regression covers both modes, source
preservation, unsafe payloads, and save/reload through the actual CMS API:

```sh
CMS_NORMALIZATION_BROWSER=1 CMS_PLAYWRIGHT_MODULE=/path/to/playwright/package.json \
  go test ./host -run '^TestEditorNormalizationBrowserScenario$' -count=1 -v
```

The module path must resolve an installed Playwright package; optionally set
`CMS_BROWSER_CHANNEL=msedge`. This loopback fixture uses isolated memory stores
and a test cookie auth hook. It does not exercise production authorization or
PostgreSQL restart durability.

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

## Navigation, Widgets, And Media

Navigation items can target static routes, CMS pages, overlays, or external
HTTP(S) URLs. Published navigation excludes draft, archived, and future
scheduled items.

Widget instances render only from an explicit allowlist. Raw script tags,
inline event handlers, and `javascript:` URLs are rejected so widget behavior
stays bounded by reviewed widget types.

Published migrated content must reference relative media paths or object-store
URLs owned by the site. Wix/parastorage/source-host media URLs are rejected
until mirrored into site-owned storage.

## Admin integration

The `cms.engine` module exposes the strict service method
`CMSEngine.AdminContribution`. Hosts such as `gocodealone-multisite` can call it
to register the site editor inside the extensible admin shell. Site content and
platform administration use separate documents and mounts:

```go
editor := adminui.HandlerWithOptions(adminui.Options{
    Mode: adminui.Editor, BasePath: "/admin/cms/sites",
})
platform := adminui.HandlerWithOptions(adminui.Options{
    Mode: adminui.Platform, BasePath: "/admin/cms/platform",
})
```

`adminui.Handler()` defaults to the editor at `/admin`. Protect every editor
request with the host's current identity and tenant membership, and every
platform request with current platform authority. The handlers only render UI;
they do not replace authorization. `host.Config.AdminPlatformAccess` is required
for tenant mutations, domain reads/writes and global cache reload. Those API
operations fail closed when the callback is absent, including when `AdminAPI`
is called directly. `AdminAuth` and `AdminTenantAccess` remain required for the
private page permission projection at
`GET /api/v1/admin/tenants/:tid/pages/permissions[?page_id=:pid]`. This endpoint
probes the configured callbacks without dispatching writes and returns create,
edit and delete capabilities for the specific tenant/page. UI visibility follows
that projection; write handlers still enforce authorization independently.

Each page has Content, Appearance, Publishing, HTML and Preview routes. Content
routes select one top-level body section; plain top-level text uses a whole-body
fallback. Section switching preserves the canonical body. The editor warns
before leaving an unsaved page and freezes the captured draft during Save.
Platform tenant/domain/cache controls are absent from the editor document.

The contribution requires the multisite admin scopes:

- `admin:multisite.sites:read`
- `admin:multisite.pages:read`
- `admin:multisite.pages:update`
- `admin:multisite.publish:update`

Platform contributions belong to the host and require its platform-specific
permission plus current platform authority. The CMS content contribution no
longer advertises tenant creation, domain controls or onboarding operations.

API route metadata is returned only when the caller passes `authorized: true`.
That keeps route discovery behind the host's authz check while preserving the
strict protobuf contract declared in `plugin.contracts.json`.

### Scoped access to private preview content

Hosts may optionally set `hostpolicy.Config.PreviewAccess` to a
`func(*http.Request, hostpolicy.Tenant) bool` that verifies scoped preview
authority on every request. The host must verify its credential's audience,
exact origin, tenant, exact content/asset path, and current grant. The plugin
does not issue credentials or cache callback approvals.

The callback is considered only after the existing HTTPS, current canonical
domain ownership, and valid tenant password-hash checks, for tenants resolved
with kind `preview`. Its ceiling allows only GET/HEAD with canonical unencoded
content paths and no query or request body. API, admin, auth, internal, control,
health, metrics, hidden paths and path aliases are excluded. It receives a
request clone; changes to it cannot change the downstream target. An approval
uses the same credential-stripping and private/no-store/noindex response path
as human Basic access, and grants no CMS or platform authority. Basic requests
always retain password verification. A nil callback or a denial retains the
existing Basic challenge. Compose host policy outside every content wrapper;
this optional callback does not replace the host's editor or API gates.

## Persistence and backup

CMS page documents are durable application state. Operators backing this plugin
with Postgres must include the `pages` table in backup and restore runs,
including `body_blocks`, `template_id`, `publish_at`, and `unpublish_at`.
`body_blocks` is the canonical editor document when present; `body_html` remains
the backward-compatible fallback for older pages.

Saved write history and optimistic save/delete preconditions are documented in
[Saved page history](docs/saved-page-history.md). Adoption requires both explicit
plugin SQL prerequisites and matched host schema/authorization support. It does
not reconstruct earlier edits or unsaved browser drafts.

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
