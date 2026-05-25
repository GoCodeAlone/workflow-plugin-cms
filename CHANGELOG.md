# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-05-25

### Added

- 4 module types declared: `cms.tenant_resolver`, `cms.static_serve_before_dynamic`, `cms.engine`, `analytics.injection`.
- 2 step types declared: `step.cms_render_page`, `step.cms_bundle_activate`.
- Strict contract descriptors and proto-compatible contract source for all advertised module and step types.
- Release metadata with platform download URLs and embedded runtime manifest.
- Tenant resolution, static-before-dynamic serving, CMS page CRUD/rendering, bundle activation, analytics HTML injection helpers, and audit-chain recording for admin writes.
