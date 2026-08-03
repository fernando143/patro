## Exploration: long-document-multivector-embeddings

### Current State

Patro has one low-level embedding contract: `embed.Embedder.Embed(ctx, text) ([]float32, error)`. The Cybertron adapter sends the complete string to `TextEncoding.Encode`, which tokenizes with WordPiece, adds `[CLS]` and `[SEP]`, and rejects sequences longer than the model's 512 positions. Therefore the usable text budget is 510 WordPiece tokens, not 512. Cybertron v0.2.1 also ignores the supplied context during a single `Encode` call.

The current representation is one normalized 384-dimensional vector per topic. That assumption is embedded in migration scoring, semantic reconciliation, web search, and `.state/vectors/topics.json`.

#### Embedding call-site audit

| Call site | Input | Can exceed 512 tokens? | Current outcome |
|---|---|---:|---|
| `internal/migration/migration.go:99` (`Service.BuildPlan`) | Entire historical topic Markdown file | **Yes; confirmed at 609 tokens** | Aborts the complete TUI/CLI migration plan with the production error. Both callers use `context.Background()`. |
| `internal/vectors/rebuild.go:54` (`Store.Rebuild`) | Entire topic Markdown file | **Yes** | Silently skips the topic on embedding error, then persists the remaining entries and marks the store current. A long topic can disappear from semantic search without keeping `NeedsRebuild` true. |
| `internal/library/reconcile.go:119` (`SemanticReconciler.Reconcile`) | Candidate name plus content | **Yes** | Analyzer topic content has no successful-parse length bound. An embedding failure safely creates a new flagged topic, but increases fragmentation. |
| `internal/library/library.go:481-501` (`tryMergeFlagged`) | Almost the entire flagged topic file as candidate content | **Yes** | Reuses `SemanticReconciler`; a long historical topic repeatedly safe-fails instead of reconciling. |
| `internal/web/web.go:317` (`rankedResults`) | User query string | **Yes** | Logs the embedding error and silently degrades that request to BM25-only results. |
| `tools/embedbench/server.go:143,151` (`report`) | User-supplied text A/B | **Yes** | Returns an embedding error and cannot benchmark long text. This is a developer-only nested module but still uses the production registry. |

Tests call `Embed` directly, but there is no current boundary or regression test for a sequence over 512 tokens.

#### Store, rebuild, and scoring behavior

- `internal/vectors` persists `{backend, dim, model_version, entries:[{id, vector}]}` and ranks one vector per topic by dot product.
- Every production `NewStore` call currently passes `embedder.Name()` as `model_version`; for Cybertron both backend and model version are therefore just `"cybertron"`. The actual `cybertron-spago-v1` manifest version is not part of invalidation.
- Rebuild order and persisted IDs are sorted, nearest-neighbor ties use ID order, and the final file replacement is atomic.
- Rebuild checks cancellation between files, not within Cybertron inference. It is single-flight and makes `Nearest` return `ErrRebuilding`.
- There is no production `Store.Upsert` call. Vector state is rebuilt only when missing/tag-mismatched, or unconditionally after an accepted historical migration. Ordinary topic changes are not source-hash checked, so a valid-tag store can become stale.
- Migration compares every topic pair, semantic reconciliation asks for one nearest topic, and web search fuses vector and BM25 ranks with reciprocal-rank fusion.

### Affected Areas

- `internal/embed/embed.go` — current single-vector public contract, registry, and test double.
- `internal/embed/cybertron.go` — exact WordPiece tokenizer, 512-position limit, model identity, normalization, and the only real encoder.
- `internal/embed/weights/cybertron/tokenizer_config.json` — declares lowercasing and `model_max_length: 512`; it must remain the tokenizer source of truth.
- `internal/vectors/store.go` — one-vector schema, invalidation tags, nearest-neighbor API, deterministic persistence, and aggregation.
- `internal/vectors/rebuild.go` — full-file embedding, silent omission on errors, cancellation, and batch replacement.
- `internal/migration/migration.go` / `configured.go` — direct full-document embedding and duplicate-document scoring.
- `internal/library/reconcile.go` / `library.go` — candidate and flagged-document embedding plus merge thresholds.
- `internal/web/web.go` — semantic query embedding and hybrid ranking.
- `internal/pipeline/pipeline.go` and `cmd/patro/main.go` — construction, rebuild triggers, model-version propagation, and index freshness.
- `tools/embedbench/server.go` — developer-facing comparison semantics must understand multi-vector results.
- `internal/{embed,vectors,migration,library,web,pipeline}/*_test.go` — strict RED-GREEN seams and cross-call-site regression coverage.

### Approaches

1. **Chunk and average inside the Cybertron adapter** — split long text, average all chunk vectors, and keep every caller/store unchanged.
   - Pros: Smallest blast radius; immediately avoids the 512-token exception.
   - Cons: Violates the requested faithful multi-vector representation; a centroid dilutes small but important sections, loses passage-level matches, and forces one aggregation policy on every use case.
   - Effort: Low.

2. **Chunk independently at each call site** — migration, rebuild, reconciliation, web, and embedbench each own splitting and aggregation.
   - Pros: Each surface can choose its own scoring policy without changing the base adapter immediately.
   - Cons: Duplicates tokenizer limits and chunk rules, invites representation drift, leaves persistence ambiguous, and makes future model changes unsafe. Fixing only migration would leave four known long-input paths exposed.
   - Effort: Medium initially, high to maintain.

3. **Shared long-document embedder plus versioned multi-vector store** — retain one-window inference as an internal primitive and expose one shared tokenizer-aware service returning an ordered vector set with metadata.
   - Pros: Faithful representation; one tested chunking policy; all call sites become safe; task-specific scoring remains explicit; representation/model changes can invalidate derived state correctly.
   - Cons: Requires coordinated interface, persistence, scoring, and caller changes; existing thresholds must be recalibrated.
   - Effort: High.

4. **Swap to a long-context model** — replace all-MiniLM-L6-v2 with another encoder.
   - Pros: Could reduce chunk count for moderately long documents.
   - Cons: Cybertron documents no verified drop-in long-context encoder for Patro; a larger model does not imply a larger context window; it changes vector space, release size, latency, and threshold calibration while still requiring a policy for documents beyond the new limit.
   - Effort: High and currently unproven.

### Recommendation

Choose approach 3. The safest first implementation slice is the **shared long-document abstraction and its pure chunk/scoring contracts**, not a migration-only patch. Subsequent slices in the same change should migrate every production call site, then replace the store format. This prevents a temporary state where migration works but rebuild, flagged reconciliation, or web search still fails on the same text.

#### Tokenizer-aware hierarchical chunking

Use the exact tokenizer loaded with the Cybertron model; do not estimate from words, bytes, or runes. The wrapper must mirror `do_lower_case`, use `MaxPositionEmbeddings`, and reserve two positions for `[CLS]`/`[SEP]`.

Recommended deterministic pipeline:

1. Parse Markdown into ordered heading sections using the existing Goldmark dependency.
2. Keep whole paragraphs together while packing toward an initial 480-token target.
3. Split an oversized paragraph at sentence boundaries and repack sentences.
4. Split an oversized sentence by exact WordPiece token spans as the guaranteed fallback.
5. Apply a bounded overlap (initial candidate: 32 payload tokens) only between adjacent content chunks, then re-count the complete chunk.
6. Enforce a hard maximum of 510 payload tokens for every encoder call; never truncate content or drop a section.

The 480/32 values are initial design candidates, not hidden constants: they, title treatment, tokenizer/model identity, and the chunker algorithm must be represented in a stable fingerprint. Cybertron token offsets are rune-based, so hard fallback must slice `[]rune`, not raw byte offsets. Check `ctx.Err()` before tokenization, before every chunk encode, and before returning/persisting results. Cancellation cannot interrupt one in-flight Cybertron call because the upstream implementation ignores context, but it can stop before the next chunk.

Represent the document title as a distinct vector and preserve stable chunk kind/ordinal metadata. Do not duplicate the root title into every content chunk; that would overweight titles and increase false positives. Section headings may provide chunk context, but any generated date prefix must not dominate the semantic text and its tokens must count against the same limit.

#### Similarity aggregation

No single aggregation is safe for all three behaviors:

| Aggregation | False-positive / false-negative tradeoff | Suitable use |
|---|---|---|
| Maximum chunk cosine | Highest recall, but one generic or boilerplate chunk can make unrelated long documents look equivalent. | Search ranking signal; unsafe by itself for auto-merge. |
| Top-k mean | Dampens one accidental peak, but depends on document length and `k`; can penalize a short document with one genuinely matching passage. | Search/reconciliation candidate after calibration. |
| Directed coverage, `mean_i(max_j cosine(query_i, doc_j))` | Requires every query/candidate chunk to be represented while not diluting against unrelated target chunks. A one-chunk query reduces to max. | Web query and new-topic candidate against a broader historical topic. |
| Symmetric coverage (both directions) | Rejects partial-overlap duplicates, reducing dangerous false merges; can miss legitimate broad-vs-narrow relationships. | Historical duplicate migration, where equivalence is intended. |

Use directed coverage for web/reconciliation and symmetric coverage for migration. Blend title-to-title similarity at a small, capped weight only when both sides have titles; title similarity alone must never cross an auto-merge threshold. Keep BM25/vector RRF unchanged after vector ranking.

The exact coverage reducer, top-k use, title weight, and thresholds must be locked in design against labeled fixtures. Existing `0.90/0.70` thresholds cannot be assumed valid after changing representation and aggregation. False positives are especially costly in semantic auto-merge; search can tolerate extra low-ranked hits, while migration proposals still require human approval.

#### Persistence, compatibility, and invalidation

Introduce a v2 store at the existing path with one entry per document and an ordered vector set, for example: schema version, backend, actual model version, representation/chunker fingerprint, dimension, and entries containing source hash plus `{kind, ordinal, token_count, vector}` chunks.

- Treat the current schema (missing `schema_version`, one `vector`) as v1 and mark it `NeedsRebuild`; do not mix or silently convert v1 vectors into v2 scoring.
- Preserve one-vector behavior naturally for short documents as a one-chunk vector set, but still rebuild so every entry shares one representation fingerprint.
- Surface the actual model/weights version from `internal/embed`; stop using `Name()` as `model_version`.
- Change the persisted model/representation tag so an old binary sees a mismatch rather than reading v2 entries as nil v1 vectors during rollback.
- Invalidate on any backend, model weights, tokenizer, dimension, chunker version, payload/overlap, title treatment, or persisted representation change. A scorer-only change need not re-embed vectors, but it does require an explicit scorer/calibration version and threshold review.
- BM25 is independent and needs no rebuild for an embedding-only representation change; Markdown mutations still require its normal rebuild/update path.

#### Performance, deterministic rebuilds, and freshness

Memory and disk grow roughly with total chunk count: each raw 384-float vector is 1,536 bytes before slice/JSON overhead. Search changes from `O(documents × 384)` to `O(query_chunks × total_document_chunks × 384)`. Historical migration becomes approximately `O(document_pairs × chunks_A × chunks_B × 384)`. Cache each document representation once, retain the current sorted file/ID order, sort chunks by ordinal, and preserve ID tie-breaking.

Keep initial inference sequential until Cybertron model concurrency is proven safe. Avoid per-chunk or per-document full-store flushes: rebuild/sync should commit one deterministic atomic snapshot. A rebuild temporarily holds old and new representations, so peak memory is roughly double the vector index.

Add source hashes and make maintenance a deterministic incremental sync: reuse unchanged v2 entries, re-embed changed/new files, remove deleted IDs, and atomically swap only after success. Cancellation or an embedding error must preserve the previous complete snapshot; it must not mark a partial store current. Run this sync at maintenance entry even when tags match. The design should also expose a post-write dirty/update seam so a long-lived `serve` process does not retain today's stale store after ordinary topic writes.

#### Strict TDD strategy

Apply must prove RED before production changes for each work unit and record the narrow command plus expected failing behavior. Recommended seams are a tokenizer/token-span interface, a one-window encoder interface, a pure hierarchical chunker, a pure multi-vector scorer, and an injectable store/rebuild boundary.

Behavior-first RED tests should cover:

- A real Cybertron regression fixture over 512 exact WordPiece tokens (including the observed 609-token class) succeeds with multiple unit vectors; markers at the beginning, middle, and end prove no truncation.
- Short input produces one chunk and preserves the existing underlying vector.
- Markdown section, paragraph, sentence, and token fallback order; Unicode-safe token slicing; every encoder call stays at or below 510 payload tokens; overlap and ordering are deterministic.
- Cancellation between chunks returns `context.Canceled` and performs no later encoder calls.
- Late-section matches survive multi-vector scoring; a single generic overlap does not classify two long documents as duplicates; directed versus symmetric coverage and title caps behave explicitly.
- Store v1 loads as rebuild-required; v2 multi-vector round-trips deterministically; model/chunker fingerprint changes invalidate; wrong dimensions/corrupt chunks are rejected.
- Incremental sync reuses unchanged hashes, refreshes changed files, removes deleted files, batches one atomic commit, and preserves the old snapshot on cancellation/error.
- Migration planning, vector rebuild, normal semantic reconciliation, flagged-topic reconciliation, and long web queries each exercise a >512-token input without the old failure/degradation path.
- `tools/embedbench` handles long A/B inputs and its nested module is tested separately.

Use table-driven tests and `t.TempDir()` throughout. Run the narrow package test to capture RED, then GREEN, followed by `go test ./...`, `go vet ./...`, `go build ./...`, and `go test ./...` in `tools/embedbench` if that module changes. One real-model contract test is sufficient; most chunking, cancellation, and aggregation cases should use deterministic fakes.

### Risks

- Multi-vector score distributions differ from single-vector cosine; reusing thresholds without labeled calibration can cause false auto-merges or excessive fragmentation.
- The current rebuild silently validates a partial index after embedding errors; preserving that behavior would hide data even after chunking.
- Cybertron cannot cancel an in-flight encode, and current TUI/CLI migration planning supplies `context.Background()`.
- Multi-vector JSON size, rebuild peak memory, and pairwise migration cost grow with total chunks; benchmarks are required before considering concurrency or a coarse prefilter.
- Markdown/sentence boundaries, generated date headings, code blocks, links, and Unicode offsets can destabilize chunks unless the algorithm and fingerprint are deterministic.
- The coordinated abstraction, store migration, all call sites, and tests are likely to challenge the 800-line single-PR review budget. `sdd-tasks` must forecast authored lines and require an explicit size exception or narrower scope before apply if the budget is exceeded.

### Ready for Proposal

Yes. The proposal should commit to a shared tokenizer-aware multi-vector abstraction, v2 invalidating/rebuildable persistence, use-case-specific aggregation, all known call sites, source-hash incremental sync, and strict recorded RED-GREEN-REFACTOR execution. Detailed formulas, thresholds, and version fingerprints should be finalized in spec/design with labeled behavior fixtures.
