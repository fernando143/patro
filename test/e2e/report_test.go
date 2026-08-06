package e2e

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// reportPath is where TestMain writes the Suite 1 report after every run —
// relative to the package directory, since that's `go test`'s working dir.
const reportPath = "summary_report.md"

// reportEntry is one recorded checklist assertion (pass or fail). Kept so
// the report can show expected vs. got without asking the reader to
// re-parse `go test -v` output.
type reportEntry struct {
	CaseID   string
	Backend  string
	Kind     string // "expected_topic", "decision", "action_item", "key_fact"
	Text     string // fixture's human-readable description of the fact
	Keywords [][]string
	Got      string
	Passed   bool
}

var (
	reportMu      sync.Mutex
	reportEntries []reportEntry
)

// recordCheck is called from assertSummaryChecklist for every checklist
// assertion. Subtests run with t.Parallel(), so writes are mutex-guarded.
func recordCheck(tc summaryCase, kind, text string, keywords [][]string, got string, passed bool) {
	reportMu.Lock()
	defer reportMu.Unlock()
	reportEntries = append(reportEntries, reportEntry{
		CaseID:   tc.caseID,
		Backend:  tc.backend,
		Kind:     kind,
		Text:     text,
		Keywords: keywords,
		Got:      got,
		Passed:   passed,
	})
}

// TestMain runs the suite as usual, then writes reportPath with every
// recorded checklist assertion.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := writeReport(reportPath); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: writing report %s: %v\n", reportPath, err)
	}
	os.Exit(code)
}

func writeReport(path string) error {
	reportMu.Lock()
	entries := append([]reportEntry(nil), reportEntries...)
	reportMu.Unlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CaseID != entries[j].CaseID {
			return entries[i].CaseID < entries[j].CaseID
		}
		if entries[i].Backend != entries[j].Backend {
			return entries[i].Backend < entries[j].Backend
		}
		return entries[i].Kind < entries[j].Kind
	})

	failed := 0
	for _, e := range entries {
		if !e.Passed {
			failed++
		}
	}

	var b strings.Builder
	b.WriteString("# Suite 1 report — Transcript to Meeting Summary\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "**Score: %s** (%d/%d checks passed, %d failed)\n\n", formatScore(len(entries)-failed, len(entries)), len(entries)-failed, len(entries), failed)
	fmt.Fprint(&b, scoresByCaseTable(entries))

	if failed > 0 {
		b.WriteString("## Failures\n\n")
		for _, e := range entries {
			if e.Passed {
				continue
			}
			fmt.Fprintf(&b, "### %s / %s — %s\n\n", e.CaseID, e.Backend, e.Kind)
			fmt.Fprintf(&b, "- **Expected**: %s\n", e.Text)
			fmt.Fprintf(&b, "- **Keywords**: `%v`\n", e.Keywords)
			b.WriteString("- **Got**:\n\n")
			fmt.Fprintf(&b, "  ```\n  %s\n  ```\n\n", strings.ReplaceAll(e.Got, "\n", "\n  "))
		}
	}

	b.WriteString("## All checks\n\n")
	b.WriteString("| Case | Backend | Kind | Result |\n|---|---|---|---|\n")
	for _, e := range entries {
		result := "PASS"
		if !e.Passed {
			result = "FAIL"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", e.CaseID, e.Backend, e.Kind, result)
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// formatScore renders passed/total as a 0-1 score with 2 decimals. This is
// a reporting number only — go test's pass/fail per check stays binary and
// fail-fast, unchanged by this score (see report's design notes: adding a
// score here does not relax any check).
func formatScore(passed, total int) string {
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", float64(passed)/float64(total))
}

// scoresByCaseTable renders a passed/total score per case+backend, so a
// reader can spot which combination is weakest without scanning every
// individual check row in "All checks".
func scoresByCaseTable(entries []reportEntry) string {
	type key struct{ caseID, backend string }
	order := []key{}
	totals := map[key]int{}
	passed := map[key]int{}
	for _, e := range entries {
		k := key{e.CaseID, e.Backend}
		if _, seen := totals[k]; !seen {
			order = append(order, k)
		}
		totals[k]++
		if e.Passed {
			passed[k]++
		}
	}

	var b strings.Builder
	b.WriteString("## Score by case\n\n")
	b.WriteString("| Case | Backend | Score | Passed/Total |\n|---|---|---|---|\n")
	for _, k := range order {
		fmt.Fprintf(&b, "| %s | %s | %s | %d/%d |\n", k.caseID, k.backend, formatScore(passed[k], totals[k]), passed[k], totals[k])
	}
	b.WriteString("\n")
	return b.String()
}
