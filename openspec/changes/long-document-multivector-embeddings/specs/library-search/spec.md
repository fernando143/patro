# Delta for Library Search

## MODIFIED Requirements

### Requirement: Derived, rebuildable index

Indexes MUST remain Markdown-derived. Rebuild MUST restore BM25 and v2 semantic state, process long documents losslessly, and report document failure without publishing partial state.

(Previously: Markdown rebuild lacked multi-vector all-or-old guarantees.)

#### Scenario: LS-01 Rebuild from Markdown only

- GIVEN deleted indexes and a 609-position Markdown topic
- WHEN rebuild runs
- THEN both rebuild, all long-topic vectors exist, and `/search` returns correct hits

#### Scenario: LS-02 Failed rebuild omits nothing

- GIVEN a usable snapshot and one failing rebuild document
- WHEN rebuild reaches that document
- THEN rebuild reports its ID, preserves old state, and publishes no partial index

### Requirement: /search endpoint

`/search` MUST remain read-only across topics/meetings. Its semantic leg MUST embed the complete query, use canonical `S(query→document)` and ID ties, then fuse BM25/semantic ranks with unchanged RRF `k=60`.

Embedding/scoring/ranking MUST accept context, check between query chunks/documents and before return, and return errors for failure/deadline, never partial IDs. The handler MUST discard the semantic leg, render all BM25 hits, and expose fallback.

(Previously: `/search` fused one-vector cosine with BM25.)

#### Scenario: LS-03 Query returns hits from both sources

- GIVEN topics and meetings containing overlapping terms
- WHEN a user submits `/search?q=...`
- THEN results include both kinds ranked by unchanged reciprocal-rank fusion

#### Scenario: LS-04 Long query matches beginning, middle, and end

- GIVEN a marked 609-position query and topic matching only its end
- WHEN `/search` runs
- THEN no encoder call exceeds 510 payload tokens and directed coverage ranks the matching topic deterministically

#### Scenario: LS-05 Semantic failure falls back completely

- GIVEN BM25 hits and a deadline reached after semantic scoring has ranked one document
- WHEN context-aware ranking returns `context.DeadlineExceeded`
- THEN all partial semantic IDs are discarded, all BM25 hits render, and fallback is observable

## ADDED Requirements

### Requirement: Fresh atomic vector maintenance

Sync MUST transition `Dirty → Building → Prepared → CommitIntent → Current`. `Building` MUST hash sources, build the snapshot, write/fsync/close its candidate, create+fsync an old-snapshot rollback copy/link, fsync that directory, then enter `Prepared`.

`Prepared` MUST perform the FINAL context check. Observable cancellation MUST clean candidates, preserve old/absent disk+memory, and return `Dirty`. A successful check MUST atomically enter `CommitIntent`; cancellation is then masked through rename, parent-directory fsync, memory swap, and rollback. Cancellation arriving after `CommitIntent`, even before rename, MUST NOT cause cancellation failure; only storage failure may fail.

In `CommitIntent`, successful rename plus parent fsync commits; memory MUST swap and state become `Current`. Rename/fsync failure MUST restore rollback, fsync target+directory, retain old memory, remain `Dirty`, and report the primary error plus restoration error when applicable. First creation MUST use absence as old state and remove uncertain targets.

Commit I/O MUST use an injected boundary exposing `CreateTemp`, `LinkOrCopyRollback`, `SyncFile`, `Close`, `Rename`, `SyncDir`, `Restore`, `Remove`; production MUST use the OS and tests a deterministic fake.

#### Scenario: LS-06 CommitIntent masks late cancellation

- GIVEN cancellation observable in `Prepared` and another arriving after `CommitIntent` but before rename
- WHEN sync runs both cases
- THEN the first returns cancellation with old `Dirty` state; the second masks cancellation through commit/rollback

#### Scenario: LS-07 Injected commit failures are independent

- GIVEN the fake independently fails candidate sync, rollback creation/sync, rename, parent fsync, restore, restore fsync, or cleanup
- WHEN each RED case runs from old and absent snapshots
- THEN state plus disk/memory bytes satisfy old-or-Current semantics
- AND errors name primary and rollback/cleanup failures where applicable

## TDD Traceability

Apply evidence MUST map LS-01 through LS-07 to RED, GREEN, and REFACTOR runs; the final root gate MUST be `go test ./...`.
