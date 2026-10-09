# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Content-only frozen snapshots, explicit target mappings and dry-run diffs with verified bundle references.
- Atomic baseline/revision-guarded page batches and rollback that preserve newer edits; requires the explicit host content-revision migration.
- Transactional saved page history retains changed-page before/after content and trusted host-supplied actors for page writes, batches and rollback; requires the explicit history migration and coordinated host adoption.
- Tenant-authorized history readback provides bounded cursors and an adoption-start marker. Refused stale writes append no history; pre-adoption edits and unsaved drafts are not reconstructed.

### Changed

- Page update/delete require the loaded expected version, and the editor retains unsaved drafts on conflicts.
- Promotion integration retains released normalized-HTML source preservation and editable-control contrast. No live promotion endpoint is enabled.

## [0.1.0] - 2026-05-25

### Added

- 4 module types declared: `cms.tenant_resolver`, `cms.static_serve_before_dynamic`, `cms.engine`, `analytics.injection`.
- 2 step types declared: `step.cms_render_page`, `step.cms_bundle_activate`.
- Strict contract descriptors and proto-compatible contract source for all advertised module and step types.
- Release metadata with platform download URLs and embedded runtime manifest.
- Tenant resolution, static-before-dynamic serving, CMS page CRUD/rendering, bundle activation, analytics HTML injection helpers, and audit-chain recording for admin writes.
