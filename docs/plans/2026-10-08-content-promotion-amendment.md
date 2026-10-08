# Content snapshot promotion implementation amendment

This additive CMS implementation follows the parent-authorized Tina publication
assessment in multisite `docs/runbook/tina-content-promotion.md`. It preserves
the locked Tina plan, CMS PR31 at `0ceed5592a335e16b2eb6ddc5148ef45f248d0a7`,
CMS main's separate editor changes, all consumer pins and active worktrees.

## Scope and boundaries

1. Typed content-only snapshot export/strict decoding, explicit target mappings,
   deterministic dry-run diffs and immutable bundle verification.
2. Whole-tenant page baseline guarded atomic batches and content-only before/after
   receipts. Guarded rollback refuses any intervening page mutation.
3. All ordinary CMS creates/updates/deletes serialize with batches. Update uses
   the caller's loaded `Page.Version`; API save/delete requires the client's
   `expected_version`, and the editor retains its draft on conflict.
4. Memory/API tests plus a real isolated Postgres schema prove stale saves,
   racing writes, all-or-nothing failure, mappings, omission and rollback.

There is no promotion HTTP route, production write, credential configuration,
host deployment, consumer-pin change or storage purchase in this amendment.
Passwords, hashes, tokens, domains, roles, memberships, sessions and repository
mapping are outside every promotion struct. Unknown JSON fields and malformed
payloads yield fixed errors without printing supplied values.

`PageStore.Delete` gains a required expected-version argument. This source API
change and the mandatory HTTP precondition require a coordinated host/plugin
release; consumers must not discard the browser's version or fall back to an
unconditional write. The historical PR31 editor remains unchanged in its own
worktree.

## Concurrency contract

Postgres page mutations take one transaction-scoped advisory lock per tenant.
Batches read and compare the complete tenant page ID/version/content baseline
under that same lock and commit all mutations in one transaction. The lock is
cooperative, not an atomic cloud-spec CAS or a promise covering arbitrary direct
SQL writers. Per-row version predicates protect stale ordinary saves/deletes
after promotion releases the lock. No omitted page is deleted.

Rollback compares the complete recorded post-state and restores only affected
content, retaining page IDs and increasing versions. Backups are content only;
their durable archive is the authorized publication operator's responsibility.

## Publication remains disabled

CMS database transactions cannot atomically switch filesystem bundles or cloud
bootstrap configuration. A live publication endpoint remains blocked on a host
write/publication fence, staged immutable bundle, verified bootstrap source,
durable backup, superadmin authority, approval digest and restart/redeploy proof.
Uploaded tenant media references are refused until durable copy/rewrite/retrieval
exists. Static files shadowing selected CMS routes refuse validation.

## Verification and resource ownership

Use the existing module/build caches, `GOWORK=off`, package-targeted tests with
`-p 2` at most. Parent authorized one focused local allocation and a fresh
`promotion_20261008_*` schema on existing task Postgres port 55432. No Docker,
new cache, full local race build, existing-schema mutation or process restart.
CI may run its existing full checks plus an isolated Postgres service. Record
exact-head checks and obtain independent source review before merge.

## Independent-review correction: durable mutation generation

Review of `f7fd874` found a delete/restore/edit/delete ABA hole: surviving page
rows alone cannot detect a changed-then-empty tenant. This additive correction
introduces a durable `cms_page_revisions` ledger with a unique content-scope
identifier and monotonic tenant revision. Every successful ordinary page write,
batch and rollback advances it under the same lock. Atomic `ReadPageState` returns
scope/revision plus rows; plans/receipts bind them and refuse replay or scope drift.
The scope is a content namespace identifier, not an authority grant or credential.

The explicit SQL migration is a **new required host schema prerequisite**. It
initializes existing tenants and new-tenant ledger rows. No automatic migration,
existing-schema mutation or consumer adoption is authorized by this source PR.
Before adoption, the host must review/apply the migration and fence writes while
replacing every older writer: mixed old/new plugin versions would bypass the
ledger. A new store instance against the same isolated database must retain the
rollback/replay guard. Canonical block links, absolute reference exclusions and
collision-free temporary paths receive regression coverage in the same correction.

## Independent-review correction: nested HTML resources

The full `afc9dc9` review found unchecked nested/resource HTML attributes.
Preparation now refuses inline nested documents and object/plugin embeddings,
validates form override URLs and the other supported resource attributes, and
refuses unsupported document base/refresh overrides. Regressions exercise page
bodies, selected CMS templates and bundled HTML, including private upload/API
URLs. Public HTTPS iframe sources and validated form overrides remain supported.
This does not add a general active-content sandbox or change the host publication
gates, schema prerequisite, consumer pins or approval boundary.

## Consumer-boundary correction: selected public media index

The unchanged reviewed-source PostgreSQL consumer completed the generic frozen
snapshot → explicit mapping/dryrun → apply → safe rollback path, including
cross-tenant/scope refusals, monotonic versions/revisions and newer-edit refusal.
The optional private six-page bundle then exposed a public `/media` navigation
collision with the upload namespace refusal. The failed optional run and all
completed generic evidence remain preserved outside this public repository.

Parent authorizes a narrow exception for relative hyperlinks to exactly the
selected public CMS route `/media`. Resource sources, upload children, absolute
private URLs, unselected routes and other private namespaces still refuse.
Acceptance covers body/template/static HTML and canonical block hrefs, encoded
and dot-normalized upload refusals, plus the private bundle consumer in a fresh
PostgreSQL schema. New exact-head tests and full independent source review are
required. No hosted write, migration/adoption, source pin or live apply route
is added; the prior locked plan and release gates stay intact.
