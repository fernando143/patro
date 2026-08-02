package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"

	"github.com/fernando143/patro/internal/status"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"negative clamps to zero", -5 * time.Second, "00:00"},
		{"zero", 0, "00:00"},
		{"seconds only", 42 * time.Second, "00:42"},
		{"minutes and seconds", 90 * time.Second, "01:30"},
		{"hours included", 2*time.Hour + 3*time.Minute + 4*time.Second, "2:03:04"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.d); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// TestListShownTruncationMath is a direct, exhaustive table test of
// listShown's total>n branch (the "+N más" math) and its two boundaries:
// total==n (no truncation) and total==n+1 (minimal truncation). It exercises
// both list-panel budgets used elsewhere in this package: n=12 (a tall
// terminal, listRows(60)==12) and n=4 (the below-floor case,
// listRows(20)==4).
func TestListShownTruncationMath(t *testing.T) {
	cases := []struct {
		name      string
		total, n  int
		wantShown int
		wantMore  int
	}{
		// n=12 (listRows(60) == 12).
		{"n12/under", 5, 12, 5, 0},
		{"n12/exact", 12, 12, 12, 0},
		{"n12/boundary+1", 13, 12, 11, 2},
		{"n12/well-over", 15, 12, 11, 4},

		// n=4 (listRows(20) == 4, the below-floor case).
		{"n4/under", 2, 4, 2, 0},
		{"n4/exact", 4, 4, 4, 0},
		{"n4/boundary+1", 5, 4, 3, 2},
		{"n4/well-over", 15, 4, 3, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shown, more := listShown(tc.total, tc.n)
			if shown != tc.wantShown || more != tc.wantMore {
				t.Errorf("listShown(%d, %d) = (%d, %d), want (%d, %d)",
					tc.total, tc.n, shown, more, tc.wantShown, tc.wantMore)
			}
			// Invariant: what is shown plus what is left over always
			// accounts for every item, and shown never exceeds n.
			if shown+more != tc.total {
				t.Errorf("listShown(%d, %d): shown+more = %d, want %d (total)",
					tc.total, tc.n, shown+more, tc.total)
			}
			if shown > tc.n {
				t.Errorf("listShown(%d, %d): shown = %d exceeds budget n=%d",
					tc.total, tc.n, shown, tc.n)
			}
		})
	}
}

// TestMaintenanceBodyPhases exercises maintenanceBody's every branch: idle
// with nothing pending, idle with flagged topics waiting, and both
// in-progress phases (rebuild -> reconcile, matching design D8's "one card,
// two phases" transition).
func TestMaintenanceBodyPhases(t *testing.T) {
	cases := []struct {
		name   string
		maint  *status.Maintenance
		flag   int
		wantIn []string
	}{
		{"idle nothing pending", nil, 0, []string{"sin temas pendientes"}},
		{"idle with flagged topics", nil, 3, []string{"3", "marcados"}},
		{"rebuilding index", &status.Maintenance{Phase: status.PhaseRebuildingIndex, Done: 40, Total: 500}, 0,
			[]string{"reconstruyendo índice", "40/500"}},
		{"reconciling flagged", &status.Maintenance{Phase: status.PhaseReconciling, Done: 2, Total: 7}, 0,
			[]string{"reconciliando", "2/7", "marcados"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := model{spinner: spinner.New(), data: dashboardData{
				snap:         &status.Snapshot{Maintenance: tc.maint},
				flaggedCount: tc.flag,
			}}
			out := ansiRe.ReplaceAllString(m.maintenanceBody(), "")
			for _, want := range tc.wantIn {
				if !strings.Contains(out, want) {
					t.Errorf("maintenanceBody() = %q, want it to contain %q", out, want)
				}
			}
		})
	}
}

// TestRenderRow1MaintenanceCardBesidesJobCard is task 8.7's proof: an
// in-flight video job (EN CURSO) and background maintenance progress
// (MANTENIMIENTO) are independent design D10/D12 states that must both be
// representable on screen at once — this renders both non-nil simultaneously
// and checks each card shows its own state, unaffected by the other.
func TestRenderRow1MaintenanceCardBesidesJobCard(t *testing.T) {
	m := sampleModel(t, 200, 40) // wide enough for EN CURSO/MANTENIMIENTO side by side
	d := m.data
	d.snap.Maintenance = &status.Maintenance{Phase: status.PhaseReconciling, Done: 2, Total: 7}
	d.flaggedCount = 7
	nm, _ := m.Update(dataMsg(d))
	m = nm.(model)

	out := ansiRe.ReplaceAllString(m.renderRow1(), "")

	// The in-flight job card (seeded by sampleModel: roadmap-review.mkv,
	// stage analyzing) must still render exactly as it would with no
	// maintenance running.
	if !strings.Contains(out, "roadmap-review.mkv") {
		t.Error("in-flight job card missing after adding a maintenance card")
	}
	if !strings.Contains(out, "analyzing") {
		t.Error("in-flight job stage missing after adding a maintenance card")
	}
	// The maintenance card must show the live phase/progress, not the idle
	// flagged-count summary — a live Maintenance always wins over the
	// idle-count fallback.
	if !strings.Contains(out, "reconciliando") {
		t.Error("maintenance card does not show the reconciling phase")
	}
	if !strings.Contains(out, "2/7") {
		t.Error("maintenance card does not show its progress")
	}
}

func makeRecentItems(count int) []status.Recent {
	items := make([]status.Recent, count)
	for i := range items {
		items[i] = status.Recent{Title: fmt.Sprintf("Meeting %02d", i)}
	}
	return items
}

func makeFailureItems(count int) []status.Failure {
	items := make([]status.Failure, count)
	for i := range items {
		items[i] = status.Failure{File: fmt.Sprintf("clip-%02d.mkv", i), Reason: "transcription failed"}
	}
	return items
}

// countBodyLines counts the non-empty rendered lines of a panel body: one
// per shown item, plus one more for the "+N más" indicator when present.
func countBodyLines(t *testing.T, body string) int {
	t.Helper()
	stripped := strings.TrimRight(ansiRe.ReplaceAllString(body, ""), "\n")
	if stripped == "" {
		return 0
	}
	return len(strings.Split(stripped, "\n"))
}

// TestRenderRecentBodyTruncates exercises listShown's total>n branch through
// its real caller, renderRecentBody, at both list budgets used in this
// package (n=12 for a tall terminal, n=4 for the below-floor case), plus the
// total==n and total==n+1 boundaries.
func TestRenderRecentBodyTruncates(t *testing.T) {
	cases := []struct {
		name      string
		total, n  int
		wantShown int
		wantMore  int
	}{
		{"n12/well-over", 15, 12, 11, 4},
		{"n12/boundary+1", 13, 12, 11, 2},
		{"n12/exact-no-truncation", 12, 12, 12, 0},
		{"n4/well-over", 15, 4, 3, 12},
		{"n4/boundary+1", 5, 4, 3, 2},
		{"n4/exact-no-truncation", 4, 4, 4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := &status.Snapshot{Recent: makeRecentItems(tc.total)}
			out := renderRecentBody(snap, tc.n, 40)
			stripped := ansiRe.ReplaceAllString(out, "")

			if got := strings.Count(stripped, "•"); got != tc.wantShown {
				t.Errorf("renderRecentBody(total=%d, n=%d): %d bullet lines, want %d",
					tc.total, tc.n, got, tc.wantShown)
			}

			wantIndicator := fmt.Sprintf("+%d más", tc.wantMore)
			if tc.wantMore > 0 {
				if !strings.Contains(stripped, wantIndicator) {
					t.Errorf("renderRecentBody(total=%d, n=%d): output %q missing indicator %q",
						tc.total, tc.n, stripped, wantIndicator)
				}
			} else if strings.Contains(stripped, "más") {
				t.Errorf("renderRecentBody(total=%d, n=%d): unexpected truncation indicator in %q",
					tc.total, tc.n, stripped)
			}

			wantLines := tc.wantShown
			if tc.wantMore > 0 {
				wantLines++
			}
			if got := countBodyLines(t, out); got != wantLines {
				t.Errorf("renderRecentBody(total=%d, n=%d): %d rendered lines, want %d (shown items + indicator, never exceeding n=%d)",
					tc.total, tc.n, got, wantLines, tc.n)
			}
		})
	}
}

// TestRenderFailuresBodyTruncates mirrors TestRenderRecentBodyTruncates for
// the Failures panel, whose body is built by model.renderFailuresBody.
func TestRenderFailuresBodyTruncates(t *testing.T) {
	cases := []struct {
		name      string
		total, n  int
		wantShown int
		wantMore  int
	}{
		{"n12/well-over", 15, 12, 11, 4},
		{"n12/boundary+1", 13, 12, 11, 2},
		{"n12/exact-no-truncation", 12, 12, 12, 0},
		{"n4/well-over", 15, 4, 3, 12},
		{"n4/boundary+1", 5, 4, 3, 2},
		{"n4/exact-no-truncation", 4, 4, 4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := model{data: dashboardData{snap: &status.Snapshot{Failures: makeFailureItems(tc.total)}}}
			out, title := m.renderFailuresBody(tc.n, 40)
			if title != "FALLADOS" {
				t.Errorf("renderFailuresBody title = %q, want %q (unfocused panel)", title, "FALLADOS")
			}
			stripped := ansiRe.ReplaceAllString(out, "")

			wantIndicator := fmt.Sprintf("+%d más", tc.wantMore)
			if tc.wantMore > 0 {
				if !strings.Contains(stripped, wantIndicator) {
					t.Errorf("renderFailuresBody(total=%d, n=%d): output %q missing indicator %q",
						tc.total, tc.n, stripped, wantIndicator)
				}
			} else if strings.Contains(stripped, "más") {
				t.Errorf("renderFailuresBody(total=%d, n=%d): unexpected truncation indicator in %q",
					tc.total, tc.n, stripped)
			}

			wantLines := tc.wantShown
			if tc.wantMore > 0 {
				wantLines++
			}
			if got := countBodyLines(t, out); got != wantLines {
				t.Errorf("renderFailuresBody(total=%d, n=%d): %d rendered lines, want %d (shown items + indicator, never exceeding n=%d)",
					tc.total, tc.n, got, wantLines, tc.n)
			}
		})
	}
}

// TestRenderRow2PanelHeightStableAcrossTruncation confirms the whole point of
// listRows/listShown/padLines working together: the RECIENTES/FALLADOS
// panels render at the same total height whether there are 2 items or 15,
// because padLines pads short lists up to n and listShown caps long lists at
// n-1 items plus the indicator — never letting the panel grow unbounded.
func TestRenderRow2PanelHeightStableAcrossTruncation(t *testing.T) {
	const w, h = 100, 60 // listRows(60) == 12

	few := sampleModel(t, w, h)
	fewData := few.data
	fewData.snap = &status.Snapshot{Recent: makeRecentItems(2), Failures: makeFailureItems(1)}
	nm, _ := few.Update(dataMsg(fewData))
	few = nm.(model)

	many := sampleModel(t, w, h)
	manyData := many.data
	manyData.snap = &status.Snapshot{Recent: makeRecentItems(15), Failures: makeFailureItems(15)}
	nm2, _ := many.Update(dataMsg(manyData))
	many = nm2.(model)

	fewHeight := strings.Count(few.renderRow2(), "\n")
	manyHeight := strings.Count(many.renderRow2(), "\n")
	if fewHeight != manyHeight {
		t.Errorf("renderRow2 height not stable: 2/1 items = %d lines, 15/15 items (truncated) = %d lines, want equal",
			fewHeight, manyHeight)
	}
}
