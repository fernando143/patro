# Delta for Embedbench

## ADDED Requirements

### Requirement: Long-input multi-vector report

`tools/embedbench` MUST accept long A/B inputs through the production document embedder and report representation fingerprint, title/content chunk counts, per-chunk token counts, directed scores both ways, symmetric migration score, capped title contribution, and deterministic rank/tie identity. It MUST report an input or chunk error explicitly and MUST NOT omit a failed side or substitute a partial score.

#### Scenario: EB-01 Benchmark the observed long-input class

- GIVEN A and B include exact-tokenizer 609-position fixtures with beginning, middle, and end markers
- WHEN the report is generated
- THEN both sides embed without an oversized call and all required chunk and score fields are present

### Requirement: Deterministic threshold calibration

Calibration MUST produce separate RFC 8785 profiles for exact modes `directed-reconciliation` and `symmetric-migration`. Each profile MUST contain exactly: `profile_schema:"calibration-v1"`, `scorer_mode`, `scorer_version:"coverage-title-v2"`, `backend`, `model_id`, `model_version`, `model_weights_sha256`, `representation_fingerprint`, `normalization_version:"l2-f32-v1"`, `corpus_id`, `corpus_sha256`, `sample_count`, `negative_support`, `positive_support`, `n`, `m`. Support/count fields MUST be integers; `n`,`m` numbers; hashes lowercase hexadecimal.

Within each mode, `m` MUST be the lowest score with at least 20 at-or-above labels and zero non-duplicates; `n` the highest score with at least 20 below labels and zero duplicates. `sample_count` MUST count all labels; `positive_support`/`negative_support` MUST record those qualifying regions. Profiles MUST require `n<m`, sort fixture ties by ID, exact-match identity, and MUST NOT cross modes.

If either region lacks support, bands overlap, or fingerprints differ, calibrated confidence MUST be reported unavailable and no thresholds MUST be emitted. Legacy `0.90/0.70` MUST NOT be imported as samples or defaults.

#### Scenario: EB-02 Valid corpus emits one stable profile

- GIVEN labeled directed and symmetric corpora satisfying both support rules
- WHEN shuffled copies are calibrated
- THEN each mode emits its own byte-identical canonical profile and identity

#### Scenario: EB-03 Insufficient confidence emits no profile

- GIVEN fewer than 20 qualifying pairs, a mislabeled tail, overlapping bands, or a stale fingerprint
- WHEN calibration runs
- THEN it reports the exact failed condition and emits no reusable threshold profile

### Requirement: Resource and performance gates

The exact authoritative host MUST be idle AC Linux/amd64, Ryzen 7 7800X3D, 32GB RAM, Go 1.26.5, `GOMAXPROCS=8`, CPU governor `performance`. Nonmatching/unavailable governor MUST produce `authoritative:false` and MUST NOT pass acceptance. Corpus manifest MUST fix ID, SHA-256, ordered cases, dimensions, and seed.

One process/model load MUST run 5 warmups then exactly 30 timed runs. P50/p95 MUST use nearest-rank `ceil(p*30)` over sorted integer nanoseconds. After one pre-block GC, mean bytes/run MUST be `TotalAlloc` delta across those same 30 runs divided by 30. The timestamp-free RFC 8785 report MUST use stable cases and an explicit `authoritative` flag.

Every elapsed gate MUST use p95 from its same 30 runs: wrapper p95 MUST be ≤1.25× direct emitted-window baseline p95; search p95 ≤100 ms; symmetric 100×10-chunk migration p95 ≤5 seconds. Allocation gates MUST use mean `TotalAlloc` delta/run from those same blocks: wrapper-minus-baseline ≤16 MiB, search ≤64 MiB, migration ≤256 MiB. V2 JSON MUST use deterministic bytes ≤8 KiB/vector +1 KiB/document, never a percentile. Separate acceptance gates MUST fail on any miss; deterministic unit tests MUST inject metrics, never measure wall time.

#### Scenario: EB-04 Every bound gates acceptance

- GIVEN matching or mismatching host/corpus manifests and fixed cases
- WHEN the separate gate performs the canonical 5+30 protocol
- THEN output passes only when authoritative and all p95, mean-allocation, and deterministic-byte bounds pass

## TDD Traceability

Apply evidence MUST map EB-01 through EB-04 to RED, GREEN, and REFACTOR runs. Verification MUST run `go test ./...` in both the root and `tools/embedbench` modules.
