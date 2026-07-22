package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// openDashboardMsg and openSettingsMsg ask the root model to switch screens.
type (
	openDashboardMsg struct{}
	openSettingsMsg  struct{}
)

// menuItem is one selectable row.
type menuItem struct {
	title string
	desc  string
}

// menuModel is the landing screen: a short list of the things patro's TUI
// can do. Hand-rolled rather than bubbles/list, which would bring its own
// styling and filter keymap for three static rows.
type menuModel struct {
	items         []menuItem
	cursor        int
	width, height int
}

func newMenu() menuModel {
	return menuModel{
		items: []menuItem{
			{"Dashboard", "Live status: queue, in-flight job, failures, log"},
			{"Settings", "Analyzer backend and AssemblyAI API key"},
			{"Quit", "Exit patro"},
		},
	}
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
			return m, nil
		case "enter":
			return m, m.selectCmd()
		}
	}
	return m, nil
}

// selectCmd turns the highlighted row into the message the root acts on.
func (m menuModel) selectCmd() tea.Cmd {
	switch m.cursor {
	case 0:
		return func() tea.Msg { return openDashboardMsg{} }
	case 1:
		return func() tea.Msg { return openSettingsMsg{} }
	default:
		return tea.Quit
	}
}

func (m menuModel) View() string {
	if m.width < 20 {
		return "cargando…"
	}

	// The panel's border and padding add 4 columns around the content.
	inner := m.width/2 - 2
	if inner < 40 {
		inner = m.width - 4
	}
	if inner > 60 {
		inner = 60
	}

	var body strings.Builder
	for i, it := range m.items {
		label := " " + truncate(it.title, inner-2) + " "
		if i == m.cursor {
			body.WriteString(styleSelected.Render(label))
		} else {
			body.WriteString(styleAccent.Render(label))
		}
		body.WriteString("\n" + styleDim.Render("   "+truncate(it.desc, inner-4)))
		if i < len(m.items)-1 {
			body.WriteString("\n\n")
		}
	}

	// Narrow terminals get shorter copy: lipgloss.Place pads every line out
	// to the widest one, so a single overflowing line would push the whole
	// menu past the terminal width.
	subtitle, help := "transcribe · analyze · remember  ▓▒░", "↑↓ move · enter select · q quit"
	if lipgloss.Width(subtitle) > m.width {
		subtitle = "▓▒░"
	}
	if lipgloss.Width(help) > m.width {
		help = "↑↓ · enter · q"
	}

	box := lipgloss.JoinVertical(
		lipgloss.Left,
		styleBanner.Render(truncate(bannerText, m.width)),
		styleSubtitle.Render(subtitle),
		"",
		panelBox("MENU", colorMagenta, inner, body.String()),
		"",
		styleHelp.Render(help),
	)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
