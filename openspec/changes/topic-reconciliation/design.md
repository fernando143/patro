# Design: Semantic topic reconciliation + searchable library

*Revision 4 — drops `go-sentex` from the embedding registry (amends D9); ships 2 backends (`zerfoo`, `cybertron`) instead of 3. Finding surfaced during implementation (Unit 1b apply), not at design time — see D9 amendment below. Revision 3 added rebuild progress reporting (D12) and rebuild trigger/concurrency semantics to D10. Revision 2 resolved the embedding-runtime decision (3-backend registry) and added `tools/embedbench`.*

## Spike outcome (closed)

| Question | Resolution |
|---|---|
| bleve pure-Go kNN? | **No.** bleve's kNN is `blevesearch/go-faiss` — **cgo**, behind the `vectors` build tag; compiles out under `CGO_ENABLED=0`. BM25 (v2.4+) *is* pure Go and CGO-0 safe. Resolved by D2/D3. |
| Pure-Go embedding runtime? | **Resolved by user (live-verified) at design time, amended during implementation (revision 4).** Design-time spike identified three viable candidates: `edgetools/go-sentex` (MiniLM-L6-v2, 384-dim unit-norm, zero deps; *maturity unverified*), `zerfoo/zerfoo` (GGUF parser + inference "Stability: stable", documented L2-normalized embeddings + similarity), `nlpodyssey/cybertron`+`spago` (BERT/MiniLM, adoption via `tmc/langchaingo/embeddings/cybertron`). Implementation (Unit 1b) found `go-sentex` downloads ~87MB of weights from HuggingFace Hub over `net/http` on first `LoadModel()` call with no way to inject local/embedded bytes — incompatible with decision 2 (zero network after install). **Dropped.** Ships `zerfoo` + `cybertron` only — see D9 amendment. |

## Architecture Decisions

**D1 — Reconciler seam via optional interface + `Ctx` twin methods.** `Library` gains an optional `Reconciler` field (nil ⇒ today's exact-slug behavior). Add `AddMeetingCtx(ctx,…)` / `AppendTopicSectionAnnotated(…,annotation)`; existing `AddMeeting` / `AppendTopicSection` delegate (`context.Background()`, `""`). *Rejected*: changing existing signatures — breaks `library_test.go`. *Rationale*: `…WithContext` precedent; zero test churn, nil-able rollback.

**D2 — kNN is a hand-rolled flat cosine store (`internal/vectors`), not bleve.** 384-dim brute force over hundreds–low thousands of topics is sub-millisecond; HNSW buys nothing at this scale. All three backends return L2-normalized vectors, so **cosine = dot product**. *Rejected*: bleve kNN (cgo kills CGO-0 cross-compile), a pure-Go HNSW dep. *Rationale*: removes the only cgo blocker and honours dependency-light.

**D3 — bleve for BM25 full-text only**, no `vectors` tag. Hybrid `/search` fuses BM25 + cosine ranks via reciprocal-rank fusion (~15 lines, no dep). *Rejected*: linear grep-scan (no ranking/stemming).

**D4 — Audit trail: BOTH markdown and `.state`.** Markdown annotation (`*Merged from proposed slug `x-y` — cosine 0.93*`) is human-facing, hand-editable, authoritative, survives `.state` deletion. `.state/reconciliation.json` is the derived, deletable ledger the TUI reads. Resolves proposal Q3.

**D5 — Duplicate review lives in the TUI only; web gains read-only `/search`.** *Rejected*: web review UI — `internal/web` is documented read-only, no JS, no external assets. Resolves Q1.

**D6 — Recency cap at the pipeline call site**, not inside `BuildPrompt`. New `Library.ExistingTopicsRecent(n)` sorts by `topicInfo` lastUpdate desc; `pipeline.go:147` passes it. `BuildPrompt` stays a pure formatter — `analyzer_test.go` and the analyzer JSON contract untouched.

**D7 — Thresholds global in `config.yaml`** (`merge_threshold: 0.90`, `new_topic_threshold: 0.70`, `topic_prompt_limit: 50`). One config already equals one library root. Resolves Q5. Values provisional — **calibrated with `tools/embedbench`**.

**D8 — TUI maintenance key = `m`.** Free against `q/ctrl+c/esc/f/r/w/o/tab/↑k/↓j/enter` and viewport defaults. Spawns `patro reconcile` detached, mirroring `openWeb`/`retrySelected`. Resolves Q4.

**D9 — `internal/embed` is a registry, all compiled-in backends selected by config.** `Available() []string`, `New(name) (Embedder, error)`; `Embedder` = `Embed(ctx,string) ([]float32,error)`, `Dim() int`, `Name() string`. New config key `embedding_backend` mirroring `analyzer_backend` exactly (`yamlConfig.EmbeddingBackend *string`, `validEmbeddingBackends`, `ValidEmbeddingBackends()` beside `ValidAnalyzerBackends()` at `config.go:52`). Registry is an **explicit table**, not `init()` registration — matches `validAnalyzerBackends` (`config.go:33`) and `backendChoices` (`tui/settings.go`). *Rejected*: single vendored backend, per-backend build tags. Provisional default `cybertron` (only candidate with third-party adoption evidence); embedbench settles it.

> **D9 amendment (revision 4) — `go-sentex` dropped, 2 backends ship, not 3.** During Unit 1b implementation, `sentex.LoadModel()` was found to have no build-time weight-embedding path: it downloads `model.onnx` (~87MB) + `tokenizer.json` (~700KB) from HuggingFace Hub over `net/http` on first call, into `$HF_HOME` or `os.UserCacheDir()+"/huggingface"`, with no exported way to inject pre-loaded bytes, a reader, or a local path (`LoadModel()` takes zero arguments). This directly violates locked decision 2 (weights embedded at build time, zero network after install) and, as a secondary issue, the only compliant workaround (pre-populating the HF cache path from our own embedded copy) would require writing outside `knowledge/`/`.state/`, which is itself a project-wide invariant (CLAUDE.md: "writes stay confined to the knowledge library and .state directories") — not something to freelance inside one adapter. `go-sentex`'s transitive dependency footprint (~15 extra modules: `gomlx`, `gomlx/onnx-gomlx`, `sugarme/tokenizer`, `google.golang.org/protobuf`, `k8s.io/klog/v2`, etc.) also undercuts the design spike's "zero deps" characterization, though that alone would not have been disqualifying. This is exactly the scenario D9's registry was built to absorb: dropping one adapter costs nothing at the interface/caller level. **Decision: ship `zerfoo` + `cybertron` only.** `edgetools/go-sentex` is not vendored, not wrapped, and not registered. No workaround (HF cache priming, `HF_HOME` override) was attempted — that would be new, unscoped design work, not a mechanical fix. Every other D9 clause (explicit table, config key shape, provisional default) is unchanged.

**D10 — The vector store is backend-tagged, self-invalidating, and rebuilt out of band.** Vectors from different backends are **not** interchangeable — same 384 dims, different vector spaces. `.state/vectors/topics.json` persists `{backend, dim, model_version}`; any mismatch invalidates the store and schedules `Rebuild()`.

- **Triggers: serve startup + on demand. Never mid-pipeline.** `serve` already scans the inbox at startup; the store integrity check is symmetric and `serve` is the only long-lived process owning a `Tracker`. On demand = `patro reconcile` (D8), needed for first-run migration on an existing library when no service is running, and for corruption recovery. If the reconciler meets a mismatch *mid-meeting* it must **not** rebuild inline: it fails safe to a new flagged topic, which is already the locked decision-3 rule — no new failure mode.
- **Rebuild does not block ingestion, but does disable reconciliation while it runs.** The watcher keeps queueing and the pipeline keeps processing; only reconciliation degrades. Enforced *inside the store*, not by scattered caller checks: `vectors.Nearest()` returns `ErrRebuilding` while a rebuild is in flight, and the reconciler maps that to the same fail-toward-new-topic-plus-flag path. Rebuild is single-flight — a second trigger while one runs is a no-op. *Rejected*: pausing the watcher (a fresh recording would sit unprocessed during a long first-run migration); letting the pipeline query a half-built store (would produce wrong nearest-topic answers and risk exactly the wrong auto-merge this design exists to prevent — "fragmentation is recoverable, a wrong merge is not").
- Meetings processed during a rebuild are flagged, so the reconcile pass that follows the rebuild picks them up. Recovery is already-designed behavior, not new machinery.

**D11 — `tools/embedbench` is a nested, non-shipping Go module.** See section below.

**D12 — Progress is a new sibling field on the existing `status.Snapshot`, not a second mechanism and not a reuse of `Job`.** New `*Maintenance` alongside `Current`, plus nil-safe `Tracker` methods `MaintenanceStart(phase, total)`, `MaintenanceProgress(done)`, `MaintenanceDone()`.

    type MaintenancePhase string // "rebuilding-index" | "reconciling"
    type Maintenance struct {
        Phase     MaintenancePhase `json:"phase"`
        Done      int              `json:"done"`
        Total     int              `json:"total"`
        StartedAt time.Time        `json:"started_at"`
    }

*Rejected — reusing `Job`/`Stage`*: a rebuild is not a file job. It has no `File`, is not in `Queue`, and runs when `Current == nil`; `Tracker.Stage` (`status.go:140`) is explicitly a **no-op when `Current == nil`**, so it cannot carry this without faking a `Job` and polluting the dashboard's in-flight-video card. Per D10 the two must also be able to display *simultaneously*, which settles it: sibling field, distinct card.

*Rejected — a separate progress file/mechanism*: the decisive factor is staleness. `data.go:68` liveness-checks `snap.PID` and clears `Current`/`Queue` when the writing process is gone. Rebuild progress is exactly the state that must not survive its writer — a separate file would have to duplicate that PID policy, and getting it wrong leaves a dashboard permanently stuck at "rebuilding 40/500". Living in `Snapshot` inherits stale detection, the atomic temp+rename flush, and the existing 1s `loadData` read path for free.

*New behavior that needs explicit care*: every current `Tracker` method flushes unconditionally, but per-topic progress would mean N temp-file+rename cycles for N topics. `MaintenanceProgress` therefore updates in memory and **flushes throttled** (on ≥1% change or ≥250 ms elapsed, whichever is coarser); start and done flush unconditionally. The dashboard only polls at 1 s, so nothing is lost. Tracker's swappable `now func() time.Time` (`status.go:77`) already provides the test seam for the time-based throttle.

*Layering*: `internal/vectors` must not import `internal/status` (D2 keeps it dependency-free). `Rebuild(ctx, src, onProgress func(done, total int))` takes a callback; `serve`/`cmd` adapts it to the Tracker.

## Data Flow

    analyzer ──candidate topics──► Library.AddMeetingCtx
                                        │
                                        ▼
                                   Reconciler
       embed.New(cfg.EmbeddingBackend).Embed(name+content) ──► vectors.Nearest(v,k)
                                        │                          │
              ≥0.90 ──► merge (annotate) ─┤              ErrRebuilding ⇒ fail safe
              <0.70 ──► new topic ────────┤──► AppendTopicSectionAnnotated
              gray  ──► 1 LLM call ───────┘         │
                        (err/timeout ⇒ NEW + flag)   ▼
                                            markdown (truth)
                                                    │
                          .state/reconciliation.json + vectors + bleve (derived)

    serve startup / `patro reconcile`
        └─► vectors.Rebuild(ctx, src, onProgress) ──► Tracker.Maintenance* ──► status.json ──► dashboard (1s tick)

## `tools/embedbench` — internal dev tool, explicitly NOT shipped

**Structure.** Separate nested module `tools/embedbench/go.mod`:

    module github.com/fernando143/patro/tools/embedbench
    require github.com/fernando143/patro v0.0.0
    replace github.com/fernando143/patro => ../..

**Isolation is automatic and verified against this repo**: the go command skips subdirectories containing their own `go.mod`, so `tools/embedbench` is excluded from the root module's `go build/vet/test ./...` — exactly what `.github/workflows/ci.yml` runs (vet, build, test+Codecov) — and `.goreleaser.yaml` builds only `./cmd/patro`. No release, CI, or coverage surface; no CI job added by default.

**The `internal/` import is legal** — `cmd/go` applies the internal rule to module code by **import-path prefix**: `parentOfInternal` = `github.com/fernando143/patro`, and the importer `…/tools/embedbench` sits under that prefix, so importing `…/internal/embed` is permitted despite the module boundary. Subtle and load-bearing: **tasks must compile-verify it first, before any server work.**

**Reuse, not reimplementation.** Imports the real production registry and iterates `embed.Available()` / `embed.New(name)`. Zero embedding logic of its own — that is the point.

**Server.** Mirrors `internal/web` rather than inventing a pattern: a `Server` struct, one inlined `template.Must(...)` page, no external assets or JS, `127.0.0.1:<port>` bind, graceful shutdown on interrupt (same shape as `run web` in `cmd/patro/main.go`).

**Function.** Form takes text A and optional B. Per backend: `Dim()`, embed wall time, `cos(A,B)`, plus a cross-backend agreement matrix. Answers "would backend X auto-merge these two topics?" — the instrument that sets D7's thresholds and D9's default.

## TUI surface

One user-facing maintenance action, not two. Rebuild and reconciliation are causally chained — flagged topics cannot be reconciled without a valid vector store — so `m` runs a single `patro reconcile` that internally does *ensure-index (rebuild if missing/mismatched) → reconcile flagged topics*, publishing both through `Maintenance.Phase`. The dashboard renders **one card** that transitions `rebuilding index 120/540` → `reconciling 3/7 flagged`, sitting **beside** (never replacing) the in-flight-job card, since D10 lets both run at once. The flagged count from `.state/reconciliation.json` is shown on the same card, so "work pending" and "work running" share one place.

## File Changes

| File | Action | Description |
|---|---|---|
| `internal/embed/embed.go` | Create | `Embedder` iface + explicit registry (`Available`, `New`); `nopEmbedder` for tests/`--mock`. |
| `internal/embed/{zerfoo,cybertron}.go` | Create | One adapter per backend, normalizing to unit-norm `[]float32`. `sentex.go` dropped — see D9 amendment (revision 4). |
| `internal/embed/weights/` | Create | `go:embed` blobs — **one per backend format, 2 backends**. Sizes measured before slice 1 merges. |
| `internal/vectors/` | Create | Flat cosine store at `.state/vectors/topics.json`, backend/dim-tagged; `Upsert/Nearest/Rebuild`; single-flight rebuild gate + `ErrRebuilding`; `onProgress` callback. No `internal/status` import. |
| `internal/searchindex/` | Create | bleve BM25 at `.state/search-index/`; `Index/Query/Rebuild` over `topics/*.md`+`meetings/*.md`. |
| `internal/library/reconcile.go` | Create | `Reconciler` iface + `Resolution{Slug,Name,Merged,ProposedSlug,Score,Flagged}`; ledger writer; `ErrRebuilding` ⇒ fail-safe path. |
| `internal/library/library.go` | Modify | `Reconciler` field; `AddMeetingCtx`; `AppendTopicSectionAnnotated`; `ExistingTopicsRecent`. Old signatures delegate. |
| `internal/status/status.go` | Modify | `Maintenance` type + `MaintenancePhase`; `Snapshot.Maintenance *Maintenance`; nil-safe `MaintenanceStart/Progress/Done`; throttled flush in `Progress` only. |
| `internal/pipeline/pipeline.go` | Modify | `:147` recency-capped topics; `:152` `AddMeetingCtx`; post-write index/vector upsert (failures logged, non-fatal). |
| `internal/config/config.go` | Modify | `embedding_backend` + 3 threshold/cap keys (pointer fields, defaults, validation), `SearchIndexDir()`, `ValidEmbeddingBackends()`. |
| `internal/setup/config.go` | Modify | `Values` gains `MergeThreshold`/`NewTopicThreshold`. `embedding_backend` stays config-only — `WriteConfig` already preserves unknown keys. |
| `internal/tui/settings.go` | Modify | New `stepThresholds` between `stepPath` and `stepKey`; fields on `settingsValues` (**pointer-bound — preserve**); own form per step. |
| `internal/tui/dashboard.go` | Modify | `m` key + maintenance/flagged card beside the in-flight card. |
| `internal/tui/data.go` | Modify | Surface `snap.Maintenance` (cleared by the existing stale-PID path) + flagged count from `.state/reconciliation.json`. |
| `internal/web/web.go` | Modify | `/search` route, RRF fusion, goldmark-rendered snippets. |
| `cmd/patro/main.go` | Modify | `patro reconcile [--config]` in `parseArgs`; serve-startup integrity check + background rebuild wired to the Tracker. |
| `tools/embedbench/{go.mod,go.sum,main.go,server.go,README.md}` | Create | Nested non-shipping module (D11). |

## Testing Strategy

| Layer | What | How |
|---|---|---|
| Unit | 3-band resolution; failure ⇒ new+flagged, never merge; registry `Available/New` + unknown name; backend-mismatch invalidation; `ErrRebuilding` ⇒ fail-safe; single-flight rebuild; RRF; cosine; recency-cap ordering | Table-driven, fake `Embedder`/`Reconciler`, `t.TempDir()` |
| Unit (status) | `Maintenance` nil-safe on nil Tracker; progress flush **throttled** (N updates ⇒ far fewer writes); start/done flush unconditionally; `Maintenance` coexists with a non-nil `Current` | Swappable `now` clock (`status.go:77`); count writes via file mtime/`UpdatedAt` |
| Contract | Existing `library_test.go`/`analyzer_test.go`/`status` tests unchanged and green; `Snapshot` gains an optional field only (old `status.json` still unmarshals) | Run as-is; round-trip an old snapshot fixture |
| Integration | `Rebuild()` from markdown alone; nil-reconciler = today's behavior; ingestion continues during rebuild; `/search` hits topics+meetings | `t.TempDir()` fixture, `httptest` |
| Build | `CGO_ENABLED=0` × darwin/linux × amd64/arm64 **with both backends linked** (zerfoo, cybertron — go-sentex dropped, D9 amendment); nested module compiles and resolves the `internal/` import | CI matrix — **gate before slice 1 merges** |
| Excluded | `tools/embedbench` — deliberately outside CI/coverage; manual `cd tools/embedbench && go build ./...` | Documented in its README |

## Threat Matrix

| Boundary | Applicability | Design response | Planned RED test |
|---|---|---|---|
| Documentation-like paths | N/A — no file-type-driven execution | — | — |
| Git repository selection | N/A — no VCS automation | — | — |
| Commit state / Push state / PR commands | N/A — no VCS or PR automation | — | — |
| **Subprocess arg composition** | **Applicable** — TUI spawns `patro reconcile`; gray-zone LLM shells to `kimi/claude -p` | `exec.Command` with an explicit argv slice (never a shell string); `--config` forwarded as a separate arg; ctx timeout on the LLM call; non-zero exit ⇒ flag, never merge | Timeout / non-zero exit ⇒ new flagged topic; argv contains no shell-metacharacter interpolation |
| **Concurrent state mutation** | **Applicable** — background rebuild writes the vector store while the pipeline reads it | Single-flight gate; `ErrRebuilding` from `Nearest()`; atomic temp+rename on store and `status.json` | Concurrent `Nearest` during rebuild returns `ErrRebuilding` and never a partial result; second rebuild trigger is a no-op |
| **Dev-tool network exposure** | **Applicable** — embedbench serves HTTP | Bind `127.0.0.1` only, mirroring `internal/web`; no write path to the library | Listener address is loopback |

## Migration / Rollout

Additive; no library format change. `Snapshot` gains one optional field, so an old `status.json` still unmarshals and an old dashboard ignores it. First run: `Rebuild()` populates both indexes **in the background with visible progress**, and writes suspected duplicates as flagged entries — reported, never auto-merged. Switching `embedding_backend` invalidates and rebuilds the vector store — no data loss, markdown untouched. Rollback = nil reconciler + `rm -rf .state/{vectors,search-index,reconciliation.json}`. Slices 3–8 ship independently of slice 1: with the `nopEmbedder`, reconciliation is inert and `/search` degrades to BM25-only.

## PR chain (8 slices, was 7)

1. `internal/embed` — registry + 2 backends (zerfoo, cybertron; go-sentex dropped per D9 amendment) + embedded weights (large; weight blobs are generated/vendored, not authored lines)
2. `tools/embedbench` — nested module + server *(depends only on slice 1, parallelizable, never release-blocking)*
3. `internal/vectors` (incl. rebuild gate, single-flight, `onProgress`) + `internal/searchindex`
4. library reconciliation seam + audit trail + flags
5. analyzer prompt recency cap
6. web `/search`
7. **`internal/status` `Maintenance` + throttled flush + `patro reconcile` + serve-startup trigger** *(new split: production plumbing, headlessly testable, no TTY needed)*
8. TUI — maintenance/flagged card, `m` key, threshold settings step

The progress work splits the old TUI slice in two rather than adding scope: slice 7 is production plumbing verifiable without a terminal, slice 8 is presentation. That keeps both inside the 400-line budget and avoids bundling the awkward TUI tests with the status-contract tests. Slice 2 should still land early so slices 4/8 inherit **measured** thresholds instead of the provisional 0.90/0.70.

## Open Questions

- [ ] Measure the release-archive size delta with both backends' weights linked, before slice 1 merges. Two weight *formats* means two blobs of the same underlying model — if untenable, the escape hatch is a build tag trimming to the configured default, not re-litigating D9.
- [x] `go-sentex` maturity is unverified — **resolved (revision 4): dropped, not merely unverified.** Live investigation during Unit 1b found a genuine architectural incompatibility (network-dependent weight loading), not just a maturity risk. See D9 amendment.
- [ ] Confirm both backends emit 384 dims for MiniLM-L6-v2; D10's tagging makes a mismatch safe but not silent.
- [ ] Throttle constants (1% / 250 ms) are a starting point — tune once a real first-run migration is timed on a library of realistic size.
