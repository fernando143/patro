package tui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernando143/patro/internal/config"
)

// screen is the child model currently on display.
type screen int

const (
	screenMenu screen = iota
	screenDashboard
	screenSettings
)

// backMsg returns the program to the menu; cfgReloadedMsg carries a config
// re-read after a settings save.
type (
	backMsg        struct{}
	cfgReloadedMsg struct {
		cfg          *config.Config
		apiKeyStored bool
	}
)

// rootModel owns the Bubble Tea program and routes messages to whichever
// screen is on display.
type rootModel struct {
	screen     screen
	configPath string // the --config flag, "" when not given

	width, height int

	menu     menuModel
	dash     model
	settings settingsModel
}

// Run opens patro's TUI menu against cfg. configPath is echoed to
// subprocesses and used to reload the config after a settings change. It
// blocks until the user quits.
func Run(cfg *config.Config, configPath string) error {
	r := rootModel{
		screen:     screenMenu,
		configPath: configPath,
		menu:       newMenu(),
		dash:       newDashboard(cfg, configPath),
	}
	p := tea.NewProgram(r, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m rootModel) Init() tea.Cmd {
	// Only the dashboard has startup work; it polls from the very first
	// tick so the screen is already populated when the user opens it.
	return m.dash.Init()
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Every child stays sized so switching screens is instant.
		m.width, m.height = msg.Width, msg.Height
		return m, tea.Batch(m.updateDash(msg), m.updateMenu(msg), m.updateSettings(msg))

	case tea.KeyMsg:
		// Quit from anywhere, before any child sees the key. huh binds
		// ctrl+c to its own Quit, which would mark the settings form
		// aborted and leave it permanently inert.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m.routeToActive(msg)

	case backMsg:
		m.screen = screenMenu
		return m, nil

	case openDashboardMsg:
		m.screen = screenDashboard
		return m, nil

	case openSettingsMsg:
		m.screen = screenSettings
		// Always rebuild: a submitted or aborted huh.Form ignores every
		// later Update and renders an empty string, so a reused form
		// would come back dead.
		var cmd tea.Cmd
		m.settings, cmd = newSettings(m.dash.cfg, m.configPath, m.width, m.height)
		return m, cmd

	case cfgReloadedMsg:
		m.dash.cfg = msg.cfg
		if msg.apiKeyStored {
			m.dash.apiKeySet = true
		}
		m.screen = screenMenu
		// Repaint the dashboard now rather than waiting for the next tick.
		return m, m.dash.loadCmd()

	// Dashboard-owned messages are always delivered, so its one-second poll
	// keeps running while the user is elsewhere and the screen is already
	// current when they come back. (spinner.TickMsg is also used by huh's
	// Select for OptionsFunc loading; settings uses static options, so
	// there is no conflict — do not add OptionsFunc without revisiting.)
	case tickMsg, dataMsg, toastMsg, spinner.TickMsg:
		return m, m.updateDash(msg)
	}

	// Everything else — huh's internal field/group messages, the settings
	// save result — belongs to whichever screen produced it.
	return m.routeToActive(msg)
}

func (m rootModel) View() string {
	switch m.screen {
	case screenDashboard:
		return m.dash.View()
	case screenSettings:
		return m.settings.View()
	default:
		return m.menu.View()
	}
}

// routeToActive forwards msg to the screen currently on display.
func (m rootModel) routeToActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenDashboard:
		return m, m.updateDash(msg)
	case screenSettings:
		return m, m.updateSettings(msg)
	default:
		return m, m.updateMenu(msg)
	}
}

func (m *rootModel) updateDash(msg tea.Msg) tea.Cmd {
	nm, cmd := m.dash.Update(msg)
	m.dash = nm.(model)
	return cmd
}

func (m *rootModel) updateMenu(msg tea.Msg) tea.Cmd {
	nm, cmd := m.menu.Update(msg)
	m.menu = nm.(menuModel)
	return cmd
}

func (m *rootModel) updateSettings(msg tea.Msg) tea.Cmd {
	nm, cmd := m.settings.Update(msg)
	m.settings = nm.(settingsModel)
	return cmd
}
