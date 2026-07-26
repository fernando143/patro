package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestSplitColsSumEqualsWidth(t *testing.T) {
	cols := splitCols(82, 4, 16)
	if cols == nil {
		t.Fatal("splitCols(82, 4, 16) = nil, want non-nil")
	}
	sum := 0
	for _, c := range cols {
		sum += c
	}
	if sum != 82 {
		t.Errorf("sum = %d, want 82 (no width lost to /4 truncation)", sum)
	}
}

func TestSplitColsNilWhenInfeasible(t *testing.T) {
	// 40 < 4*(16+2)=72: cannot fit 4 columns at minInner=16.
	if cols := splitCols(40, 4, 16); cols != nil {
		t.Errorf("splitCols(40, 4, 16) = %v, want nil", cols)
	}
}

func TestSplitColsFeasibleHonorsMinInner(t *testing.T) {
	cols := splitCols(82, 4, 16)
	if len(cols) != 4 {
		t.Fatalf("len(cols) = %d, want 4", len(cols))
	}
	sum := 0
	for _, c := range cols {
		if c < 16 {
			t.Errorf("column width %d below minInner 16", c)
		}
		sum += c
	}
	if sum != 82 {
		t.Errorf("sum = %d, want 82", sum)
	}
}

func TestSplitColsMatrix(t *testing.T) {
	cases := []struct {
		width, n, minInner int
	}{
		{200, 4, 16},
		{82, 4, 16},
		{40, 4, 16},
		{40, 2, 16},
		{40, 1, 16},
	}
	for _, tc := range cases {
		cols := splitCols(tc.width, tc.n, tc.minInner)
		feasible := tc.width >= tc.n*(tc.minInner+2)

		if cols == nil {
			if feasible {
				t.Errorf("width=%d n=%d minInner=%d: got nil, want a feasible split",
					tc.width, tc.n, tc.minInner)
			}
			continue
		}
		if !feasible {
			t.Errorf("width=%d n=%d minInner=%d: got %v, want nil (infeasible)",
				tc.width, tc.n, tc.minInner, cols)
			continue
		}
		if len(cols) != tc.n {
			t.Errorf("width=%d n=%d: len(cols) = %d, want %d", tc.width, tc.n, len(cols), tc.n)
		}
		sum := 0
		for _, c := range cols {
			if c < tc.minInner {
				t.Errorf("width=%d n=%d minInner=%d: column %d below floor",
					tc.width, tc.n, tc.minInner, c)
			}
			sum += c
		}
		if sum != tc.width {
			t.Errorf("width=%d n=%d: sum = %d, want %d", tc.width, tc.n, sum, tc.width)
		}
	}
}

func TestContentWidthCapsAtMax(t *testing.T) {
	cases := []struct{ w, want int }{
		{200, 100},
		{100, 100},
		{40, 40},
	}
	for _, tc := range cases {
		if got := contentWidth(tc.w); got != tc.want {
			t.Errorf("contentWidth(%d) = %d, want %d", tc.w, got, tc.want)
		}
	}
}

func TestInnerWidthSubtractsBorder(t *testing.T) {
	if got := innerWidth(40); got != 38 {
		t.Errorf("innerWidth(40) = %d, want 38", got)
	}
}

func TestReserveRowsMeasuresEmptyBodyHeight(t *testing.T) {
	render := func(body string) string {
		if body == "" {
			return "line1\nline2\nline3"
		}
		return "line1\n" + body + "\nline3"
	}
	if got := reserveRows(render); got != 2 {
		t.Errorf("reserveRows = %d, want 2 (Height(render(\"\"))-1 = 3-1)", got)
	}
}

func TestReserveRowsTracksDifferentChrome(t *testing.T) {
	render := func(body string) string {
		if body == "" {
			return "a\nb\nc\nd\ne"
		}
		return "a\n" + body
	}
	if got := reserveRows(render); got != 4 {
		t.Errorf("reserveRows = %d, want 4 (Height(render(\"\"))-1 = 5-1)", got)
	}
}

func TestListRowsBelowFloorIsFour(t *testing.T) {
	for _, h := range []int{0, 15, 24, 31} {
		if got := listRows(h); got != 4 {
			t.Errorf("listRows(%d) = %d, want 4 (below the 32-row floor)", h, got)
		}
	}
}

func TestListRowsMonotonicAndClamped(t *testing.T) {
	prev := listRows(0)
	for h := 0; h <= 200; h += 4 {
		got := listRows(h)
		if got < prev {
			t.Fatalf("listRows(%d) = %d, decreased from %d — not monotonic", h, got, prev)
		}
		if got < 4 || got > 12 {
			t.Errorf("listRows(%d) = %d, want in [4, 12]", h, got)
		}
		prev = got
	}
	if got := listRows(200); got != 12 {
		t.Errorf("listRows(200) = %d, want 12 (clamped ceiling)", got)
	}
}

func TestScreenChromeRendersAllSectionsAtNarrowFloor(t *testing.T) {
	c := screenChrome{
		width:    40,
		height:   20,
		subtitle: "transcribe · analyze · remember  ▓▒░",
		panels:   []string{"PANEL-ONE", "PANEL-TWO"},
		help:     "↑↓ move · enter select · q quit",
	}
	out := c.render("BODY-CONTENT")
	if strings.TrimSpace(out) == "" {
		t.Fatal("screenChrome.render at width=40 produced an empty frame")
	}
	if !strings.Contains(out, "BODY-CONTENT") {
		t.Error("rendered frame dropped the body")
	}
	if !strings.Contains(out, "PANEL-ONE") || !strings.Contains(out, "PANEL-TWO") {
		t.Error("rendered frame dropped a panel")
	}
	// Help text must not disappear even when it has to be shortened to fit.
	if !strings.Contains(out, "↑↓") {
		t.Error("rendered frame dropped the help section entirely")
	}
}

func TestScreenChromeCentersWhenRequested(t *testing.T) {
	c := screenChrome{width: 100, height: 30, help: "q quit", center: true}
	out := c.render("BODY")
	if !strings.Contains(out, "BODY") {
		t.Fatal("centered chrome dropped the body")
	}
	for i, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got > 100 {
			t.Errorf("centered chrome line %d width %d exceeds 100", i, got)
		}
	}
}
