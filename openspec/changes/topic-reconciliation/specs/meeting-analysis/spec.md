# Spec Delta: meeting-analysis

> Extends `internal/analyzer` + `internal/pipeline`. No prior
> `openspec/specs/meeting-analysis/` exists — this is a brand-new capability
> domain in OpenSpec terms; every requirement below is `ADDED`, even though
> the proposal originally labeled this capability "Modified" relative to
> existing (pre-SDD) code. Backfilled after design revision 3.

## ADDED Requirements

### Requirement: Recency-capped existing-topics prompt
`BuildPrompt` MUST receive a recency-bounded topics list
(`topic_prompt_limit`, default 50) computed by the caller
(`Library.ExistingTopicsRecent(n)` at the pipeline call site), instead of
the full unbounded topic list.

#### Scenario: Prompt size bounded as topics grow
- GIVEN a library with 500 topics
- WHEN a meeting is analyzed
- THEN the prompt's existing-topics block contains at most `topic_prompt_limit` entries, the most recently updated ones

### Requirement: Analyzer JSON contract unchanged
The analyzer request/response JSON contract shared by kimi/claude/lemur
backends MUST remain unchanged. `BuildPrompt` MUST stay a pure formatter
with no new fields required by callers.

#### Scenario: Existing analyzer tests pass unmodified
- GIVEN the current `analyzer_test.go` suite
- WHEN run against the changed pipeline
- THEN all cases pass without modification, and no backend requires new response fields
