# Design: `patro regenerate <transcript-file>`

## Technical Approach

A new `regenerate` arm in `cmd/patro/main.go` delegates to one new pipeline
function, `pipeline.Regenerate`, which reuses `runPipeline`'s analyzer
selection but replaces transcription with a file read and replaces
`AddMeetingCtx` with a direct meeting-note write. Two small, testable
helpers are added to `internal/library` (transcript-ID resolution and
prior-note lookup) so `cmd/patro` stays a thin dispatcher.

## Invariants (non-negotiable)

- No code path added by this change reads or writes `knowledge/topics/**`
  or `knowledge/index.md`. `AddMeetingCtx`, `AppendTopicSectionAnnotated`,
  `RebuildIndex`, `ExistingTopics`, `ExistingTopicsRecent` are never called.
- `existing []types.TopicRef` passed to `AnalyzeFunc` is always literal `nil`.
- `lib.Reconciler` is left nil, so no embedding/reconciliation can occur.
- `Regenerate` takes no `*state.State` and no `*status.Tracker` parameter —
  bypass is structural, not a nil-argument convention.

## Architecture Decisions

| # | Decision | Choice | Rejected | Rationale |
|---|---|---|---|---|
| D1 | External transcript ID | `"ext-" + hex(sha256(file bytes))[:12]`, via `Library.ResolveTranscriptID`; filename stem when the file already sits in `TranscriptsDir` | filename slug; timestamp/UUID; sha1 (`MockTranscribe`'s scheme) | Content hash is deterministic: re-running on identical bytes yields the same ID, so the prior-note lookup hits and no duplicate is minted. Slug collides across directories; random IDs break idempotency. sha256 avoids `MockTranscribe`'s `//nolint:gosec` (no Python-parity constraint here). |
| D2 | `--date` validation | `time.Parse("2006-01-02", v)` **plus** round-trip `t.Format(...) == v` in a `parseDate` helper next to `parsePort` | lenient parse; multiple layouts; default to today | Rejects `2026-8-4`, `2026-02-31`, `04/08/2026`. Error surfaces from `parseArgs` exactly like `parsePort`: `run` prints `patro: %v` + usage, **exit 2**. Message: `invalid --date %q: must be in YYYY-MM-DD format`. |
| D3 | Prior-note lookup | `Library.FindMeetingNoteByTranscriptID(id) (*PriorNote, error)`; `(nil, nil)` = not found | inline scanning in `cmd`; a persisted ID→note index | Filenames encode date+slug, not ID, so a scan is required; a package-level function is table-testable and keeps the link format co-located with the writer. |
| D4 | Exact-path overwrite | Extract `WriteMeetingNoteAt(path, ...)`; `WriteMeetingNote` computes `date+"-"+Slugify(title)` then delegates | write-then-`os.Rename`; write-then-delete-old | Signature/behavior of `WriteMeetingNote` is unchanged for `AddMeetingCtx`; one atomic write, never two files on disk. |
| D5 | Copy placement | Copy external transcript **after** date resolution, **before** analysis | copy after analysis | Fails fast on an unwritable `transcripts/` before an expensive LLM/LeMUR call, and the note's `../transcripts/<id>.txt` target already exists when the note is written. A failed analysis leaves at worst an orphan transcript that the next run rewrites byte-identically (hash is of the *source* file). |
| D6 | `--date` on other commands | Rejected: `--date` outside `regenerate` exits 2 | silently ignore | Mirrors the existing `(opts.all \|\| opts.dryRun) && opts.command != "reconcile"` guard (main.go:123). |
| D7 | Source-video provenance | `PriorNote.SourceVideo` is reused when present; else the transcript path is passed | always pass the transcript path | Otherwise regenerating an existing note silently rewrites `- **Source video:** \`meeting.mkv\`` to `\`<id>.txt\``, losing provenance. Small addition to the lookup's contract. |
| D8 | API-key pre-check | Only when `cfg.AnalyzerBackend == "lemur"` (exit 2) | unconditional check as in `runPipeline` | `regenerate` never uploads; requiring a key would block offline kimi/claude runs and the e2e suite. |
| D9 | `--mock` | `analyzeFn = pipeline.MockAnalyze`, identical to `runPipeline`; `MockTranscribe` unused | new mock | Zero new branching; no subprocess, no network. |

**Malformed/missing date line (D3 edge case)**: the note is still reported as
found with `Date: ""` and **no error** — never a panic. The caller then
requires `--date`; if that is also absent it fails with
`cannot determine date for transcript %q: prior note %s has no valid "- **Date:**" line; pass --date YYYY-MM-DD`.
The write target remains the prior note's exact path.

## Data Flow

    cmd: parseArgs(--date) ─→ runRegenerate ─→ pipeline.Regenerate
                                                    │
      ResolveTranscriptID ──→ FindMeetingNoteByTranscriptID ──→ resolve date
                                                    │ (fail fast if unresolvable)
                            WriteTranscript (external only)
                                                    │
                              af(ctx, t, nil)  ← MockAnalyze | MakeAnalyzeFunc(cfg)
                                                    │
                 WriteMeetingNoteAt(prior.Path) | WriteMeetingNote(date+slug)

    topics/ · index.md · .state/processed.json ── never opened

## Interfaces / Contracts

```go
// internal/library
func (l *Library) ResolveTranscriptID(path string) (id string, external bool, err error)

type PriorNote struct{ Path, Date, SourceVideo string } // Date "" = unparsable

func (l *Library) FindMeetingNoteByTranscriptID(id string) (*PriorNote, error)
func (l *Library) WriteMeetingNoteAt(path string, t *types.TranscriptResult,
    a *types.AnalysisResult, videoPath, date string) (string, error)
func transcriptLink(id string) string // "[transcript](../transcripts/<id>.txt)"

// internal/pipeline/regenerate.go
func Regenerate(ctx context.Context, transcriptPath, date string,
    cfg *config.Config, af AnalyzeFunc) (notePath string, err error)

// cmd/patro/main.go
type cliOptions struct{ /* ...existing... */ date string }
func parseDate(value string) (string, error)
func runRegenerate(opts *cliOptions) int
```

`FindMeetingNoteByTranscriptID` globs `MeetingsDir/*.md` (sorted, first
match wins), matches the literal substring returned by `transcriptLink(id)`
— shared with `WriteMeetingNote` line 198 so the format cannot drift — then
scans for the `- **Date:** ` and `- **Source video:** ` prefixes. Unreadable
files are skipped, not fatal.

`runRegenerate` mirrors `runPipeline`: empty `opts.file` → `patro:
regenerate requires a transcript file` (exit 2); `config.Load` → exit 1;
`logging.Init`; `setup.ExpandPath` + `os.Stat` → exit 1; `signal.NotifyContext`;
`pipeline.Regenerate` error → `logging.Errorf` + exit 1; else 0.

## File Changes

| File | Action | Description |
|---|---|---|
| `internal/pipeline/regenerate.go` | Create | `Regenerate` |
| `internal/pipeline/regenerate_test.go` | Create | Table tests incl. negative topics/index/state assertions |
| `internal/library/library.go` | Modify | `ResolveTranscriptID`, `FindMeetingNoteByTranscriptID`, `PriorNote`, `WriteMeetingNoteAt`, `transcriptLink` |
| `internal/library/library_test.go` | Modify | ID resolution + lookup tables |
| `cmd/patro/main.go` | Modify | `date` field, `--date`/`--date=`, `parseDate`, guard, `case "regenerate"`, `runRegenerate`, usage + package doc, `--mock` message updated to "serve, process or regenerate" |
| `cmd/patro/main_test.go` | Modify | `TestParseArgs` `--date` cases, `TestRunRegenerate*` |

## Testing Strategy

| Layer | What | How |
|---|---|---|
| Unit | `parseDate` valid/invalid/round-trip | Table test, `cmd/patro` |
| Unit | `ResolveTranscriptID` in-library vs external, stable hash | `t.TempDir()` |
| Unit | Lookup: found / not found / malformed date line / unreadable file | `t.TempDir()` fixtures |
| Unit | `WriteMeetingNoteAt` overwrites exact path on title drift | `t.TempDir()` |
| Integration | `Regenerate` happy path, external copy, missing-date failure, `existing == nil` (spy `AnalyzeFunc`) | `pipeline_test.go` style helpers |
| Integration | Topics/index/`processed.json` byte-identical before/after | Hash-compare in `TestRunRegenerate*` via `run([]string{...})` |

## Threat Matrix

| Boundary | Applicability |
|---|---|
| Documentation-like paths | N/A — inputs are read as text, never executed or classified |
| Git repository selection | N/A — no VCS interaction |
| Commit state | N/A |
| Push state | N/A |
| PR commands | N/A |

CLI routing changes are covered by `TestParseArgs` and the misplaced-`--date`
rejection test. The only subprocess boundary (`analyzer.AnalyzeCLI` via
`MakeAnalyzeFunc`) is reused unchanged, with no new argument composition.

## Migration / Rollout

No migration required. Additive command; no config, schema, or on-disk
format change.

## Open Questions

- [ ] None blocking. D4 and D7 add exported surface to `internal/library`
      beyond the proposal's "reused unchanged" wording — additive only, with
      `WriteMeetingNote`'s existing behavior preserved.
