# Tasks: Long-Document Multi-Vector Embeddings

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines | 5,000–6,500; fixtures included |
| Review budget | 800 lines |
| Delivery strategy | single-pr |
| Suggested split | WU1→WU2→WU3→WU4→WU5→WU6→WU7 |
| Size exception | Pending maintainer approval |

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: pending
400-line budget risk: High

Threat matrix: N/A.

### Suggested Work Units

| Unit | Goal/PR | Focused command | Runtime harness | Rollback boundary |
|---|---|---|---|---|
| WU1 | Representation / PR1 | `go test ./internal/embed -run 'Test(Chunk|Identity|Represent)'` | `go test ./internal/embed -run TestCybertron609Positions -count=1` | `internal/embed/{representation,chunker,identity}.go`; `internal/embed/cybertron.go` adapter |
| WU2 | Scoring/calibration / PR2 | `go test ./internal/embed -run 'Test(Scor|Calibrat)'` | `go test ./internal/embed -run TestCalibrationCorpusHarness -count=1` | `internal/embed/{scorer,calibration}.go` and tests |
| WU3 | V2 compatibility / PR3 | `go test ./internal/vectors -run 'Test(V2|Compatibility)'` | `go test ./internal/vectors -run TestV05RejectsV2 -count=1` | `internal/vectors/{store,codec}.go` and fixtures |
| WU4 | Incremental commit / PR4 | `go test ./internal/vectors -run 'Test(Sync|Commit)'` | `go test ./internal/vectors -run TestSync609Markdown -count=1` | `internal/vectors/{sync,commitfs,rebuild}.go` |
| WU5 | Reconcile/migrate / PR5 | `go test ./internal/library ./internal/migration ./internal/pipeline ./internal/tui ./cmd/patro -run MultiVector` | `go test ./internal/pipeline -run TestProcessVideo609Reconcile -count=1` | `internal/library/{library,reconcile}.go`, `internal/migration/{migration,configured}.go`, `internal/{pipeline/pipeline,tui/migrate}.go`, `cmd/patro/main.go` |
| WU6 | Web fallback / PR6 | `go test ./internal/web -run 'Test(Search|Fallback)'` | `go test ./internal/web -run TestSearch609Fallback -count=1` | `internal/web/*`, `cmd/patro` `wireSearch` |
| WU7 | Embedbench / PR7 | `(cd tools/embedbench && go test ./... -run 'Test(Report|Calibration|Acceptance)')` | `(cd tools/embedbench && GOMAXPROCS=8 go run . -acceptance -manifest testdata/corpus-manifest.json)` | `tools/embedbench/{main,server,report,acceptance}*.go`, fixtures |

Every unit records IDs, exact RED command and failing assertion/output, GREEN focused rerun/pass, and REFACTOR broader gate/pass.

## Phase 1: Representation and Scoring

- [ ] 1.1 **WU1 [LE-01–05]** RED: fakes plus real `internal/embed/testdata/cybertron-609.md` fail limits/loss/identity/cancellation → GREEN: exact tokenizer, 510/478/32 chunker, title and identity contracts → REFACTOR: `go test ./internal/embed`.
- [ ] 1.2 **WU2 [TR-01–03, EB-02–03]** RED: fake vectors/corpora fail formulas/ties/support/identity → GREEN: canonical directed/symmetric scorer and separate profiles → REFACTOR: `go test ./internal/embed`.

## Phase 2: Persistence and Sync

- [ ] 2.1 **WU3 [TR-07–09]** RED: v1/shuffled-v2/alias/malformed/v0.5 fixtures fail validity/canonicality → GREEN: RFC-8785 v2 round-trip, invalidation and rollback compatibility → REFACTOR: `go test ./internal/vectors`.
- [ ] 2.2 **WU4 [LS-01–02, LS-06–07]** RED: fake `CommitFS` matrix fails old-or-current/error joins → GREEN: source-hash reuse/delete and Dirty→Current durable commit/restore → REFACTOR: `go test ./internal/vectors`.

## Phase 3: Production Paths

- [ ] 3.1 **WU5 [TR-01–09, LS-01–02]** RED: fakes fail one-vector/fixed-threshold/pre-mutation rules → GREEN: wire listed library/migration/pipeline/TUI/CLI paths with dirty/sync → REFACTOR: `go test ./internal/library ./internal/migration ./internal/pipeline ./internal/tui ./cmd/patro`.
- [ ] 3.2 **WU6 [LS-03–05]** RED: fake deadline yields a partial semantic ID → GREEN: complete-query ranking, RRF-60 and observable all-BM25 fallback in `internal/web` → REFACTOR: `go test ./internal/web ./cmd/patro`.

## Phase 4: Tooling and Gates

- [ ] 4.1 **WU7 [EB-01–04]** RED: injected metrics/real 609 A/B fail fields/profiles/bounds → GREEN: canonical report, calibration and 5+30 time/allocation/size gates → REFACTOR: nested `go test ./...`.
- [ ] 4.2 Audit evidence; pass root `go test ./...` and nested `(cd tools/embedbench && go test ./...)`; document v2 deletion/rebuild rollback.
