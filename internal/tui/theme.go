// Package tui holds patro's terminal UIs: the menu, live status dashboard
// and settings screens behind `patro run tui`, plus the shared
// 80s-synthwave visual theme also used by the init wizard.
package tui

import "github.com/charmbracelet/lipgloss"

// Synthwave palette: neon magenta/cyan/purple over deep indigo, with sunset
// accents. Colors are 24-bit hex so they render faithfully on truecolor
// terminals and degrade gracefully elsewhere.
var (
	colorBg      = lipgloss.Color("#1a0b2e") // deep indigo background
	colorPanel   = lipgloss.Color("#2a1a4a") // slightly lighter panel fill
	colorMagenta = lipgloss.Color("#ff2e97") // hot neon pink
	colorCyan    = lipgloss.Color("#05d9e8") // neon cyan
	colorPurple  = lipgloss.Color("#b967ff") // neon purple
	colorSunset  = lipgloss.Color("#ff8a00") // sunset orange
	colorYellow  = lipgloss.Color("#ffd319") // neon yellow
	colorGreen   = lipgloss.Color("#05ffa1") // neon mint (success/active)
	colorRed     = lipgloss.Color("#ff3864") // neon red (failure)
	colorText    = lipgloss.Color("#f5e6ff") // near-white lavender
	colorDim     = lipgloss.Color("#8b7bb8") // muted lavender
)

// Shared styles.
var (
	styleBanner = lipgloss.NewStyle().
			Foreground(colorMagenta).Bold(true)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colorCyan).Italic(true)

	// panelStyle is the base neon-bordered box; panelWith recolors it.
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPurple).
			Padding(0, 1)

	statTitle = lipgloss.NewStyle().Foreground(colorDim).Bold(true)
	statValue = lipgloss.NewStyle().Foreground(colorText).Bold(true)

	styleActive   = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleInactive = lipgloss.NewStyle().Foreground(colorDim)
	styleAlert    = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	styleFail     = lipgloss.NewStyle().Foreground(colorRed)
	styleAccent   = lipgloss.NewStyle().Foreground(colorSunset).Bold(true)
	styleDim      = lipgloss.NewStyle().Foreground(colorDim)
	styleSelected = lipgloss.NewStyle().
			Foreground(colorBg).Background(colorMagenta).Bold(true)

	styleHelp = lipgloss.NewStyle().Foreground(colorDim)
)

// panelBox renders a titled neon panel with the given border color and inner
// content, sized to width (0 = natural width).
func panelBox(title string, border lipgloss.Color, width int, content string) string {
	titleStyle := lipgloss.NewStyle().Foreground(border).Bold(true)
	s := panelStyle.BorderForeground(border)
	if width > 0 {
		s = s.Width(width)
	}
	body := titleStyle.Render("▍"+title) + "\n" + content
	return s.Render(body)
}

// bannerText is the retro title shown at the top of the dashboard.
const bannerText = "▓▒░ P A T R O ░▒▓"

// levelStyle maps a log level to its neon color.
func levelStyle(level string) lipgloss.Style {
	switch level {
	case "ERROR":
		return lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	case "WARNING":
		return lipgloss.NewStyle().Foreground(colorYellow)
	case "INFO":
		return lipgloss.NewStyle().Foreground(colorCyan)
	default:
		return lipgloss.NewStyle().Foreground(colorDim)
	}
}
