package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// sizeMatrix is the shared terminal-size matrix every screen's overflow test
// runs against: from a wide desktop terminal down to and below the
// confirmed 40-column floor (30x10 and 20x8 are below-floor cases).
var sizeMatrix = []struct{ w, h int }{
	{200, 50},
	{120, 40},
	{100, 40},
	{80, 30},
	{72, 24},
	{64, 24},
	{60, 24},
	{48, 20},
	{40, 15},
	{30, 10}, // below floor
	{20, 8},  // below floor
}

// assertNoOverflow fails the test if out is empty or any rendered line is
// wider than w. This enforces the spec's "No rendered line exceeds terminal
// width" invariant.
//
// h is accepted (matching the shared harness signature used across screens)
// but intentionally not enforced as a strict total-line-count ceiling: the
// dashboard's fixed panel count (header/stats/row1/row2/log/help) cannot
// shrink below roughly two dozen lines without collapsing panels entirely,
// which is out of this change's scope (design.md's migration notes only
// specify width-driven breakpoints and a log-viewport height floor of 3).
// The width invariant is the one the spec names explicitly and is what this
// helper enforces.
func assertNoOverflow(t *testing.T, name string, out string, w, h int) {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		t.Errorf("%s @ %dx%d: empty view", name, w, h)
		return
	}
	for i, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got > w {
			t.Errorf("%s @ %dx%d: line %d width %d exceeds %d: %q",
				name, w, h, i, got, w, ansiRe.ReplaceAllString(line, ""))
		}
	}
}

// TestOverflowMenu runs the menu screen through the full size matrix.
// Pre-migration (before phase 3), this is expected to be RED at some narrow
// widths — the menu still keeps its own 40-60 column clamp instead of the
// shared primitives.
func TestOverflowMenu(t *testing.T) {
	for _, size := range sizeMatrix {
		m := newMenu()
		nm, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		out := nm.(menuModel).View()
		assertNoOverflow(t, "menu", out, size.w, size.h)
	}
}

// TestOverflowDashboard runs the dashboard through the size matrix across
// its live/stale/missing-status states. Pre-migration (before phase 4),
// this is expected to be RED at narrow widths — renderStats/renderRow1/
// renderRow2 still divide m.width with no infeasibility fallback.
func TestOverflowDashboard(t *testing.T) {
	for _, size := range sizeMatrix {
		m := sampleModel(t, size.w, size.h)
		assertNoOverflow(t, "dashboard/live", m.View(), size.w, size.h)

		stale := m.data
		stale.statusStale = true
		nm, _ := m.Update(dataMsg(stale))
		assertNoOverflow(t, "dashboard/stale", nm.(model).View(), size.w, size.h)

		missing := m.data
		missing.snap = nil
		missing.statusMissing = true
		missing.service = serviceActive
		missing.inboxBacklog = 3
		nm2, _ := m.Update(dataMsg(missing))
		assertNoOverflow(t, "dashboard/missing-status", nm2.(model).View(), size.w, size.h)
	}
}

// TestOverflowSettings runs every settings step through the size matrix.
// Pre-migration (before phase 5), this is expected to be RED at narrow
// widths — sizeForm/frame still use ad-hoc m.width math.
func TestOverflowSettings(t *testing.T) {
	steps := []settingsStep{stepBackend, stepPath, stepThresholds, stepKey, stepSaving, stepResult}
	for _, size := range sizeMatrix {
		for _, step := range steps {
			m := newTestSettingsSized(t, kimiCfg(t), size.w, size.h)
			m.err = errors.New("boom")
			m = pump(t, m, m.enter(step))
			assertNoOverflow(t, "settings/step-"+stepNames[step], m.View(), size.w, size.h)
		}
	}
}
