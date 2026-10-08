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
