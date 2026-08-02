# Spec Delta: maintenance-status

> Extends `internal/status`, `internal/tui`, `cmd/patro`. No prior
> `openspec/specs/maintenance-status/` exists — this is a brand-new
> capability domain; every requirement below is `ADDED`. Backfilled after
> design revision 3 (design D12).

## ADDED Requirements

### Requirement: Maintenance progress reporting
`status.Snapshot` MUST gain an optional, nil-safe `Maintenance` field
reporting `Phase` (`rebuilding-index`|`reconciling`), `Done`/`Total`, and
`StartedAt`. Progress updates MUST flush throttled (≥1% change or ≥250ms);
start/done MUST flush unconditionally. An old `status.json` without this
field MUST still unmarshal correctly.

#### Scenario: Rebuild progress visible
- GIVEN `patro reconcile` starts a rebuild of 540 topics
- WHEN the dashboard polls `status.json`
- THEN it shows phase `rebuilding-index` with increasing `Done/Total`, without one file write per topic

### Requirement: Unified maintenance action
The TUI MUST expose one `m` key that runs `patro reconcile`, performing
ensure-index (rebuild if missing/mismatched) then reconcile flagged topics,
shown as one card beside (not replacing) the in-flight job card.

#### Scenario: Flagged count and running state coexist
- GIVEN 7 flagged topics and an in-flight video job
- WHEN maintenance is triggered via `m`
- THEN the dashboard shows the flagged count and maintenance phase on one card, while the existing job card is unaffected
