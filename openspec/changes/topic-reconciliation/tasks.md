# Tasks: Semantic topic reconciliation + searchable knowledge library

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines | ~2,800-3,600 authored (whole change); weight blobs excluded as generated |
| 400-line budget risk | High (whole change) — Low/Medium per stacked unit below |
| Chained PRs recommended | Yes |
| Suggested split | 10 stacked units: 1a→1c→1d→2→3→4→5→6→7→8 (unit 1b/go-sentex dropped — see resolution below, D9 amendment revision 4) |
| Delivery strategy | **feature-branch-chain** (confirmed) — long-lived `feature/topic-reconciliation` branch, chained units on top, merged to `main` once the full chain is done |
| Chain strategy | feature-branch-chain (confirmed) |

**Flagged risk**: Slice 1 (`internal/embed`), as one PR, is at the same 400-line risk that caused design to split slice 7 out of TUI — interface+registry+adapters+tests+config validation is authored code, not just weight blobs. Splitting it into 1a (registry+interface+nopEmbedder, config validation) and one PR per backend adapter (each trivially droppable behind the interface) removes the risk without reopening D9. **Update**: unit 1b (`go-sentex`) was dropped during implementation. **Second update**: unit 1c (`zerfoo`) is now also **BLOCKED** (finding above — no contextual-embedding path in zerfoo's public API, not a quick fix) — see finding in Phase 2 below. **This leaves only 1 remaining candidate backend (cybertron, Unit 1d, not yet verified).** A single surviving backend materially changes the `tools/embedbench` (Unit 2) value proposition — it becomes a quality/perf report on one backend, not an A/B comparison, which was one of its stated purposes (D11: "the instrument that sets D7's thresholds and D9's default" — a default choice needs no comparison if there is only one candidate). **This needs a real decision before Unit 1d or beyond proceeds**: (a) verify cybertron and, if it works, accept a 1-backend registry (D9's "registry" framing still has value for future backends, but the current PR chain's "2 backends" premise from the 1b resolution no longer holds); (b) re-open the embedding-runtime candidate search for a genuine second option before locking in cybertron as the sole backend; or (c) accept zerfoo behind the registry anyway as a deliberately lower-quality "cheap" option with the quality gap documented and thresholds calibrated accordingly (not recommended — silently ships a weaker default than the design assumed). This apply run does not decide among these and stops for orchestrator/human input.

### Suggested Work Units

| Unit | Goal | PR | Focused test command | Runtime harness | Rollback boundary |
|---|---|---|---|---|---|
| 1a | embed registry+interface+nopEmbedder+config validation | 1a | `go test ./internal/embed/... ./internal/config/...` | N/A — no adapter yet; `go build ./...` only | delete embed.go + config keys, no callers |
| ~~1b~~ | ~~go-sentex adapter~~ — **DROPPED** (network-dependent weight loading, incompatible with decision 2; see D9 amendment rev 4) | — | — | — | n/a — no code was written |
| 1c | zerfoo adapter — **BLOCKED**, no contextual-embedding path in public API (see finding below) | 1c | `go test ./internal/embed/ -run Zerfoo` | N/A — build-only | n/a — no code was written |
| 1d | cybertron adapter + size/dim gate — **now the only unblocked backend candidate; verify before proceeding** | 1d | `go test ./internal/embed/ -run Cybertron` | `goreleaser build --snapshot` archive-size check, all 4 platforms | remove cybertron.go + registry entry; escape hatch = trim build tag |
| 2 | `tools/embedbench` nested module | 2 | `cd tools/embedbench && go build ./... && go vet ./...` | `cd tools/embedbench && go run .` → manual form at `127.0.0.1:<port>` | delete `tools/embedbench/` dir, zero root-module impact |
| 3 | `internal/vectors` + `internal/searchindex` | 3 | `go test ./internal/vectors/... ./internal/searchindex/... -race` | N/A pre-Unit-7 (no CLI trigger yet) | delete `.state/vectors`, `.state/search-index` |
| 4 | library reconciliation seam | 4 | `go test ./internal/library/...` | `./patro process --mock <file>` with stub Reconciler | `Library.Reconciler = nil` restores exact-slug behavior |
| 5 | analyzer prompt recency cap | 5 | `go test ./internal/analyzer/... ./internal/pipeline/...` | `./patro process --mock <file>` vs 500-topic fixture | revert `pipeline.go:147` to full list |
| 6 | web `/search` | 6 | `go test ./internal/web/...` | `./patro run web --mock` → GET `/search?q=` | remove route+handler |
| 7 | status Maintenance + `patro reconcile` + serve trigger | 7 | `go test ./internal/status/... -race` | `./patro reconcile --mock`, `./patro serve --mock` | optional field; remove subcommand+trigger, old dashboards ignore it |
| 8 | TUI maintenance card + threshold settings | 8 | `go test ./internal/tui/...` | `./patro run tui --mock`, press `m`, Settings threshold step | revert hooks, `m` freed |

## Phase 0/1 (Unit 1a): embed registry skeleton — **DONE** (commit 083bed9 on `feature/topic-reconciliation-1a-embed-registry`)
- [x] 1a.1 RED: `internal/embed/embed_test.go` — `Available()` contents, `New()` success/unknown-name error.
- [x] 1a.2 GREEN: `internal/embed/embed.go` — `Embedder` iface, explicit registry table, `nopEmbedder`.
- [x] 1a.3 [SPIKE/GATE] `tools/embedbench/go.mod` with D11 replace directive + minimal `main.go` importing `internal/embed`; run `cd tools/embedbench && go build ./...` to compile-verify the internal/-import legality **before any server/UI work in Unit 2**. Confirm root `go build/vet/test ./...` does not descend into it.
- [x] 1a.4 RED→GREEN: `embedding_backend` config key — `internal/config/config.go` (`ValidEmbeddingBackends()` beside `ValidAnalyzerBackends()`, config.go:52); unknown-name validation error lists `Available()`.

## Phase 2 (Units 1c-1d): backend adapters — 1c **BLOCKED** (finding below, needs a decision); 1d not yet started
- [x] 1b.1 **DROPPED — deliberate scope reduction, not a failure to fix later.** `internal/embed/sentex.go` wrapping `github.com/edgetools/go-sentex` will not be implemented. Investigated live on branch `feature/topic-reconciliation-1b-drop-sentex` (off 1a, commit 083bed9): the module is real and resolves, so this is a genuine architectural incompatibility with decision 2, not an imagined API. Finding: `sentex.LoadModel()` takes zero arguments and has no build-time weight-embedding path — it downloads `model.onnx` (~87MB) + `tokenizer.json` (~700KB) from HuggingFace Hub over `net/http` on first call into `$HF_HOME`/`os.UserCacheDir()+"/huggingface"`, with no exported way to inject pre-loaded bytes, a reader, or a local path. This breaks "no network call after install" (decision 2). A `.state/`-scoped `HF_HOME` cache-priming workaround exists in principle but is new, unscoped design work (global env mutation, cache-priming semantics) — not a same-unit fix — and its own weight-shipping story would duplicate what `go:embed` already does for the other backends with zero benefit. Transitive dependency footprint (~15 extra modules: `gomlx`, `onnx-gomlx`, `sugarme/tokenizer`, etc.) also contradicts the design spike's "zero deps" characterization. **Decision (orchestrator, using D9's own droppable-adapter escape hatch): ship 2 backends — zerfoo (1c) + cybertron (1d) — not 3.** No `sentex.go` written, no registry entry added. See design D9 amendment (revision 4) for full rationale. Unit 1c is unaffected and can proceed immediately.
- [ ] 1c.1 **BLOCKED — do not implement as designed.** `internal/embed/zerfoo.go` wrapping `github.com/zerfoo/zerfoo`. Investigated live on branch `feature/topic-reconciliation-1c-zerfoo-adapter` (off `feature/topic-reconciliation-1b-drop-sentex`): the module is real and resolves (`go get github.com/zerfoo/zerfoo@latest` → v1.57.0), and `zerfoo.Load()`/`inference.LoadFile()` genuinely support a **local GGUF file path with zero network calls** (unlike go-sentex) — decision 2 is satisfiable in principle. The blocker is different and deeper: verified by reading `inference/inference.go` (`Model.Embed`), `inference/arch_bert.go`, and `inference/encoder.go` source directly.
  - `Model.Embed(text)` (the only exported embedding API reachable from a `Load()`-ed model) tokenizes the text and **mean-pools the raw, static token-embedding lookup table** — it never invokes the model's transformer graph (no attention, no FFN, no layer norms). This holds for every architecture, including `bert`: `buildBertGraph` constructs a full contextual BERT graph, but `LoadFile` only ever extracts the isolated `token_embd.weight` tensor from it for `Embed()`'s use; the rest of the graph is unused by that path.
  - The other path, `inference.LoadEncoderFile` → `EncoderModel.Forward`, DOES run the full contextual graph, but only exposes final classification logits (`[1, numClasses]`, requiring a fine-tuned classification head baked into the GGUF) — there is no exported way to read the pooled pre-classifier hidden state as a general-purpose embedding vector.
  - Net effect: zerfoo's public API (v1.57.0) has **no path to genuine contextualized sentence embeddings** from a BERT/MiniLM-family GGUF — only a non-contextual bag-of-token-embeddings average. This directly undercuts the reason embeddings were chosen over hand-rolled fuzzy matching in the first place (proposal: "misses true synonyms"): a static average is only marginally better at synonym/paraphrase detection than lexical matching.
  - Compounding: zerfoo's own documented "Embeddings" feature (README, `examples/embedding/`, `examples/rag/`) is demonstrated exclusively with full generative LLMs — Gemma 3 1B, Llama 3.2 1B — not any small dedicated embedding model. Embedding either at build time would be GB-scale per platform, far beyond even the 87MB already flagged as large for go-sentex, and incompatible with the project's embed-at-build-time size budget. A small hand-sourced MiniLM-family GGUF would fit the size budget but would still hit the "no contextual path" limitation above — size and quality cannot both be solved by choosing a different model, because the limitation is in `Embed()` itself, not in model size.
  - Action taken: no `zerfoo.go` written, no `"zerfoo"` registry entry added. The exploratory `go get` was reverted (`git checkout -- go.mod go.sum`); working tree confirmed byte-identical to the 1b-drop-sentex commit. `go build/vet/test ./...` green after revert.
  - **This is NOT a same-shape finding as 1b (no locked-decision violation — zerfoo is network-free).** It is a "does the stable API actually deliver a usable embedding model" finding, matching the caveat this task was scoped to test for. Requires a human/design decision — see risk note above and D9 amendment (revision 4 note). Not unilaterally dropped, unlike 1b: this apply run stops here and reports rather than guessing.
- [ ] 1d.1 RED→GREEN: `internal/embed/cybertron.go` — cybertron/spago adapter, same contract test shape. **Now the only remaining unblocked backend adapter — see flagged risk above.**
- [ ] 1.gate Measure release archive size once backends are resolved, `CGO_ENABLED=0` × darwin/linux × amd64/arm64. Scope pending 1c resolution — do not reopen D9 without a decision.
- [ ] 1.checkpoint Confirm dims for whichever backends ship; record actual values if any mismatch (D10 tagging makes mismatch safe, not silent).

## Phase 3 (Unit 2): tools/embedbench
- [ ] 2.1 `server.go`/`main.go`: Server mirroring `internal/web` shape, one inlined `template.Must` page, graceful shutdown.
- [ ] 2.2 RED→GREEN: listener address is loopback-only (threat matrix: dev-tool network exposure).
- [ ] 2.3 Form: text A/B → per-backend `Dim()`, wall-time, `cos(A,B)`, cross-backend agreement matrix. Reuses `embed.Available()`/`New()` only.
- [ ] 2.4 Confirm CI + `.goreleaser.yaml` do not reach `tools/embedbench`.

## Phase 4 (Unit 3): vectors + searchindex
- [ ] 3.1 RED: concurrent `Nearest()` during rebuild returns `ErrRebuilding`, never a partial result (design: "highest-risk new interaction") — write before/alongside concurrency code.
- [ ] 3.2 RED: second rebuild trigger while one is in flight is a no-op (single-flight gate).
- [ ] 3.3 GREEN: `internal/vectors/{store.go,rebuild.go}` — flat cosine store, backend/dim-tagged, `Upsert/Nearest/Rebuild(ctx,src,onProgress)`, no `internal/status` import.
- [ ] 3.4 RED→GREEN: backend-mismatch invalidation schedules `Rebuild()`.
- [ ] 3.5 `internal/searchindex/{index.go,rebuild.go}` — bleve BM25 (no `vectors` tag), `Index/Query/Rebuild`.
- [ ] 3.6 RED→GREEN: `Rebuild()` reconstructs both indexes from markdown alone after `.state/{vectors,search-index}` deletion.
- [ ] 3.7 Integration test: pipeline ingestion continues (non-blocking) during a rebuild.

## Phase 5 (Unit 4): library reconciliation seam
- [ ] 4.1 `internal/library/reconcile.go` — `Reconciler` iface, `Resolution{...}`, `.state/reconciliation.json` ledger writer.
- [ ] 4.2 RED→GREEN: 3-band table test — ≥0.90 merge, <0.70 new, gray zone → exactly one LLM call.
- [ ] 4.3 RED→GREEN: gray-zone LLM error/timeout ⇒ new+flagged, never merge (threat matrix: subprocess arg composition — explicit argv, ctx timeout).
- [ ] 4.4 RED→GREEN: merge writes markdown annotation (proposed slug + cosine) AND matching `.state/reconciliation.json` entry.
- [ ] 4.5 GREEN: `library.go` — `Reconciler` field, `AddMeetingCtx`, `AppendTopicSectionAnnotated`, `ExistingTopicsRecent`; old signatures delegate unchanged.
- [ ] 4.6 [CHECKPOINT] Existing `internal/library/library_test.go` passes unmodified with nil `Reconciler`.
- [ ] 4.7 RED→GREEN: meeting mid-rebuild → `ErrRebuilding` → new+flagged, picked up by next reconcile pass.

## Phase 6 (Unit 5): analyzer prompt cap
- [ ] 5.1 `Library.ExistingTopicsRecent(n)` sorted by lastUpdate desc.
- [ ] 5.2 Wire `pipeline.go:147`; `BuildPrompt` stays an unchanged pure formatter (D6).
- [ ] 5.3 RED→GREEN: 500-topic fixture, prompt capped at `topic_prompt_limit` (50), most-recent-first.
- [ ] 5.4 [CHECKPOINT] Existing `internal/analyzer/analyzer_test.go` passes unmodified.

## Phase 7 (Unit 6): web /search
- [ ] 6.1 `/search` route — BM25+cosine via RRF, goldmark-rendered, no external JS/assets.
- [ ] 6.2 RED→GREEN: `httptest` — ranked hits from both `topics/` and `meetings/`.

## Phase 8 (Unit 7): status Maintenance + patro reconcile
- [ ] 7.1 `Maintenance`/`MaintenancePhase` types + `Snapshot.Maintenance *Maintenance`.
- [ ] 7.2 RED→GREEN: old-format `status.json` fixture (no `Maintenance` field) still unmarshals — explicit backward-compat round-trip test.
- [ ] 7.3 RED→GREEN: nil-safe `MaintenanceStart/Progress/Done` on nil `Tracker`.
- [ ] 7.4 RED→GREEN: progress flush is throttled — count actual writes across N `MaintenanceProgress` calls (via mtime/write-count instrumentation), assert far fewer than N; start/done flush unconditionally. Use swappable `now func() time.Time` (status.go:77) to control the 250ms threshold deterministically — assert write count, not just final state.
- [ ] 7.5 RED→GREEN: `Maintenance` coexists with non-nil `Current`.
- [ ] 7.6 Wire `internal/vectors` `onProgress` callback to `Tracker.Maintenance*` in serve/cmd (D12: `internal/vectors` stays status-free).
- [ ] 7.7 `patro reconcile [--config]` in `cmd/patro/main.go` — ensure-index → reconcile flagged; explicit argv, no shell string (threat matrix, since Unit 8's TUI spawns this).
- [ ] 7.8 Serve-startup integrity check → background rebuild wired to Tracker (D10: startup + on-demand only, never mid-pipeline).

## Phase 9 (Unit 8): TUI
- [ ] 8.1 `settings.go` — new `stepThresholds` between `stepPath`/`stepKey`; pointer-bound `settingsValues` fields (preserve documented gotcha); own form per step.
- [ ] 8.2 RED→GREEN: submitting thresholds updates `config.yaml`.
- [ ] 8.3 `internal/setup/config.go` — `Values` gains threshold fields; confirm `WriteConfig` preserves unknown keys.
- [ ] 8.4 `dashboard.go` — bind `m` (free vs. `q/ctrl+c/esc/f/r/w/o/tab/↑k/↓j/enter`) to spawn `patro reconcile` detached.
- [ ] 8.5 `data.go` — surface `snap.Maintenance` (stale-PID-cleared) + flagged count from `.state/reconciliation.json`.
- [ ] 8.6 One maintenance/flagged card beside (not replacing) the in-flight job card, transitioning `rebuilding index D/T` → `reconciling D/T flagged`.
- [ ] 8.7 RED→GREEN: dashboard test — flagged count + phase on one card, independent in-flight card unaffected.

## Phase 10: Final gate
- [ ] 10.1 CI matrix: `CGO_ENABLED=0` × darwin/linux × amd64/arm64, both backends linked (zerfoo, cybertron) — final confirmation once both adapters exist.
- [ ] 10.2 `.goreleaser.yaml` builds only `./cmd/patro`; no embedbench in release artifacts.
- [ ] 10.3 Full `go vet ./... && go test ./...` green, all units integrated.
