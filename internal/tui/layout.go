package tui

import "github.com/charmbracelet/lipgloss"

// Layout primitives shared by menu, dashboard, and settings. Every screen
// composes its frame through these functions instead of keeping its own
// column-split or height math; breakpoints are derived from splitCols'
// feasibility rather than hardcoded thresholds.
const (
	maxContentWidth = 100 // wide terminals cap+center here (menu only)
	minRenderWidth  = 20  // below this, screens keep their "cargando…" bailout
	minCardInner    = 16  // dashboard stat card
	minPanelInner   = 30  // dashboard row1/row2 panel
)

// contentWidth caps w at maxContentWidth, so a wide terminal does not
// stretch centered content edge to edge.
func contentWidth(w int) int {
	return min(w, maxContentWidth)
}

// innerWidth converts a bordered panel's total width to its content width —
// 2 columns are lost to the border; padding lives inside that.
func innerWidth(total int) int {
	return total - 2
}

// splitCols divides width into n column TOTALS, distributing the remainder
// to the leftmost columns so the sum is always exactly width — no /n
// truncation loss. It returns nil when width cannot fit n columns at
// minInner (width < n*(minInner+2)); the caller must retry with fewer
// columns. splitCols never returns a column below minInner and never
// returns columns that would overflow width.
func splitCols(width, n, minInner int) []int {
	if n <= 0 || width < n*(minInner+2) {
		return nil
	}
	base, rem := width/n, width%n
	cols := make([]int, n)
	for i := range cols {
		cols[i] = base
		if i < rem {
			cols[i]++
		}
	}
	return cols
}

// reserveRows reports how many rows render consumes around a one-line body,
// by actually rendering it and measuring: Height(render(""))-1. Generalizes
// settings' original frame("") trick and replaces dashboard's vpReserve
// constant — both now measure the real chrome instead of guessing at it.
func reserveRows(render func(body string) string) int {
	return lipgloss.Height(render("")) - 1
}

// listRows returns how many items a growable list panel (Recent/Failures)
// should show for the given terminal height: 4 below the 32-row floor, then
// growing 1 row per 2 extra terminal rows, clamped to [4, 12].
func listRows(height int) int {
	if height < 32 {
		return 4
	}
	rows := 4 + (height-32)/2
	if rows > 12 {
		return 12
	}
	return rows
}

// screenChrome composes the banner/subtitle/panels/body/help shared by
// every screen. It holds only ints and strings — never a model pointer or a
// huh.Form — so it cannot reintroduce the dead-stack-frame bug settings
// worked around before this change.
type screenChrome struct {
	width, height int
	subtitle      string
	panels        []string // e.g. settings' detection panel
	help          string   // auto-downgraded (truncated) when too wide
	center        bool     // menu: center body inside contentWidth
}

// render assembles the frame around body.
func (c screenChrome) render(body string) string {
	width := c.width
	if c.center {
		width = contentWidth(c.width)
	}

	sections := []string{styleBanner.Render(truncate(bannerText, width))}
	if c.subtitle != "" {
		sections = append(sections, styleSubtitle.Render(truncate(c.subtitle, width)))
	}
	sections = append(sections, "")
	for _, p := range c.panels {
		if p != "" {
			sections = append(sections, p)
		}
	}
	sections = append(sections, body, "")
	if c.help != "" {
		sections = append(sections, styleHelp.Render(truncate(c.help, width)))
	}

	frame := lipgloss.JoinVertical(lipgloss.Left, sections...)
	if c.center {
		frame = lipgloss.Place(c.width, c.height, lipgloss.Center, lipgloss.Center, frame)
	}
	return frame
}
