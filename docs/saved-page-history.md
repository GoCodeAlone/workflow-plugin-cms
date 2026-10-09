# Saved page history and concurrent editing

This is an additive dependency on the content-promotion implementation. It is
source support, not an applied host migration or runtime adoption.

Every successful managed page create, update, delete, batch apply and batch
rollback appends one saved history event in the same transaction as the page
mutation and monotonic tenant revision. Each event records the changed pages'
before/after versions and full content (including blocks, HTML, schedules,
template and status), content scope, operation, UTC saved time and actor label.
Deletes retain their former saved content. Unchanged pages are omitted from
batch events. A saved version-only edit is still recorded.

The host supplies an authenticated stable subject through its existing
AuditActor callback. Direct store clients use WithPageWriteActor only after
their own authentication and authorization. JSON, arbitrary headers and
unverified token claims must never set that context value. Missing or invalid
labels are recorded as unattributed. This metadata grants no permissions.
Digests detect content corruption; they are not signatures or proof of the
actor's authority. The existing audit logger remains separate.

## Read saved state

GET /api/v1/admin/tenants/:tid/pages/history accepts canonical after_revision
(exclusive, default 0) and limit (1–100, default 20). Both request authorization
and current tenant access callbacks are required, including direct AdminAPI
use. Configure the host to authorize this route with the same tenant-scoped
page-read authority as ordinary private page reads. Responses are private,
no-store and include the content scope, adoption marker, current tenant
revision, immutable events, next cursor and has_more. They include drafts, so
they must never be exposed on a public content route.

Use the saved page's ID, Version and content digest to compare a fresh page read
with its latest applicable history event. Read more pages of history until
has_more is false; current_revision is an atomic head for each response. Repeat
from that head if new saved writes occur during pagination. PostgreSQL uses a
read-only repeatable-read transaction; reconnecting does not lose the history.

PUT and DELETE continue to require the editor's loaded expected_version.
Missing preconditions fail; stale versions return 409 without a page write,
revision increment or history event. The caller must retain the draft and
review a fresh read. Never retry a stale save with a substituted fresh version.

## Explicit adoption and retention

1. Review and install the first content-revision SQL prerequisite, then the
   additional store/postgres/migrations/0002_page_saved_history.up.sql under
   the same exclusive write fence. The second prerequisite records each
   tenant's current revision as history_start_revision. It creates no past
   actor claims or content snapshots. The first event is the next revision.
2. Ship matched plugin and host authorization/callers. Replace every old page
   writer before releasing the fence. Missing history schema or an unrecorded
   cooperative-writer revision gap fails closed and rolls back the entire
   managed write. Reconcile gaps from trustworthy evidence; never reset the
   marker to disguise missing events.
3. Include pages, cms_page_revisions and cms_page_history in database backups.
   Verify content scope, start marker, saved head and history on restore.
   History is append-only through database UPDATE/DELETE/TRUNCATE triggers.
   Tenant deletion is blocked while retained history references its ledger;
   privileged retention or erasure requires a separate reviewed procedure.
4. Extend the host's versioned schema and auth-continuity admission explicitly
   for this second prerequisite. The existing clean10→clean11 admission
   prepared for the first revision ledger does not cover it. No migration,
   plugin version pin, release, auth-baseline rewrite or deployment is performed
   by this library or change.

All participating writers must use this store; arbitrary external SQL page
edits are not covered by the cooperative store transaction contract.
Schema owners can bypass triggers, so this is not a tamper-proof audit service.
There is no public restore endpoint or automatic retention deletion.

## Unsaved drafts

Database history verifies committed saves after adoption. It cannot reveal
text that exists only in a browser tab, reconstruct pre-adoption editing
history, or guarantee that an owner is not currently typing. An independent
unsaved-draft check needs an explicit editor autosave/presence protocol with
separate draft state, identity, expiry and conflict handling. It must not treat
an empty presence record as permission to overwrite saved content. Current
CAS protects the next saved write regardless of such presence.

## Validation

The shared memory/PostgreSQL contract covers snapshots, trusted actor labels,
deletion, pagination, deep-copy isolation, stale-save refusal, batch/rollback
history and replay refusal. Actual PostgreSQL fixtures use unique task-owned
schemas and check reconnect durability, adoption without fabricated backfill,
append-only triggers, missing-schema/mixed-writer fail-closed behavior,
concurrent save winners and transaction rollback after a late history INSERT
failure. HTTP fixtures cover protected readback, tenant denial, unconfigured
auth refusal, canonical queries/routes and ignored arbitrary actor headers.
