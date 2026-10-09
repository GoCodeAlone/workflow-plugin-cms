# Content promotion verification

This verifies CMS persistence/preparation primitives, not hosted publication.
There is no live apply endpoint, consumer-pin update or production content write.
Host bundle fencing, durable backup/bootstrap integration and restart/redeploy
proof remain required before live publication can be enabled.

## Local behavior demonstrated

Focused tests use Go 1.27.1, existing shared module/native build caches,
`GOWORK=off`, offline dependencies and concurrency two. Real Postgres uses only
fresh `promotion_20261008_*` schemas on the existing task-owned local server;
each schema is cleaned by its test. No runtime tables, processes or ports change.

| Boundary | Observed result |
| --- | --- |
| Snapshot export → strict decode | Frozen selection survives decode; later source edits do not affect approved content; authority fields, duplicate keys, trailing documents and malformed fields refuse with fixed errors |
| Source → target mapping/dryrun | IDs differ; explicit mapping required; implicit moves refused; dry run writes nothing; omitted target page retained |
| Batch → real Postgres → reload | Update/create/delete persist atomically; mapping names target identities; complete page baseline checked |
| Ordinary save/delete → batch | Stale loaded versions conflict; racing save/promotion and delete/promotion each admit one winner |
| Database failure mid-batch | Isolated trigger refuses the second item; earlier update rolls back and complete baseline remains unchanged |
| Durable revision → rollback replay | Deleted/restore/edit/delete ABA and create/delete history refuse replay; fresh Postgres connections retain the guard |
| Path moves → Postgres | Occupied legacy staging paths do not block title-only updates; two-page path swap persists |
| Canonical blocks → references | Internal/relative/anchor/mail/HTTPS links export through the actual renderer; arbitrary slash text is preserved; absolute/encoded admin/API/media references refuse |
| Receipt → rollback | Content restored with increasing versions; created page removed; deleted page restored; later edit refuses whole rollback |
| Bundle → validator | Changed bytes, missing assets, static homepage shadow, symlink, admin reference and encoded tenant-upload references refused |
| HTML resource surfaces → validator | Page bodies, templates and bundled HTML refuse nested `srcdoc`, object/plugin embeddings and private resource/form overrides; public HTTPS iframe sources and verified local overrides remain usable |
| Actual Chrome editor → API → Postgres | Repeated saves send versions 1/2; fixture promotion changes version to 4; stale save/delete send 3 and return 409; unsaved draft retained; reload then version-4 save persists as version 5; wrong tenant returns 403 |

The browser test launches an ephemeral loopback test server and a temporary
Chrome context using already installed Playwright. Authority and promotion helper
exist only in the test binary. It is not a hosted authentication or domain proof.
The task evidence directory retains the browser conflict screenshot and test logs.

## Release limits

The explicit durable-revision SQL migration and a fenced rollout replacing every
older writer are mandatory new host schema prerequisites. No automatic migration
or host schema deployment occurred. `PageStore.Delete` now requires expected version; external API clients must send
`expected_version` for PUT/DELETE. The CMS host must consume this change in a
separate reviewed release. The historical PR31 worktree and current consumer
pin are preserved. Independent review and exact committed-head CI are recorded
separately; this document does not imply those gates passed before observation.
