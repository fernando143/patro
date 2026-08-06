# Proposal: `patro regenerate <transcript-file>`

## Intent

Re-running the analyzer over an already-transcribed meeting today requires the full pipeline: it re-uploads to AssemblyAI (cost + latency) and unconditionally mutates topics and `index.md` through `AddMeetingCtx`. Two real needs are unserved: (1) after switching `analyzer_backend`, users want a fresh meeting note **without** disturbing knowledge already merged into topics; (2) the scaffolded `test/e2e/` Suite 1 needs a deterministic entry point that drives the configured analyzer CLI against fixture transcripts and evaluates only the meeting note.

## Scope

### In Scope
- New `regenerate` arm in `run()` (`cmd/patro/main.go`), sibling to `serve`/`process`/`reconcile`/`run`; `--date YYYY-MM-DD` added to `cliOptions`/`parseArgs`; usage + package doc updated.
- New one-shot pipeline function: read transcript into `types.TranscriptResult{ID, Text}`, run configured (or mock) analyzer, call `library.WriteMeetingNote` directly. Overwrites the meeting note only.
- Transcript ID = filename stem when the file is already under `knowledge/transcripts/`; a new ID is generated for an external file.
- External transcripts are copied to `knowledge/transcripts/<id>.txt` unconditionally — no prompt, no gating flag — so the note's `../transcripts/<id>.txt` link resolves (`library.go:198`) and the command stays scriptable/non-interactive.
- One lookup finds the prior meeting note for the transcript ID and drives two decisions: date reuse and target path.
  - **Date**: reuse that note's `- **Date:**`; otherwise `--date` is required; otherwise fail with a clear error — never default silently to today.
  - **Target path**: overwrite that exact file. A fresh `date + "-" + Slugify(title)` filename is computed **only** when no prior note exists. Title drift between runs therefore never produces an orphaned or duplicate meeting file — always exactly one write.
- `--mock` behaves like `process --mock`; status tracker is `nil`; `.state/processed.json` is neither read nor written.
- Tests mirroring `main_test.go` / `pipeline_test.go` patterns, including negative assertions that topics and `index.md` are untouched.

### Out of Scope
- Any read or write of `knowledge/topics/*.md` or `index.md`. `AddMeetingCtx`, `AppendTopicSectionAnnotated`, `RebuildIndex` MUST NOT be called. Already-merged topic content stays as-is, permanently, and the command emits no topics-staleness warning — it never inspects topic files.
- Recovering `Language` / `Chapters` for a bare transcript. Accepted degradation (verified non-crashing): empty language line, no chapters section.
- Sidecar transcript metadata, batch/`--all` regeneration, TUI or web surfacing, state dedup, reconcile interaction.

## Capabilities

### New Capabilities
- `meeting-note-regeneration`: analyzer-only regeneration of a single meeting note from a transcript file, with topics/index left untouched.

### Modified Capabilities
None. The analyzer JSON contract and `library.WriteMeetingNote` are reused unchanged.

## Approach

| Decision | Rationale |
|---|---|
| Own `runRegenerate` arm, not folded into `runPipeline` | `ProcessVideo`'s only library entry point is `AddMeetingCtx`, which always writes topics + index |
| New pipeline-level function, not inline in `cmd` | Keeps `cmd/patro` a thin dispatcher; enables table-driven tests like `ProcessVideo` |
| Reuse `runPipeline`'s mock/real analyzer selection | Same `--mock` semantics, zero new branching in the analyzer layer |
| Bypass `state` | Dedup is a video-upload/billing concern; re-running regenerate must always regenerate |
| Copy external transcripts into the library, unconditionally | Prevents a broken relative link, makes library state self-consistent, and keeps the command usable from scripts and the e2e suite |
| Overwrite the prior note's exact path when one exists | Title drift would otherwise create a second file under a new date+slug; one lookup serves both date reuse and target path |
| Never read topic files | Enforces the topics-frozen rule structurally, not just by omission of writes |

## Affected Areas

| Area | Impact | Description |
|---|---|---|
| `cmd/patro/main.go` | Modified | `regenerate` case, `--date` flag, usage/doc |
| `internal/pipeline` | Modified | New regenerate function + tests |
| `internal/library` | Read-only reuse | `WriteMeetingNote` called directly |
| `cmd/patro/main_test.go`, `internal/pipeline/pipeline_test.go` | Modified | New suites incl. negative topic/index assertions |

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Undecided ID generation for external transcripts | High | Design phase picks a deterministic scheme (e.g. content hash) |
| `--date` format not validated; a bad date names a first-time note wrongly | Medium | `parseArgs` validates `YYYY-MM-DD`; design defines the error |
| Prior-note lookup must resolve an ID from meeting files (filenames are date+slug, not ID) | Medium | Design specifies the lookup — scan `knowledge/meetings/*.md` for the `../transcripts/<id>.txt` link — since both date reuse and overwrite-target depend on it |
| `--date` accepted on unrelated commands | Low | Mirror the existing `--all`/`--dry-run` command guard |

## Rollback Plan

Additive command. Revert the commit (or ship a binary without the arm); no schema, config, or on-disk format changes. Meeting notes written by a bad run are restored by re-running `regenerate` or from git/backups; topics and `index.md` were never touched.

## Dependencies

- Configured `analyzer_backend` CLI/API for non-`--mock` runs.

## Success Criteria

- [ ] `patro regenerate knowledge/transcripts/<id>.txt --mock` rewrites exactly one meeting note and leaves `topics/` and `index.md` byte-identical.
- [ ] Re-running with an analyzer that produces a different title overwrites the same file path — the meeting count is unchanged and no orphan note appears.
- [ ] An external transcript is copied to `knowledge/transcripts/<id>.txt` with no prompt, and the note's transcript link resolves.
- [ ] Date is reused from an existing note; missing note + missing `--date` exits non-zero with a clear message.
- [ ] `.state/processed.json` is unchanged after any regenerate run.
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass.
