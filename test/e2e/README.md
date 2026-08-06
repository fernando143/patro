# E2E test suite — brief

High-level design for evaluating patro's generated output quality. Scope: **design only**, no test logic implemented yet.

## Tooling

Stdlib `testing`, table-driven style (`[]struct{...}` + `t.Run`), matching the project convention already declared in the root `CLAUDE.md`. Each fixture directory below is one row of the table — the Go equivalent of a Gherkin `Scenario Outline` / `Examples:` table, without adding a new dependency.

## Suite 1 — Transcript → Meeting Summary

| Field | Definition |
|---|---|
| Input | `fixtures/transcripts/<case-id>/transcript.txt` |
| Output evaluated | Generated meeting note (summary, decisions, action items, assigned topic) |
| Golden reference | `fixtures/summaries/<case-id>/checklist.yaml` — static, hand-curated list of atomic facts (decisions, action items with owner, key data, expected topic) |
| Evaluation | Presence/absence of each checklist fact in the generated output — structured check, not free-text diff, no LLM judge involved |
| Scenarios | short/simple meeting, long multi-topic meeting, multiple crossing speakers, noisy/incomplete transcript, mixed language or jargon |

## Suite 2 — Meetings → Topics (creation, merge, matching)

| Field | Definition |
|---|---|
| Input | `fixtures/meetings/<case-id>/*.md` (new meeting notes) + pre-existing topic library state |
| Output evaluated | Topic created/updated, merge decision (new vs. update), similarity score used |
| Golden reference | `fixtures/topics/<case-id>/expected.yaml` — static, hand-curated categorical mapping (meeting → topic, merge yes/no) |
| Evaluation | Exact match against the expected mapping — categorical, no LLM judge involved |
| Scenarios | brand-new topic, meeting that updates an existing topic, two meetings on the same topic with different wording (must converge), similar-but-distinct topics (must NOT converge — false positive), borderline case near the similarity threshold |

## Why no LLM judge

Using an LLM to generate the summary/merge decision and another LLM to grade it shares bias — the judge tends to favor output with its own stylistic patterns even when a fact is missing or invented. Both suites use **static, human-curated golden references** instead: a fact checklist for Suite 1, a categorical mapping for Suite 2. Evaluation is deterministic and auditable.

## Layout

```
test/e2e/
├── README.md
├── fixtures/
│   ├── transcripts/<case-id>/transcript.txt
│   ├── summaries/<case-id>/checklist.yaml
│   ├── meetings/<case-id>/*.md
│   └── topics/<case-id>/expected.yaml
├── summary_test.go   (TBD — Suite 1 table-driven runner)
└── topics_test.go    (TBD — Suite 2 table-driven runner)
```

Fixture cases and the two `_test.go` runners are the next step, not yet implemented.
