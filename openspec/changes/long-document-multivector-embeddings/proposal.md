# Proposal: Long-Document Multi-Vector Embeddings

## Intent

Cybertron rejects documents beyond 512 positions. A 609-token topic aborts migration; equivalent paths omit vectors, fragment topics, or degrade search. Patro will use one lossless multi-vector representation without replacing Cybertron or its model.

## Scope

### In Scope
- Exact-tokenizer, deterministic, ordered chunking within encoder limits; no truncation.
- Normalized chunks with stable model/tokenizer/representation/source metadata.
- Apply to migration, vector rebuild/sync, candidate/flagged reconciliation, web queries, and `tools/embedbench`.
- Versioned persistence with v1/incompatible-fingerprint invalidation, source-hash freshness, deterministic order/ties, and atomic all-or-old updates.
- Specs/design SHALL decide exact chunk/overlap/title constants, aggregation formulas, calibrated thresholds, performance bounds, and store schema/fingerprints. Existing `0.90/0.70` auto-merge thresholds MUST NOT be blindly reused.
- Recorded RED-GREEN-REFACTOR evidence per work unit, ending with `go test ./...`.

### Out of Scope
- Replacing Cybertron, changing model weights/dimensions, or adding remote embedding services.
- Summarization, topic-taxonomy changes, or unrelated migration UX.
- Changing BM25 or post-vector reciprocal-rank fusion.

## Capabilities

### New Capabilities
None.

### Modified Capabilities
- `local-embeddings`: tokenizer-aware multi-vector representation and identity.
- `topic-reconciliation`: use-case scoring, calibrated merging, and compatible storage.
- `library-search`: long-query/document ranking while preserving BM25 fusion.
- `embedbench`: long-input scoring and calibration.

## Approach

Retain single-window inference as an internal primitive. One service emits ordered vectors. Search/reconciliation use deterministic directed coverage; migration uses symmetric coverage. The store rejects incompatible schema/model/representation data and rebuilds from Markdown. Failures preserve the complete snapshot.

## Affected Areas

| Area | Impact | Description |
|---|---|---|
| `internal/embed` | Modified | Tokenization, representation, identity |
| `internal/vectors` | Modified | Schema, scoring, atomic sync |
| `internal/{migration,library,web}` | Modified | Production embedding paths |
| `internal/pipeline`, `cmd/patro`, `tools/embedbench`, tests | Modified | Wiring, tooling, TDD proof |

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Score shifts cause false merges | High | Labeled fixtures; conservative calibration |
| Chunk growth raises resource use | Medium | Acceptance bounds and benchmarks |
| Partial/incompatible stores hide topics | Medium | Version rejection; atomic rollback |
| Single PR exceeds 800 lines | High | Tasks forecast before apply |

## Rollback Plan

Revert the binary, delete the derived vector file, and rebuild v1 from unchanged Markdown. Tags must make old binaries reject v2 entries.

## Dependencies

- Cybertron tokenizer/model; Markdown source of truth.

## Success Criteria

- [ ] Over-limit fixtures, including 609 tokens, pass every listed path with beginning/middle/end content represented and no oversized call.
- [ ] v1 invalidates; v2 round-trips deterministically; fingerprint changes rebuild; failed sync retains the old snapshot.
- [ ] Labeled fixtures approve formulas/thresholds; benchmarks meet specified bounds.
- [ ] Every work unit records RED, GREEN, and REFACTOR; `go test ./...` passes.
