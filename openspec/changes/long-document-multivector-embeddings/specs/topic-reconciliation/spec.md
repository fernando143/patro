# Delta for Topic Reconciliation

## ADDED Requirements

### Requirement: Use-case-specific multi-vector scoring

Content coverage MUST be `C(A→B)=mean_a(max_b(dot(a,b)))`; title coverage MUST be `T=min(C(TA→TB),C(TB→TA))`. If both content sets and both title sets exist, `S=.9*C+.1*T`; otherwise `S=C`. Auto-merge/proposal MUST require nonempty content on both sides; title-only evidence MUST NOT authorize either.

Queries/candidates MUST use `S(A→document)`; historical migration MUST use `min(S(A→B),S(B→A))`. Ties/pair IDs MUST sort ascending; representation failure MUST abort before mutation.

#### Scenario: TR-01 Directed coverage finds a late candidate match

- GIVEN a 609-position candidate and longer target with unrelated sections
- WHEN normal/flagged reconciliation uses directed coverage
- THEN extras do not dilute; title adds at most 0.10 and alone cannot merge

#### Scenario: TR-02 Symmetric migration rejects partial duplicates

- GIVEN 609-position documents share one passage but lack reverse coverage
- WHEN migration scores the pair
- THEN `min` rejects them and equal scores sort by ID

#### Scenario: TR-03 Equal scores have stable order

- GIVEN documents tie
- WHEN ranked repeatedly
- THEN ascending ID always wins

## MODIFIED Requirements

### Requirement: Threshold-based reconciliation

Candidates MUST be scored before persistence. Profiles MUST exact-match embedbench identity. Reconciliation MUST use mode `directed-reconciliation`; migration MUST use separate mode `symmetric-migration`. Unqualified/cross-mode profiles MUST NOT authorize merge/proposal.

A matching directed profile MUST provide lowercase `n<m`: `≥m` auto-merges, `<n` creates new, otherwise exactly one LLM call. A matching symmetric profile MAY propose migration only at `≥m`. Legacy `0.90/0.70` MUST be recalibrated. Without a match, score action MUST be disabled; only successful LLM adjudication MAY merge, otherwise MUST flag.

(Previously: one-vector cosine used fixed 0.90/0.70 bands.)

#### Scenario: TR-04 Calibrated high-similarity merge

- GIVEN candidate and target have content and score `≥m` under a matching directed profile
- WHEN `AddMeetingCtx` runs
- THEN it appends to the match, not a new file

#### Scenario: TR-05 Calibrated low-similarity new topic

- GIVEN every topic scores `<n` under the matching directed profile
- WHEN reconciliation runs
- THEN a new topic uses the proposed slug

#### Scenario: TR-06 Confidence unavailable is conservative

- GIVEN legacy, unqualified, wrong-mode, or stale profiles
- WHEN reconciliation/migration run without successful adjudication
- THEN no score merge/proposal occurs and candidates remain flagged

### Requirement: Vector-space integrity across backend switches

V2 MUST be RFC 8785 canonical JSON with `model_weights_sha256`, `source_hash`, and chunk `token_count`; aliases `weights_sha256`, `source_sha256`, and `payload_tokens` MUST NOT appear. It MUST persist `normalization_version:"l2-f32-v1"`, `scorer_version:"coverage-title-v2"`, actual `model_version:"cybertron-spago-v1"`, representation identity, dimension, and sorted entries/chunks. V1, malformed data, or identity mismatch MUST set `NeedsRebuild` and MUST NOT be scored.

v0.5.x expects legacy `model_version:"cybertron"`; therefore v2 MUST mismatch and rebuild under v0.5.x rather than be scored. Model weights/dimension MUST remain unchanged; only metadata becomes truthful.

(Previously: three tags invalidated a one-vector store.)

#### Scenario: TR-07 Backend or fingerprint changed

- GIVEN backend or representation fingerprint mismatches
- WHEN startup or `patro reconcile` opens the store
- THEN stale scores are withheld and rebuild is required

#### Scenario: TR-08 V1 invalidates and v2 round-trips

- GIVEN v1 and shuffled equivalent v2 inputs
- WHEN loaded and v2 is saved again
- THEN v1 requires rebuild and v2 round-trips to byte-identical bytes

#### Scenario: TR-09 V0.5.x rejects v2 metadata

- GIVEN identical vector bytes in legacy and v2 fixtures
- WHEN the v0.5.x `cybertron` tag checks v2 `cybertron-spago-v1`
- THEN tags mismatch, rebuild is required, and no v2 vector is scored

## TDD Traceability

Apply evidence MUST map TR-01 through TR-09 to RED, GREEN, and REFACTOR runs; the final root gate MUST be `go test ./...`.
