# Search architecture

Patro uses **hybrid retrieval**: BM25 finds lexical term matches and multi-vector
representations find semantically similar topics. The two ranked lists are
combined with
**Reciprocal Rank Fusion (RRF)**. The old Markdown-wide lexical scoring pass is
not part of the search path anymore.

## At a glance

```mermaid
flowchart LR
    U[Browser\n/search?q=...] --> H[handleSearch]
    H --> R[rankedResults]
    R --> B[BM25 leg]
    R --> E[Multi-vector semantic leg]
    B --> BI[searchindex.Index.Query]
    BI --> IDX[(.state/search-index)]
    E --> ER[Embedder.Represent]
    ER --> NR[V2Store.NearestRepresentations]
    VS[(.state/vectors/topics.json)] --> NR
    B --> F[RRF fusion\nK = 60]
    NR --> F
    F --> P[Metadata + Markdown snippet]
    P --> O[Sorted HTML results]
```

The two legs have different responsibilities:

| Leg | Finds | Current coverage |
| --- | --- | --- |
| BM25 | Terms, names, and phrases present in the text | Topics **and** meetings |
| Multi-vector embeddings | Similar meaning, paraphrases, and related concepts | Topics |

BM25 and semantic **raw scores are not added together**. RRF combines their
rank positions, which avoids pretending that the two score scales are
calibrated.

## Query flow

```mermaid
sequenceDiagram
    participant B as Browser
    participant W as internal/web.Server
    participant I as searchindex.Index
    participant E as embed.Embedder
    participant V as vectors.V2Store
    participant F as RRF fusion

    B->>W: GET /search?q=roadmap
    W->>W: handleSearch()
    W->>W: rankedResults(ctx, query, kind)
    W->>I: bm25Hits(query)
    I-->>W: topic/meeting hits by BM25 rank
    W->>E: Represent(query document)
    E-->>W: complete multi-vector representation
    W->>V: NearestRepresentations(query)
    V-->>W: topic hits by multi-vector rank
    W->>F: add 1 / (60 + rank) per leg
    F-->>W: merged result IDs and scores
    W->>W: resultForID() + searchSnippet()
    W-->>B: HTML result list
```

### BM25 leg

1. `bm25Hits` opens the configured index path for the request.
2. `searchindex.Index.Query` runs Bleve's query-string BM25 search.
3. The result carries an ID such as `topic:roadmap` or
   `meeting:2026-01-05-kickoff`.
4. The index handle is closed after the leg finishes. This prevents a long
   running web viewer from holding Bleve's lock while ingestion publishes a
   rebuilt index.

### Multi-vector semantic leg

`semanticHits` uses the single supported semantic path:

```mermaid
flowchart TD
    Q[Query text] --> R[Embedder.Represent]
    R --> MV[V2Store.NearestRepresentations]
    MV --> T[Topic IDs]
```

The backend must implement `Represent`; there is no one-vector fallback.
`Represent` creates the query representation, and `V2Store` ranks complete
document representations. Semantic IDs are normalized to the same `topic:slug`
namespace used by BM25 before fusion. A semantic-only topic can therefore
appear even when BM25 did not return it.

## Reciprocal Rank Fusion

For a document `d`, the current implementation computes:

```text
RRF(d) = Σ 1 / (K + rank(d))
```

with `K = 60`. The code stores ranks zero-based, so the implementation uses
`1 / (60 + rank + 1)`.

Example:

| Document | BM25 rank | Semantic rank | Fused score |
| --- | ---: | ---: | ---: |
| `topic:roadmap` | 1 | 1 | `1/61 + 1/61` |
| `meeting:standup` | 2 | — | `1/62` |
| `topic:strategy` | — | 1 | `1/61` |

The final deterministic tie-breakers are:

1. Fused RRF score, descending.
2. Document date, newest first.
3. Document ID, ascending.

## Index freshness

The Markdown library is the source of truth. BM25 is a derived index rebuilt
after every successfully written video:

```mermaid
sequenceDiagram
    participant V as Video
    participant P as ProcessVideo
    participant L as Library
    participant M as Markdown library
    participant I as BM25 index
    participant S as Processed state

    V->>P: transcribe + analyze
    P->>L: AddMeetingCtx()
    L->>M: write transcript, meeting, topics
    P->>I: rebuildSearchIndex()
    I->>M: Rebuild(topics/*.md, meetings/*.md)
    I-->>I: build temporary Bleve index
    I-->>I: atomically publish replacement
    P->>S: MarkProcessed()
```

The order matters: if rebuilding BM25 fails, `MarkProcessed` is not reached, so
the video remains eligible for retry. `searchindex.Rebuild` reconstructs the
whole index from Markdown and removes stale documents instead of adding only
the newest file.

`patro serve` and `patro reconcile` also ensure both derived indexes: they sync
the multi-vector snapshot when it is missing or marked dirty and rebuild BM25
when its directory is missing. Manual edits to existing Markdown files are
outside the automatic video-processing path; remove the affected derived state
before running maintenance when those edits must be reflected immediately:
`.state/vectors/topics.json` for semantic retrieval and
`.state/search-index` for lexical retrieval.

### Multi-vector snapshot maintenance

The semantic snapshot is maintained separately from the per-video BM25
freshness boundary:

```mermaid
sequenceDiagram
    participant M as Maintenance
    participant E as embed.Embedder
    participant V as vectors.V2Store
    participant T as Topic Markdown

    M->>V: NeedsSync()
    alt missing or marked dirty
        M->>V: Sync(topics, embedder)
        V->>T: read topic Markdown
        V->>E: Represent(document)
        E-->>V: chunk vectors + identity
        V-->>M: atomically publish current snapshot
    else current snapshot
        V-->>M: reuse snapshot
    end
```

`patro process` refreshes BM25 immediately after writing a video. It does not
rebuild the complete semantic snapshot in that same critical path. An existing
semantic snapshot therefore remains unchanged until the next maintenance sync;
the newly written topic may be absent from semantic results while BM25 remains
available. If no usable snapshot exists, `V2Store` safe-fails only the semantic
leg and the BM25 leg still works.

## Code map

```mermaid
flowchart TB
    MAIN[cmd/patro/main.go\nwireSearch] --> WEB[internal/web/web.go]
    MAIN --> MAINT[runMaintenance\nserve/reconcile]
    WEB --> H[handleSearch]
    H --> RR[rankedResults]
    RR --> BM[bm25Hits]
    RR --> SEM[semanticHits]
    BM --> SIQ[searchindex.Index\nOpen / Query]
    SEM --> EMBQ[embed.Embedder\nRepresent]
    SEM --> VQ[V2Store\nNearestRepresentations]
    SIQ --> F[RRF fusion\nreciprocalRank]
    EMBQ --> VQ
    IDX[(.state/search-index)] -. opened by .-> SIQ
    VS[(.state/vectors/topics.json)] -. loaded by .-> VQ
    VQ --> F
    F --> META[metadata + Markdown snippets]
    MAINT --> SYNC[V2Store.Sync\nNeedsSync]
    SYNC --> EMBM[embed.Embedder\nRepresent]
    SYNC --> VS
    PIPE[internal/pipeline/pipeline.go\nProcessVideo] --> LIB[internal/library\nAddMeetingCtx]
    PIPE --> REF[rebuildSearchIndex]
    REF --> SIR[searchindex.Rebuild\ncollectDocs]
    MD[(knowledge/topics + meetings)] --> SIR
    SIR --> IDX
```

| Responsibility | File | Methods / symbols |
| --- | --- | --- |
| HTTP route and rendering | `internal/web/web.go` | `handleSearch`, `writeSearchFilters`, `resultForID`, `searchSnippet` |
| Hybrid ranking | `internal/web/web.go` | `rankedResults`, `bm25Hits`, `semanticHits`, `reciprocalRank` |
| Web wiring | `cmd/patro/main.go` | `wireSearch` |
| BM25 query | `internal/searchindex/index.go` | `Open`, `Query` |
| BM25 rebuild | `internal/searchindex/rebuild.go` | `Rebuild`, `collectDocs` |
| Video freshness boundary | `internal/pipeline/pipeline.go` | `ProcessVideo`, `rebuildSearchIndex` |
| Semantic backend | `internal/embed/embed.go`, `internal/embed/cybertron.go` | `Embedder`, `Represent`, `EncodeWindow` |
| Semantic retrieval and sync | `internal/vectors/sync.go` | `NewV2Store`, `NeedsSync`, `Sync`, `NearestRepresentations` |
| Maintenance boundary | `cmd/patro/main.go` | `runMaintenance`, `wireSearch` |

## What is intentionally not part of ranking?

```mermaid
flowchart LR
    MD[Markdown] --> SN[searchSnippet\npresentation only]
    MD --> REL[Explicit Markdown references\nrelated documents]
    MD -. not a scorer .-> RANK[Hybrid ranking]
```

- There is no second hand-written lexical score.
- Snippet generation does not create matches or alter ranking.
- “Related documents” are explicit Markdown links, not embedding results.
