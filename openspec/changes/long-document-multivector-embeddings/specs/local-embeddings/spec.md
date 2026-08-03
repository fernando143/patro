# Delta for Local Embeddings

## ADDED Requirements

### Requirement: Lossless tokenizer-aware chunking

The service MUST use the model's exact tokenizer. Cybertron MUST reserve `[CLS]`/`[SEP]`, leaving 510 payload tokens. Boundary precedence MUST be Markdown heading section → semantic block → sentence → tokenizer boundary. A semantic block is one paragraph, individual list item, fenced/indented code block, table row, or thematic block, in source order. Hard fallback MUST split only exact tokenizer indices; rune ranges MUST derive from token offsets for valid-UTF-8 slicing/metadata, never arbitrary rune windows.

The first chunk MAY contain 510 new tokens; later chunks MUST contain at most 478 new tokens prefixed by exactly 32 prior tokens, or all prior tokens when fewer exist. Generated context MUST count within 510.

A nonblank root title MUST be represented as `title` and removed from `content`; it MUST NOT be copied into every content chunk. Oversized titles MUST use the same lossless rules. No source span may be truncated or omitted.

#### Scenario: LE-01 Observed 609-position document has no loss

- GIVEN an exact-tokenizer fixture of 609 positions, including specials, with beginning, middle, and end markers
- WHEN the document is embedded
- THEN multiple encoder calls occur, each with at most 510 payload tokens and 512 total positions
- AND removing declared overlaps reconstructs every source span in order and all three markers reach the encoder

#### Scenario: LE-02 Hierarchy, overlap, and Unicode are deterministic

- GIVEN oversized Markdown containing sections, every semantic-block kind, sentences, and multibyte fallback runes
- WHEN it is chunked twice
- THEN both runs produce identical valid-UTF-8 spans using the required precedence and exact token boundaries
- AND every later chunk has the declared 32-token overlap without an empty or oversized chunk

### Requirement: Multi-vector identity and validity

Each result MUST carry document ID, source hash, backend, model identity, dimension, and representation fingerprint. `model_weights_sha256` MUST hash raw `spago_model.bin`. Hash strings MUST be lowercase hexadecimal.

The fingerprint MUST be SHA-256 of RFC 8785 canonical JSON containing exactly `schema_version:2`, `backend`, `model_id`, `model_version`, `model_weights_sha256`, `tokenizer_sha256`, `chunker_version:"md-wordpiece-510-478-32-v1"`, `normalization_version:"l2-f32-v1"`, and `dimension`. No alternate framing is valid. `tokenizer_sha256` MUST hash the RFC-8785 object `{"tokenizer_config_sha256":"<SHA256(raw tokenizer_config.json)>","vocab_sha256":"<SHA256(raw vocab.txt)>"}`, not concatenated files/hashes.

Each ordered vector MUST carry kind, zero-based ordinal, `token_count`, source span, and overlap; `(document ID, kind, ordinal)` MUST be unique. Vectors MUST be finite, dimension-correct, and L2-normalized to `1 ± 1e-5`; one invalid vector MUST fail the result.

#### Scenario: LE-03 Metadata and vectors are stable

- GIVEN the same Unicode document and model assets
- WHEN it is embedded twice
- THEN metadata, vector identities, ordering, token counts, source hash, and fingerprint are identical
- AND every vector satisfies dimension, finiteness, and unit-norm checks

#### Scenario: LE-04 Canonical identity is byte-stable and sensitive

- GIVEN reordered exact identity fields and variants changing weights, tokenizer, chunker, normalization, or dimension
- WHEN canonical identity JSON and fingerprints are produced
- THEN equal inputs produce byte-identical JSON/fingerprints and every variant produces a different fingerprint

### Requirement: Cancellation and failure are transactional

The service MUST check context before tokenization, before each chunk encode, and before returning. Cancellation or any chunk failure MUST return an explicit error and MUST NOT return a partial document representation.

#### Scenario: LE-05 Cancellation between chunks stops work

- GIVEN a multi-chunk input whose context is cancelled after the first encoder call
- WHEN the next chunk would be encoded
- THEN `context.Canceled` is returned, no later encoder call occurs, and no representation is returned

## TDD Traceability

Apply evidence MUST map LE-01 through LE-05 to recorded RED, GREEN, and REFACTOR runs; the final root gate MUST be `go test ./...`.
