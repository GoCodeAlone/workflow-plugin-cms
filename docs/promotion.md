# Preparing approved content publication

The `promotion` package prepares frozen, content-only snapshots and explicit
production diffs. It has no network client, background synchronization or live
apply endpoint. Review edits stay in their review tenant. An approval must name
the frozen snapshot digest and production diff; later drafts require a new
approval.

Export selected page IDs from an authorized review tenant using
`promotion.Export(ctx, pages, reviewID, sourceSite, ids, immutableBundleDir, now)`.
Archive the returned snapshot and exact bundle bytes together. The snapshot
includes source page ID/version keys, content and SHA-256 file inventory. Strict
`promotion.Decode` refuses unknown/duplicate JSON keys, trailing documents and
oversize input using fixed errors. It never imports tenant settings or authority.

Read the production tenant atomically using `ReadPageState` and inventory its immutable bundle. The returned durable content scope/revision is part of the batch baseline. Review
`promotion.SuggestedMapping` path matches, then supply an explicit mapping for
every selected page to `promotion.DryRun`. Target page IDs are distinct from source
IDs. A zero target ID explicitly creates; moves require `AllowMove`; deletion
requires a separate `DeleteSelection`. Omission preserves existing pages. The plan
contains the complete target page baseline, both bundle digests and content diffs.
Dry run never writes.

`store.PageBatchStore` supplies persistence primitives, not publication authority.
`ApplyPageBatch` atomically compares the entire page baseline and applies content
in one tenant. Ordinary creates, saves and deletes share its per-tenant lock.
Update requires the caller's loaded `Page.Version`. API PUT and DELETE require
`expected_version`; missing preconditions fail and stale versions return 409.
The editor keeps an unsaved draft on conflict and refreshes its version after a
successful save. Existing external API consumers must upgrade before this plugin
is adopted. Every successful page write also advances the durable tenant content
revision; receipts bind that revision and the content scope, preventing receipt
replay even after create/delete history leaves the same surviving rows. `PageStore.Delete` also gains a required expected-version argument.

Archive the content-only batch receipt before completing host publication. Its
before/after rows, explicit mapping and integrity digest support
`RollbackPageBatch`, which refuses any change to the recorded post-state and
restores affected content with increasing versions. The receipt digest detects
corruption; it is neither a signature nor approval. Durable backup storage and
operator authorization belong to the host publication workflow.

`VerifyBundle` re-hashes regular files, verifies template/assets references and
refuses symlinks, missing assets, uploaded tenant media URLs, admin API references
and static files shadowing selected CMS routes. Bundled photos retain relative
references and exact hashes. The operator must stage those verified immutable
bytes in the target bundle. The library does not configure media storage, copy
uploaded objects, fetch third-party embeds or claim rights to external media.

HTML checks cover page bodies, CMS templates and bundled HTML. Inline nested
documents (`srcdoc`), object/plugin embeddings (`object`, `embed`), reference base
overrides and meta refresh are unsupported and refuse validation. Public HTTPS
iframe sources remain supported; form action/override URLs, image source sets
and other resource attributes use the same private-path and asset checks.
Both scripting modes are inspected, including `noscript` fallback resources.
Stylesheet and HTML extensions are matched without case sensitivity. Every
imported shell is checked, including shells not selected by the snapshot;
unselected shells require root-relative or public external references because
their eventual public page base is unknown. Template identifiers and the 1 MiB
shell limit share the runtime resolver's admission rules. Host-reserved page
routes and encoded path aliases are refused during snapshot validation.
CSS accepts ordinary `url()` and quoted or URL imports. CSS escapes and the
alternative string-image functions `image()`, `image-set()`,
`-webkit-image-set()` and `src()` are unsupported and fail closed. Comments and
ordinary text/font strings remain supported; no new CSS parser dependency is
installed.
This is reference validation, not a JavaScript sandbox. The host must review the
exact approved bundle's scripts and runtime behavior separately.

The public CMS index route `/media` may be a relative hyperlink only when that
exact route is in the selected snapshot pages. The exception covers navigation,
including canonical block links; it does not permit media resource URLs, upload
children, unselected routes, absolute private URLs or other private namespaces.
Only canonical `/media` or relative `media` path spelling qualifies; encoded,
dot-segment and trailing-slash aliases remain refused.

The SQL prerequisite `store/postgres/migrations/0001_page_content_revision.up.sql`
must be reviewed and applied by the host before adoption. It initializes tenant
content scopes/revisions and new-tenant initialization. This library never applies
it automatically. Fence writes while replacing all older writers; mixed versions
bypass the ledger. Preserve ledger history across restart/backup, and never drop
or reset it to force a conflicted rollback.

Live application remains blocked on host integration: current superadmin
publication authority, a named approval digest, an exclusive publication/write
fence, staged immutable assets, durable content/bundle backups, approved production
bootstrap source and actual restart/redeploy proof. A database transaction cannot
atomically swap a filesystem bundle or cloud bootstrap setting. Preserve the
existing domains, credentials, roles, memberships and repository mapping. If the
host cannot provide the fence and restart-safe bundle, use a separately reviewed
maintenance procedure and do not enable partial publication.

## Verification

`go test ./store ./promotion ./internal ./adminui ./store/postgres` runs focused
unit/API checks. Set `CMS_TEST_DATABASE_URL` to an isolated authorized test server
to run the real Postgres contract; each test creates and cleans only a fresh
`promotion_20261008_*` schema. CI provisions a disposable Postgres service and
checks the exact PR head before its existing vet/race checks.

The optional actual-editor test uses already installed tools. Set
`CMS_TEST_PLAYWRIGHT_PATH` to the local Playwright package and
`CMS_TEST_BROWSER_EXECUTABLE` to the browser binary; `CMS_TEST_SCREENSHOT` is an
optional screenshot destination. `TestPostgresEditorVersionBrowser` launches
only a temporary browser context and ephemeral loopback listener. Its authority
and promotion helper exist only in the test binary. It demonstrates repeated
save version refresh, stale save/delete conflicts after a batch, retained draft,
wrong-tenant denial and save/reload through the real editor/API/Postgres boundary.
It does not prove hosted authentication or host bundle activation.
