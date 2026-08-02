# Spec Delta: local-embeddings

> No prior `openspec/specs/local-embeddings/` exists — this is a brand-new
> capability domain; every requirement below is `ADDED`. Backfilled after
> design revision 3.

## Purpose

In-binary vector generation with zero post-install network and zero new
secret, via a 3-backend registry (D9).

## ADDED Requirements

### Requirement: Embedder registry
`internal/embed` MUST expose `Available() []string` and `New(name)
(Embedder, error)` over three compiled-in backends (go-sentex, zerfoo,
cybertron/spago), all shipped in the production binary. `Embedder` MUST
expose `Embed(ctx,string)([]float32,error)`, `Dim()`, `Name()`.

#### Scenario: Config selects backend
- GIVEN `embedding_backend: zerfoo` in config.yaml
- WHEN the pipeline requests an embedder
- THEN `embed.New("zerfoo")` is used and its `Dim()`/`Name()` are recorded with produced vectors

#### Scenario: Unknown backend name
- GIVEN `embedding_backend: unknown-backend`
- WHEN config validation runs
- THEN startup fails with a clear validation error listing `Available()` names

### Requirement: No network, no secret after install
Embedding weights MUST be embedded at build time (`go:embed`). The system
MUST NOT perform network calls or require new secrets to produce embeddings
at runtime.

#### Scenario: Offline embedding
- GIVEN no network connectivity after install
- WHEN a meeting is embedded
- THEN embedding succeeds using only the embedded weights
