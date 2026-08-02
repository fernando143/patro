# Spec Delta: embedbench

> Engineering tooling (non-shipping, informational). No prior
> `openspec/specs/embedbench/` exists; the requirement below is `ADDED`.
> Backfilled after design revision 3 (design D11, `tools/embedbench`).

## Purpose

`tools/embedbench` is a nested, non-shipping Go module used to compare the
three local-embeddings backends and calibrate the `topic-reconciliation`
merge/new-topic thresholds (D7). It is developer tooling, not a
user-facing capability, and MUST stay outside the release and CI surface.

## ADDED Requirements

### Requirement: embedbench excluded from release
`tools/embedbench` MUST NOT be included in `.goreleaser.yaml` build targets
and MUST NOT be reachable from the root module's `go build/vet/test ./...`
(separate `go.mod`). It is a developer-only backend comparison tool, not a
user-facing capability.

#### Scenario: Release build excludes embedbench
- GIVEN a tagged release build via `.goreleaser.yaml`
- WHEN the release artifacts are produced
- THEN no `embedbench` binary is present and CI's `go test ./...` does not execute its package
