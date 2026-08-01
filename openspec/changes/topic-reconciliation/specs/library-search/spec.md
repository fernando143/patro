# Spec Delta: library-search

> No prior `openspec/specs/library-search/` exists — this is a brand-new
> capability domain; every requirement below is `ADDED`. Backfilled after
> design revision 3.

## Purpose

Hybrid search over the knowledge library: BM25 full text
(`internal/searchindex`, bleve, cgo-free) fused with cosine similarity
(`internal/vectors`), exposed as read-only `/search` in `internal/web`.

## ADDED Requirements

### Requirement: Derived, rebuildable index
Both `internal/searchindex` and `internal/vectors` MUST be derived
artifacts, never the source of truth. `Rebuild()` MUST reconstruct a
working index from `topics/*.md` + `meetings/*.md` alone.

#### Scenario: Rebuild from markdown only
- GIVEN `.state/search-index/` and `.state/vectors/` are deleted
- WHEN `Rebuild()` runs
- THEN both indexes are reconstructed and `/search` returns correct hits again

### Requirement: /search endpoint
`internal/web` MUST expose a read-only `/search` route returning ranked
hits across topics and meetings, fusing BM25 and cosine ranks (reciprocal-
rank fusion), rendered via the existing goldmark pipeline, with no external
JS/assets.

#### Scenario: Query returns hits from both sources
- GIVEN topics and meetings containing overlapping terms
- WHEN a user submits a `/search?q=...` request
- THEN results include entries from both `topics/` and `meetings/`, ranked by fused score
