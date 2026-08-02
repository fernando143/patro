# Proposal: Semantic topic reconciliation + searchable knowledge library

**Status**: product questions ANSWERED and locked (round 1). One UI-surface sub-question deferred to `sdd-design`.

## Intent

Every meeting spawns a new topic file. Root cause (verified): `AppendTopicSection` (internal/library/library.go:208) matches by **exact slug string** only — 100% of reuse logic is delegated to a non-deterministic LLM prompt hint. Any synonym, typo, or backend swap fragments the namespace silently. Compounding: the full existing-topics list is dumped unbounded into every prompt, and `internal/web` has no search — so a fragmented library is also unnavigable. Goal: patro becomes a durable note-keeper whose topics converge instead of multiply, self-contained **after install** (no post-install network, no cgo, no new secret).

## Locked product decisions

| # | Decision | Consequence |
|---|---|---|
| 1 | Auto-merges are **traceable, never silent** | Every `>0.90` merge records the original LLM-proposed slug/name (section annotation and/or `.state/` audit trail) so wrong merges are detectable and reversible |
| 2 | Model weights **embedded at build time** (`go:embed` or equivalent); no first-run download | Larger binary accepted. Rationale: brew/GitHub already need network to install patro; the real constraint is zero network *after* install |
| 3 | Gray-zone LLM failure/timeout ⇒ **create a new topic** (safe), flagged `needs-reconciliation` in `.state/` | Never auto-merge on failure. User manually triggers reconciliation for flagged topics from the TUI |
| 4 | First run: build index **silently** AND report suspected duplicate topics for review | Surface for that review (TUI vs web) is **an open design question**, not decided here |
| 5 | Similarity thresholds **configurable from TUI Settings**, not just `config.yaml` | New fields in `internal/tui/settings.go` + `internal/setup` |
| 6 | `onnxruntime-purego` **ruled out** | See Alternatives rejected |

## Scope

### In Scope
- `internal/library`: reconcile candidates in `AddMeeting` **before** `AppendTopicSection` — embed name+content, kNN-query existing topics, then `>0.90` auto-merge (annotated), `<0.70` new topic, gray zone ⇒ one LLM "same as X?" call via an injected reconciler (pipeline's injected-function pattern).
- New `internal/embed`: genuinely pure-Go, no-cgo, no-external-shared-library inference (target: zerfoo or equivalent) with build-time-embedded weights.
- New `internal/searchindex`: bleve (pure Go, BM25 + HNSW kNN) at `.state/search-index/`. **Derived artifact, not source of truth** — `Rebuild()` walks `topics/*.md` + `meetings/*.md` for migration and corruption recovery.
- `internal/analyzer` `BuildPrompt`: cap existing-topics by recency (`topicInfo` already extracts lastUpdate).
- `internal/web`: `/search` endpoint, hybrid BM25+kNN, rendered via existing goldmark pipeline.
- **`internal/tui` (new scope, from decisions 3/4/5)**: manual "reconcile flagged topics" action + flagged-count surfacing; threshold fields in Settings.
- Merge audit trail + `needs-reconciliation` flag persistence under `.state/`.

### Out of Scope
- Topic file compaction/summarization — append-only growth stays; deferred follow-up.
- Automatic retroactive merging of already-fragmented topics (report for review only; no bulk auto-merge).
- Topic hierarchy/tags.

## Capabilities

### New Capabilities
- `topic-reconciliation`: threshold merge-vs-new decision at ingestion, with audit trail and safe-failure flagging.
- `local-embeddings`: in-binary vector generation, no post-install network, no secret.
- `search-index`: derived bleve index with rebuild-from-markdown.
- `library-search`: `/search` in the web viewer.
- `reconciliation-review`: manual re-reconciliation of flagged/duplicate topics from the TUI.

### Modified Capabilities
- `meeting-analysis`: existing-topics prompt block bounded by recency (analyzer JSON contract unchanged).
- `settings-management`: TUI Settings gains similarity-threshold configuration.

## Approach

Insert a reconciliation seam between analysis and persistence. `AddMeeting` gains an optional reconciler dependency (nil = today's exact-slug behavior, keeping `process --mock` and existing tests green). Embedding + index are additive packages; markdown stays authoritative so any index failure degrades to current behavior rather than data loss. Reconciliation always fails **toward** a new flagged topic, never toward a silent merge — fragmentation is recoverable, a wrong merge is not.

Deviation from exploration: exploration recommended hand-rolled fuzzy matching over embeddings on dependency-light grounds. User selected embeddings + bleve for true synonym coverage plus search, accepting the added dependency weight and binary size.

## Alternatives rejected

- **`onnxruntime-purego`** — verified: `libonnxruntime.so`/`.dylib` ships by default on neither Linux nor macOS. macOS needs a separate `brew install onnxruntime`; Linux has no standard system package. That means an extra install step, a per-platform native shared library, and version drift between what patro expects and what is installed — breaking today's `brew install patro && patro init` single-static-binary model. Excluded as a candidate.
- **First-run model download** — rejected per decision 2; post-install network is the constraint that matters.
- **Hand-rolled fuzzy/edit-distance only** — misses true synonyms and delivers no search capability.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `internal/library/library.go` | Modified | Reconciler seam in `AddMeeting` (:293); `AppendTopicSection` (:208) takes the resolved slug + merge annotation |
| `internal/embed/` | New | Pure-Go embedding wrapper, weights embedded at build time |
| `internal/searchindex/` | New | bleve index + `Rebuild()` |
| `internal/analyzer/analyzer.go` | Modified | Recency-capped topics block in `BuildPrompt` (:59) |
| `internal/web/` | Modified | `/search` handler + template |
| `internal/tui/settings.go` | Modified | Threshold fields — must extend `settingsValues` (:93, pointer-bound per the documented Bubble Tea gotcha) and the `settingsStep` chain (:58) |
| `internal/tui/dashboard.go` | Modified | New reconcile action; `handleKey` (:142) already binds q/esc/f/r/w/o/tab/↑↓/enter — needs a free key |
| `internal/tui/data.go` | Modified | Load flagged/duplicate counts into `dashboardData` |
| `internal/setup/config.go` | Modified | `Values` (:12) gains thresholds; `WriteConfig` already preserves unknown keys |
| `internal/config/` | Modified | Thresholds, topic cap, index path |
| `internal/pipeline/pipeline.go` | Modified | Wire reconciler + index update (:147, :152) |
| `.state/` | New files | Merge audit trail + `needs-reconciliation` flags |
| `.goreleaser.yaml` | Verify | CGO_ENABLED=0 across darwin/linux × amd64/arm64 with embedded weights |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Embedded weights inflate every release artifact (4 platforms) | High → **accepted** | Decision 2; prefer the smallest model meeting quality bar; measure in design |
| No pure-Go inference path meets quality/perf bar (zerfoo maturity) | Med-High | **Design-phase spike required** — this is the main remaining technical unknown after ruling out onnxruntime-purego |
| Wrong-merge corrupts a topic | Med → reduced | Conservative 0.90 threshold + audit trail (decision 1) + markdown is authoritative and hand-editable |
| Gray-zone LLM call adds latency/cost | Med | Only for the 0.70–0.90 band; batch candidates in one call |
| Index drift vs markdown | Med | `Rebuild()` + rebuild on schema/version mismatch |
| Flagged topics accumulate unnoticed | Med | Surface count in TUI dashboard; manual reconcile action |
| TUI scope creep (settings step chain + new key binding) | Med | Own PR slice; `settingsValues` pointer + per-step form rules are documented constraints, not discoveries |
| Dependency weight vs CLAUDE.md philosophy | High → accepted | Explicit tradeoff; pipeline path stays optional/nil-able |
| 400-line PR budget | High | 6-slice chain (below) |

## Delivery / PR chain

TUI work adds a 6th slice: `internal/embed` (+ weights) → `internal/searchindex` → library reconciliation seam (+ audit trail + flags) → analyzer prompt cap → web `/search` → TUI (reconcile action + threshold settings). Slice 1 is unusually large due to embedded weights; treat generated/vendored weight blobs as non-authored lines.

## Rollback Plan

Per-slice revert. The reconciler is nil-able: setting it to nil (or a config flag) restores exact-slug matching with zero data migration. `.state/search-index/`, the audit trail, and the flag file are all deletable derived state — regenerated by `Rebuild()`, never read as source of truth. Merge annotations live in markdown the user can hand-edit. No knowledge-library format change requiring forward migration.

## Dependencies

- bleve (pure Go, no cgo).
- A genuinely pure-Go embedding runtime + embeddable weights — candidate zerfoo; **final choice still requires a design-phase spike**. `onnxruntime-purego` excluded.
- No new secrets; `ASSEMBLYAI_API_KEY` remains the only one.

> **Note (post-design amendment)**: the design-phase spike identified 3 embedding candidates (`go-sentex`, `zerfoo`, `cybertron`). Implementation (Unit 1b) found `go-sentex` requires a network download of model weights on first call, incompatible with decision 2 above — dropped. **2 candidates ship**: `zerfoo`, `cybertron`. See design D9 amendment (revision 4) for the full finding and rationale.

## Open questions for sdd-design

1. **Where do suspected duplicates surface for review — TUI or web?** (decision 4, deliberately deferred)
2. Which pure-Go inference library + model actually meets the quality/size/perf bar.
3. Audit-trail shape: annotation inside the topic markdown, a `.state/` ledger, or both.
4. Free key binding for the TUI reconcile action (q/esc/f/r/w/o/tab/↑↓/enter are taken).
5. Whether thresholds are global or per-library.

## Success Criteria

- [ ] Two meetings on the same subject with different LLM slugs land in one topic file.
- [ ] Every auto-merge is traceable to the original LLM-proposed slug.
- [ ] Gray-zone failure produces a new flagged topic, never an auto-merge.
- [ ] Flagged topics are visible and manually reconcilable from the TUI.
- [ ] Thresholds are editable from TUI Settings and persisted.
- [ ] `CGO_ENABLED=0` build succeeds for darwin/linux × amd64/arm64 with embedded weights, and the binary runs with **no network and no extra install step**.
- [ ] Existing `library_test.go` / `analyzer_test.go` contracts pass or are updated in lockstep.
- [ ] Analyzer prompt size stays bounded as topic count grows.
- [ ] `/search` returns hits across topics and meetings.
- [ ] `Rebuild()` reconstructs a working index from markdown alone on an existing library.
- [ ] No new secret introduced.
