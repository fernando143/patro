# Design: Long-Document Multi-Vector Embeddings

## Technical Approach

Cybertron remains the one-window encoder behind an exact-token Markdown representer. RFC 8785 representation/storage supplies scoring, migration, reconciliation, search, and embedbench without truncation.

## Architecture Decisions

| Choice | Rationale |
|---|---|
| Exact token windows | Prevent WordPiece/UTF-8 drift. |
| Separate profiles | Directed/symmetric distributions differ. |
| Rollback-backed commit | Preserve all-old/all-new state durably. |

## Interfaces and Schemas

```go
type ExactTokenizer interface { Tokenize(context.Context, string) ([]TokenSpan, error); Identity() TokenizerIdentity; MaxPositions() uint32 }
type WindowEncoder interface { EncodeWindow(context.Context, []TokenSpan) ([]float32, error); Identity() ModelIdentity }
type Representer interface { Represent(context.Context, Document) (Representation, error) }
type Scorer interface { Directed(context.Context, Representation, Representation) (float64, error); Symmetric(context.Context, Representation, Representation) (float64, error); Version() string }
type Ranker interface { Rank(context.Context, Representation, []Representation, int) ([]Result, error) }
type RepresentationStore interface { Sync(context.Context, string, Progress) error; Nearest(context.Context, Representation, int) ([]Result, error); MarkDirty(); NeedsSync() bool }
type CommitFS interface { CreateTemp(string, string) (File, error); LinkOrCopyRollback(string, string) (File, error); SyncFile(File) error; Close(File) error; Rename(string, string) error; SyncDir(string) error; Restore(string, string) (File, error); Remove(string) error }
```

`Representation={schema_version:2,document_id:string,source_hash:string,backend:string,model_id:string,model_version:string,model_weights_sha256:string,tokenizer_sha256:string,chunker_version:string,normalization_version:string,representation_fingerprint:string,dimension:integer,chunks:[]Chunk}`. `Chunk={kind:string,ordinal:integer,token_count:integer,source_start_rune:integer,source_end_rune:integer,overlap:integer,vector:[]number}`. `StoreV2={schema_version:2,backend:string,model_id:string,model_version:string,model_weights_sha256:string,tokenizer_sha256:string,chunker_version:"md-wordpiece-510-478-32-v1",normalization_version:"l2-f32-v1",representation_fingerprint:string,scorer_version:"coverage-title-v2",dimension:integer,entries:[]Entry}`; `Entry={id:string,source_hash:string,chunks:[]Chunk}`.

All persistence is RFC 8785 JSON; entries sort by ID, chunks by kind/ordinal. Lowercase-hex `source_hash` hashes exact Markdown; `model_weights_sha256` hashes raw `spago_model.bin`; `tokenizer_sha256=SHA256(RFC8785({"tokenizer_config_sha256":SHA256(raw tokenizer_config.json),"vocab_sha256":SHA256(raw vocab.txt)}))`.

Representation fingerprint is SHA-256 of RFC 8785 JSON containing exactly `{schema_version:2,backend,model_id,model_version,model_weights_sha256,tokenizer_sha256,chunker_version:"md-wordpiece-510-478-32-v1",normalization_version:"l2-f32-v1",dimension}`. Validate hashes, unique `(document_id,kind,ordinal)`, spans, `token_count`, finite dimension-correct vectors, and norm `1±1e-5`; one defect invalidates all.

## Chunking and Scoring

Precedence: heading section → semantic block → sentence → tokenizer boundary. Semantic blocks are paragraph, list item, fenced/indented code block, table row, or thematic block, source-ordered. First title/content chunk has ≤510 new tokens; later chunks have exactly 32 prior (all if fewer) plus ≤478 new; generated context counts within 510. Fallback splits tokenizer indices only; rune ranges derive only from token offsets. Root title is separately chunked/removed/never duplicated; nothing is omitted.

Exactly `C(A→B)=mean_a(max_b(dot(a,b)))`, `T=min(C(TA→TB),C(TB→TA))`; with both content/title sets, `S=.9*C+.1*T`, otherwise `S=C`. Merge/proposal requires content both sides. Queries/candidates use `S(A→document)`; migration uses `min(S(A→B),S(B→A))`; ties sort ascending. Scoring/ranking checks context between chunks/documents and before return; error returns no IDs. `/search` discards all semantic results, exposes fallback, and renders every BM25 hit with RRF `k=60`.

## Calibration

Each RFC 8785 profile contains exactly `{profile_schema:"calibration-v1",scorer_mode,scorer_version:"coverage-title-v2",backend,model_id,model_version,model_weights_sha256,representation_fingerprint,normalization_version:"l2-f32-v1",corpus_id,corpus_sha256,sample_count,negative_support,positive_support,n,m}`. Only `directed-reconciliation` and `symmetric-migration` exist with separate corpora. Counts/supports are integers; `n,m` numbers. Per mode, `m` is lowest with ≥20 at-or-above labels/zero non-duplicates; `n` highest with ≥20 below/zero duplicates; counts record all/qualifying labels, `n<m`, fixture ties sort by ID. Identity/mode mismatch disables score action; only successful LLM adjudication may merge, otherwise flag. Legacy `0.90/0.70` is recalibrated, never imported.

## Durable Sync

States are exactly `Dirty→Building→Prepared→CommitIntent→Current`. Building hashes/reuses sources, embeds changed/new, removes deleted, then uses injected `CommitFS` to CreateTemp, write candidate, SyncFile/Close it, LinkOrCopyRollback old state, SyncFile/Close rollback, and SyncDir before Prepared. Prepared performs the final context check: cancellation cleans artifacts, preserves old/absent disk+memory, remains Dirty; success atomically enters CommitIntent.

CommitIntent masks all later cancellation, including before physical Rename. Only storage failure can fail: Rename candidate, SyncDir parent, then swap memory/Current. Failure invokes Restore, SyncFile/Close restored target, SyncDir, and Remove cleanup; old memory/Dirty remains. First creation treats absence as old state and removes uncertain targets. `errors.Join` preserves primary plus rollback/cleanup failures.

Production supplies OS `CommitFS`; tests inject a deterministic fake. RED matrix independently fails CreateTemp; LinkOrCopyRollback; SyncFile(candidate/rollback/restore); Close(candidate/rollback/restore); Rename; SyncDir(rollback/commit/restore); Restore; Remove, from old/absent states, including rollback failure joining and cancellation in Prepared versus after CommitIntent.

## Call Sites, Tests, and Performance

`internal/embed` owns representation/scoring/calibration; `internal/vectors` owns v2/CommitFS. `migration.BuildPlan` uses `symmetric-migration`; normal/flagged `library` and `web.rankedResults` use `directed-reconciliation`. `pipeline.ProcessVideo`, `cmd/patro` maintenance/reconcile/`wireSearch`, `migration.ConfiguredService`, `internal/tui/migrate.go`, and `tools/embedbench/server.go` share cancellable state; writes dirty then sync.

Other RED tests cover semantic-block/token/rune boundaries; RFC 8785 identity/store/profile bytes; formulas/content gating; wrong profiles; scorer/ranker error/cancellation; web zero-partial fallback. V0.5.x `model_version:"cybertron"` rejects v2 `"cybertron-spago-v1"` and rebuilds without scoring while weights/dimension stay identical. Record RED/GREEN/REFACTOR in `tasks.md`; finish root/nested `go test ./...`.

Authoritative manifest fixes corpus identity/cases/dimensions/seed. Host: idle AC Linux/amd64, Ryzen 7 7800X3D, 32GB, Go 1.26.5, `GOMAXPROCS=8`, governor `performance`; mismatch/unavailable governor sets `authoritative:false` and cannot pass. One model load performs 5 warmups then exactly 30 measured runs. Each block reports nearest-rank p50/p95 `ceil(p*30)` over its sorted runs; gates use that p95: wrapper ≤1.25× paired direct-window p95, search ≤100 ms, symmetric 100×10 chunks ≤5 s. After one pre-block GC, each same block reports mean `TotalAlloc` delta/30; wrapper-minus-baseline ≤16 MiB, search ≤64 MiB, migration ≤256 MiB. RFC 8785 report is timestamp-free and includes `authoritative`; JSON size is deterministic ≤8 KiB/vector +1 KiB/document. Any miss fails/suppresses profiles; unit tests inject metrics. Single PR stays within 800 lines.

## Threat Matrix

N/A — no routing, shell, subprocess, VCS/PR, executable-classification, network-exposure, or process-integration change.

## Migration / Rollout

V1 is rebuilt. V2 uses `cybertron-spago-v1`; rollback deletes it so v0.5.x rebuilds `cybertron` metadata. Failed historical migration restores Markdown through rollback-backed sync.

## Open Questions

None.
