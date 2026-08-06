# Tasks: meeting-note-regeneration (`patro regenerate <transcript-file>`)

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~550–750 (additions+deletions) |
| 400-line budget risk | High (exceeds base 400-line guard; within session-cached 800-line override) |
| Chained PRs recommended | Yes (reviewability), not mandatory under the 800-line session cap |
| Suggested split | PR 1 → PR 2 → PR 3 (library → pipeline → cmd) |
| Delivery strategy | single-pr |
| Chain strategy | pending — ask maintainer: chain anyway, or accept `size:exception` under the 800-line session budget |

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: pending
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | `internal/library` extensions (D1/D3/D4/D7) | PR 1 | `go test ./internal/library/... -run 'ResolveTranscriptID|FindMeetingNoteByTranscriptID|WriteMeetingNoteAt' -v` | N/A — pure filesystem unit tests, no live pipeline entry point yet | Revert `library.go`/`library_test.go` diff; no callers exist yet |
| 2 | `internal/pipeline.Regenerate` (D1–D9) | PR 2 | `go test ./internal/pipeline/... -run Regenerate -v` | `go build ./... ` then call `pipeline.Regenerate` from a throwaway `go run` (no CLI wired yet) | Revert `regenerate.go`/`regenerate_test.go`; Unit 1 unaffected |
| 3 | `cmd/patro` wiring (D2/D6/D8/D9) | PR 3 | `go test ./cmd/patro/... -run 'ParseArgs|RunRegenerate' -v` | `./patro regenerate knowledge/transcripts/<id>.txt --mock` | Revert `main.go`/`main_test.go` diff; Units 1–2 unaffected |

Units are sequential (each depends on the previous); none run in parallel.

## Phase 1: `internal/library` extensions

- [x] 1.1 RED: `library_test.go` — table test `ResolveTranscriptID`: in-library stem reuse, external file gets stable `"ext-"+sha256[:12]` (D1).
- [x] 1.2 GREEN: `library.go` — implement `ResolveTranscriptID(path) (id string, external bool, err error)`.
- [x] 1.3 RED: table test `FindMeetingNoteByTranscriptID`: found / not found / malformed `Date` line (no error, `Date:""`) / unreadable file skipped (D3, D7).
- [x] 1.4 GREEN: add `PriorNote{Path, Date, SourceVideo}`, `FindMeetingNoteByTranscriptID(id) (*PriorNote, error)`, `transcriptLink(id) string` shared with `WriteMeetingNote`.
- [x] 1.5 RED: test `WriteMeetingNoteAt` overwrites the exact given path even when the new title's slug differs (D4).
- [x] 1.6 GREEN: extract `WriteMeetingNoteAt(path, t, a, videoPath, date)`; `WriteMeetingNote` computes `date+"-"+Slugify(title)` then delegates — signature/behavior unchanged.
- [x] 1.7 Verify: `go test ./internal/library/...` green, no regressions.

## Phase 2: `internal/pipeline.Regenerate`

- [x] 2.1 RED: `regenerate_test.go` — prior-note overwrite (date/SourceVideo reused), external-copy-before-analysis, missing-`--date`-no-prior-note fails with no file written, `af` spy asserts `existing == nil`, `--mock` deterministic, topics/index/`processed.json` byte-identical before/after.
- [x] 2.2 GREEN: `regenerate.go` — `Regenerate(ctx, transcriptPath, date, cfg, af) (notePath string, err error)`: `ResolveTranscriptID` → `FindMeetingNoteByTranscriptID` → resolve date (prior reused, else `--date` required, D2 error text) → copy transcript only if external, post-date/pre-analysis (D5) → `af(ctx, t, nil)` → `WriteMeetingNoteAt`/`WriteMeetingNote` with `SourceVideo` fallback (D7); no `*state.State`/`*status.Tracker` params.
- [x] 2.3 Verify: `go test ./internal/pipeline/...`; confirm `regenerate.go` never references `AddMeetingCtx`, `AppendTopicSectionAnnotated`, `RebuildIndex`, `ExistingTopics*`, `IsProcessed`, `MarkProcessed`.

## Phase 3: `cmd/patro` wiring

- [x] 3.1 RED: `main_test.go` `TestParseArgs` — `--date`/`--date=` accepted; invalid dates rejected (`2026-8-4`, `2026-02-31`, `04/08/2026`); `--date` on non-`regenerate` commands rejected (D6); bare `regenerate` (no file) rejected.
- [x] 3.2 GREEN: add `date string` to `cliOptions`; `--date`/`--date=` cases in `parseArgs`; `parseDate(value)` beside `parsePort` (D2); extend the `--all`/`--dry-run` guard with `opts.date != "" && opts.command != "regenerate"`.
- [x] 3.3 RED: `TestRunRegenerate*` — mock happy path (overwrite / new-note-with-`--date`), missing file exits 1, missing `--date`+no prior note fails with no file, lemur-only API-key check (D8), topics/index/`processed.json` byte-identical via `run([]string{...})`.
- [x] 3.4 GREEN: `case "regenerate"` in `run()`; `runRegenerate(opts)` mirroring `runPipeline`'s exit-code shape; mock/real `analyzeFn` selection (D9).
- [x] 3.5 Update `usage` + package doc for `regenerate`; update `"--mock is only valid with serve or process"` to include `regenerate`.
- [x] 3.6 Verify: `go test ./cmd/patro/...`, `go build ./...`.

## Phase 4: Full verification

- [x] 4.1 `go vet ./...` && `go test ./...` — zero regressions repo-wide.

**Note**: bare-transcript Language/Chapters degradation (spec requirement) needs no task — `WriteMeetingNote`/`WriteMeetingNoteAt` already emit empty `Language` and omit `## Chapters` when absent; satisfied by construction.
