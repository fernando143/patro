package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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

	inner := innerWidth(contentWidth(m.width))

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

	chrome := screenChrome{
		width:    m.width,
		height:   m.height,
		subtitle: "transcribe · analyze · remember  ▓▒░",
		help:     "↑↓ move · enter select · q quit",
		center:   true,
	}
	return chrome.render(panelBox("MENU", colorMagenta, inner, body.String()))
}
