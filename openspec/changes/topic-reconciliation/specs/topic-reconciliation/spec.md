# Spec Delta: topic-reconciliation

> No prior `openspec/specs/topic-reconciliation/` exists — this is a brand-new
> capability domain, so every requirement below is `ADDED`, not modified,
> even though the proposal originally labeled some adjacent capabilities
> "Modified." Backfilled after design revision 3.

## Purpose

Deterministic, traceable merge-vs-new decision at meeting ingestion,
replacing `AppendTopicSection`'s (library.go:208) exact-slug-only matching,
per design D1/D2/D10.

## ADDED Requirements

### Requirement: Threshold-based reconciliation
The system MUST embed the candidate topic (name+content) and compare it via
cosine similarity against existing topic vectors before persisting. Score
≥ `merge_threshold` (0.90) MUST auto-merge into the closest topic; score
< `new_topic_threshold` (0.70) MUST create a new topic; the gray zone MUST
trigger exactly one reconciliation LLM call.

#### Scenario: High-similarity merge
- GIVEN an existing topic "react-hooks" and a new meeting whose embedding scores 0.93 against it
- WHEN `AddMeetingCtx` runs
- THEN the meeting is appended to the "react-hooks" topic file, not a new file

#### Scenario: Low-similarity new topic
- GIVEN no existing topic scores ≥ 0.70 against the candidate
- WHEN `AddMeetingCtx` runs
- THEN a new topic file is created using the LLM-proposed slug

### Requirement: Traceable, non-silent merges
Every auto-merge MUST record the original LLM-proposed slug/name and score,
both as a markdown annotation and in `.state/reconciliation.json` (decision
1, D4).

#### Scenario: Merge produces audit trail
- GIVEN a merge at score 0.93 where the LLM proposed slug `x-y`
- WHEN the merge is committed
- THEN the topic markdown contains an annotation citing proposed slug `x-y` and cosine 0.93, AND `.state/reconciliation.json` gets a matching entry

### Requirement: Safe-fail toward new topic
A gray-zone reconciliation LLM call that errors or times out MUST NOT
auto-merge under any circumstance. It MUST create a new topic and flag it
`needs-reconciliation` in `.state/` (decision 3).

#### Scenario: LLM timeout in gray zone
- GIVEN a candidate scoring 0.80 (gray zone) and the reconciliation LLM call times out
- WHEN `AddMeetingCtx` completes
- THEN a new topic is created and flagged `needs-reconciliation`, and no merge occurs

### Requirement: Vector-space integrity across backend switches
Vectors from different embedding backends MUST NOT be treated as
comparable. The vector store MUST persist its producing
backend/dim/model_version; a mismatch against the configured
`embedding_backend` MUST invalidate the store and trigger `Rebuild()` rather
than return misleading similarity scores (D10).

#### Scenario: Backend changed in config
- GIVEN `.state/vectors/topics.json` tagged `backend=cybertron` and config now sets `embedding_backend: zerfoo`
- WHEN the store is next accessed (serve startup or `patro reconcile`)
- THEN the mismatch is detected, no stale-backend similarity score is used, and a rebuild is scheduled

### Requirement: No lost meetings during rebuild
A meeting processed while a vector-index rebuild is in progress MUST NOT be
lost or silently skipped by reconciliation. `vectors.Nearest()` MUST return
`ErrRebuilding`, the reconciler MUST map that to the
safe-fail-toward-new-topic-plus-flag path, and the meeting MUST be picked up
by the next reconciliation pass.

#### Scenario: Meeting arrives mid-rebuild
- GIVEN a rebuild is in flight (single-flight gate held)
- WHEN a new meeting is processed and queries `Nearest()`
- THEN `ErrRebuilding` is returned, the meeting's topic is created new and flagged, and the following `patro reconcile` pass includes it

### Requirement: Backward-compatible reconciler seam
`AddMeeting`/`AppendTopicSection` (existing signatures) MUST remain
unchanged and MUST continue to produce today's exact-slug-only behavior
when no `Reconciler` is configured (nil-safe). Existing `library_test.go`
and `analyzer_test.go` contracts MUST keep passing unmodified.

#### Scenario: Nil reconciler preserves legacy behavior
- GIVEN a `Library` with `Reconciler == nil`
- WHEN `AddMeeting` is called with a slug matching an existing topic file exactly
- THEN behavior is identical to pre-change code, and no embedding/reconciliation call occurs

#### Scenario: Existing test suite unaffected
- GIVEN the current `library_test.go` and `analyzer_test.go` suites
- WHEN run against the changed code
- THEN all existing cases pass without modification

## Out of Scope

The following is explicitly NOT covered by this change (per the original
proposal); no scenario in this spec exercises it:

- Topic file compaction/summarization.
- Automatic retroactive merging of already-fragmented topics (report for
  review only; no bulk auto-merge).
- Topic hierarchy/tags.
