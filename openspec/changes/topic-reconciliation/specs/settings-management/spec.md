# Spec Delta: settings-management

> Extends `internal/tui/settings.go`, `internal/setup`. No prior
> `openspec/specs/settings-management/` exists — this is a brand-new
> capability domain in OpenSpec terms; the requirement below is `ADDED`,
> even though the proposal originally labeled this capability "Modified"
> relative to existing (pre-SDD) code. Backfilled after design revision 3.

## ADDED Requirements

### Requirement: Configurable thresholds and backend selection
TUI Settings MUST allow editing `merge_threshold` and `new_topic_threshold`,
and MAY allow selecting `embedding_backend`, persisted via `internal/setup`
(`Values`) and `config.yaml`, following the existing pointer-bound
`settingsValues`/`settingsStep` pattern.

#### Scenario: User edits thresholds
- GIVEN the Settings TUI's threshold step
- WHEN the user submits new merge/new-topic threshold values
- THEN `config.yaml` is updated and subsequent reconciliation uses the new values after service restart
