package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/status"
)

const (
	refreshInterval = time.Second
	logTailLines    = 400
	vpReserve       = 22 // rows reserved above the log viewport
)

// focusArea is the pane currently receiving up/down/enter.
type focusArea int

const (
	focusLog focusArea = iota
	focusFailures
)

// tickMsg fires on the refresh interval; dataMsg carries a fresh load;
// toastMsg is a transient status-line message.
type (
	tickMsg  time.Time
	dataMsg  dashboardData
	toastMsg string
)

// model is the dashboard's Bubble Tea state.
type model struct {
	cfg        *config.Config
	configPath string
	exePath    string
	apiKeySet  bool

	data    dashboardData
	spinner spinner.Model
	log     viewport.Model

	width, height int
	focus         focusArea
	failSel       int
	followLog     bool
	toast         string
	toastAt       time.Time
	ready         bool
}

// Run starts the dashboard TUI against cfg. configPath is echoed to
// subprocesses (retry, web viewer). It blocks until the user quits.
func Run(cfg *config.Config, configPath string) error {
	exe, _ := os.Executable()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorCyan)

	m := model{
		cfg:        cfg,
		configPath: configPath,
		exePath:    exe,
		apiKeySet:  strings.TrimSpace(os.Getenv(config.APIKeyEnvVar)) != "",
		spinner:    sp,
		followLog:  true,
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCmd(), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) loadCmd() tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		return dataMsg(loadData(cfg, logTailLines))
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		vpHeight := msg.Height - vpReserve
		if vpHeight < 3 {
			vpHeight = 3
		}
		if !m.ready {
			m.log = viewport.New(msg.Width-4, vpHeight)
			m.ready = true
		} else {
			m.log.Width = msg.Width - 4
			m.log.Height = vpHeight
		}
		m.refreshLog()
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.loadCmd(), tickCmd())

	case dataMsg:
		m.data = dashboardData(msg)
		if m.failSel >= len(m.data.failures()) {
			m.failSel = 0
		}
		m.refreshLog()
		return m, nil

	case toastMsg:
		m.toast = string(msg)
		m.toastAt = time.Now()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "f":
		m.followLog = !m.followLog
		if m.followLog {
			m.log.GotoBottom()
		}
		return m, nil
	case "r":
		return m, m.loadCmd()
	case "w", "o":
		return m, m.openWeb()
	case "tab":
		if m.focus == focusLog {
			m.focus = focusFailures
		} else {
			m.focus = focusLog
		}
		return m, nil
	case "up", "k":
		if m.focus == focusFailures {
			if m.failSel > 0 {
				m.failSel--
			}
			return m, nil
		}
		m.followLog = false
		m.log.LineUp(1)
		return m, nil
	case "down", "j":
		if m.focus == focusFailures {
			if m.failSel < len(m.data.failures())-1 {
				m.failSel++
			}
			return m, nil
		}
		m.followLog = false
		m.log.LineDown(1)
		return m, nil
	case "enter":
		if m.focus == focusFailures {
			return m, m.retrySelected()
		}
		return m, nil
	}
	return m, nil
}

// openWeb launches the web viewer as a detached subprocess.
func (m model) openWeb() tea.Cmd {
	return func() tea.Msg {
		if m.exePath == "" {
			return toastMsg("No puedo localizar el binario de patro")
		}
		args := []string{"run", "web"}
		if m.configPath != "" {
			args = append(args, "--config", m.configPath)
		}
		cmd := exec.Command(m.exePath, args...)
		if err := cmd.Start(); err != nil {
			return toastMsg("Error al iniciar el visor web: " + err.Error())
		}
		return toastMsg("Visor web iniciado en http://127.0.0.1:8765 (PID " + fmt.Sprint(cmd.Process.Pid) + ")")
	}
}

// retrySelected reprocesses the selected failed file via `patro process`.
func (m model) retrySelected() tea.Cmd {
	failures := m.data.failures()
	if m.failSel >= len(failures) {
		return nil
	}
	file := failures[m.failSel].File
	path := filepath.Join(m.cfg.Inbox, file)
	exe, configPath := m.exePath, m.configPath
	return func() tea.Msg {
		if exe == "" {
			return toastMsg("No puedo localizar el binario de patro")
		}
		args := []string{"process"}
		if configPath != "" {
			args = append(args, "--config", configPath)
		}
		args = append(args, path)
		cmd := exec.Command(exe, args...)
		if err := cmd.Start(); err != nil {
			return toastMsg("Error al reintentar " + file + ": " + err.Error())
		}
		return toastMsg("Reintentando " + file + " …")
	}
}

// refreshLog rebuilds the viewport content from the current log data.
func (m *model) refreshLog() {
	if !m.ready {
		return
	}
	var b strings.Builder
	for _, l := range m.data.log {
		if l.Raw != "" {
			b.WriteString(styleDim.Render(l.Raw))
			b.WriteByte('\n')
			continue
		}
		ts := styleDim.Render(l.Time)
		lvl := levelStyle(l.Level).Render(fmt.Sprintf("%-7s", l.Level))
		b.WriteString(ts + " " + lvl + " " + l.Message + "\n")
	}
	m.log.SetContent(strings.TrimRight(b.String(), "\n"))
	if m.followLog {
		m.log.GotoBottom()
	}
}

// failures returns the current snapshot's failures (nil-safe).
func (d dashboardData) failures() []status.Failure {
	if d.snap == nil {
		return nil
	}
	return d.snap.Failures
}
