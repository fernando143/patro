package tui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/setup"
)

// settingsReserve is the number of rows the settings screen draws around the
// embedded form (banner, subtitle, blank lines, help).
const settingsReserve = 8

// backendOptions are the analyzer backends the settings form offers, in
// display order. The set must match config.ValidAnalyzerBackends.
var backendOptions = []huh.Option[string]{
	huh.NewOption("kimi   — local Kimi CLI", "kimi"),
	huh.NewOption("claude — local Claude CLI", "claude"),
	huh.NewOption("lemur  — hosted (AssemblyAI)", "lemur"),
}

// settingsStep is what the screen is doing right now.
type settingsStep int

const (
	stepForm settingsStep = iota
	stepSaving
	stepResult
)

// submitMsg fires when the form is submitted; saveDoneMsg carries the result
// of writing the config and updating the service.
type (
	submitMsg   struct{}
	saveDoneMsg struct {
		cfg          *config.Config
		apiKeyStored bool
		err          error
	}
)

// settingsModel edits the analyzer backend and the AssemblyAI API key.
type settingsModel struct {
	form      *huh.Form
	pathInput *huh.Input // kept so the backend switch can re-seed its value

	backend     string
	lastBackend string
	binaryPath  string
	apiKey      string
	confirm     bool

	cfg        *config.Config
	configPath string // the --config flag
	target     string // the config file we actually write

	step   settingsStep
	err    error
	result string

	width, height int
}

// newSettings builds a fresh settings screen. The root model calls this every
// time the screen is opened: a huh.Form that has been submitted or aborted
// ignores later updates and renders nothing, so forms are never reused.
func newSettings(cfg *config.Config, flagConfig string, width, height int) (settingsModel, tea.Cmd) {
	m := settingsModel{
		cfg:        cfg,
		configPath: flagConfig,
		width:      width,
		height:     height,
	}

	// Prefer the file config.Load actually resolved. Falling back to
	// ConfigPath can name a file that does not exist yet, which would move
	// the state dir on the next load — hence the warning note below.
	m.target = cfg.Path
	if m.target == "" {
		m.target = setup.ConfigPath(flagConfig)
	}
	m.backend = cfg.AnalyzerBackend
	m.lastBackend = cfg.AnalyzerBackend
	m.binaryPath = currentBinary(cfg)

	m.pathInput = huh.NewInput().
		Title("Path to the backend CLI").
		Description("Auto-detected on PATH. Edit it to use a different binary.").
		Value(&m.binaryPath).
		Validate(setup.ValidateExecutable)

	var groups []*huh.Group
	if cfg.Path == "" {
		groups = append(groups, huh.NewGroup(
			huh.NewNote().
				Title("No config file found").
				Description("Saving will create "+m.target+".\nRun `patro init` first if you want the full setup."),
		))
	}
	groups = append(groups,
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Analyzer backend").
				Description("Which model writes the knowledge library.").
				Options(backendOptions...).
				Value(&m.backend),
		),
		huh.NewGroup(m.pathInput).
			WithHideFunc(func() bool { return m.backend == "lemur" }),
		huh.NewGroup(
			huh.NewInput().
				Title("AssemblyAI API key").
				Description("Leave blank to keep the current key.\nStored in the service environment, never in config.yaml.").
				EchoMode(huh.EchoModePassword).
				Value(&m.apiKey),
			huh.NewConfirm().
				Title("Save?").
				Description("Writes "+m.target+" and restarts the background service.").
				Value(&m.confirm),
		),
	)

	m.form = huh.NewForm(groups...).
		WithTheme(SynthwaveHuhTheme()).
		WithKeyMap(settingsKeyMap()).
		WithShowHelp(true)

	// Embedded forms get no submit/cancel commands of their own: huh only
	// assigns them (to tea.Quit / tea.Interrupt) inside its own Run.
	m.form.SubmitCmd = func() tea.Msg { return submitMsg{} }
	m.form.CancelCmd = func() tea.Msg { return backMsg{} }

	m.sizeForm()
	return m, m.form.Init()
}

// settingsKeyMap disables the select's "/" filter. Once filtering is active
// huh binds esc to clearing the filter, which would swallow our back key —
// and Select.Filtering(false) does not unbind it.
func settingsKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Select.Filter = key.NewBinding(key.WithDisabled())
	return km
}

// currentBinary returns the CLI path configured for cfg's backend.
func currentBinary(cfg *config.Config) string {
	switch cfg.AnalyzerBackend {
	case "kimi":
		return cfg.KimiPath
	case "claude":
		return cfg.ClaudePath
	default:
		return ""
	}
}

func (m settingsModel) Init() tea.Cmd { return nil }

func (m settingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sizeForm()
		return m, nil

	case tea.KeyMsg:
		// esc backs out, discarding changes. Safe to intercept: with the
		// select filter disabled no huh field binds esc.
		if msg.String() == "esc" {
			return m, func() tea.Msg { return backMsg{} }
		}

	case submitMsg:
		if !m.confirm {
			return m, func() tea.Msg { return backMsg{} }
		}
		m.step = stepSaving
		return m, m.saveCmd()

	case saveDoneMsg:
		if msg.err != nil {
			m.step = stepResult
			m.err = msg.err
			return m, nil
		}
		cfg, stored := msg.cfg, msg.apiKeyStored
		return m, func() tea.Msg { return cfgReloadedMsg{cfg: cfg, apiKeyStored: stored} }
	}

	if m.step != stepForm || m.form == nil {
		return m, nil
	}

	fm, cmd := m.form.Update(msg)
	m.form = fm.(*huh.Form)

	// The path field is prefilled from the backend chosen in the same form.
	// huh binds input values by pointer but reads that pointer only once,
	// when Value is called, so re-calling it pushes the newly detected path
	// into the visible field.
	if m.backend != m.lastBackend {
		m.lastBackend = m.backend
		m.binaryPath = ""
		if m.backend != "lemur" {
			if path, err := setup.ResolveBinary(m.backend); err == nil {
				m.binaryPath = path
			}
		}
		m.pathInput.Value(&m.binaryPath)
	}
	return m, cmd
}

// sizeForm keeps the embedded form matched to the window. huh only auto-sizes
// while its own width/height are zero, so once we set them we own sizing.
func (m *settingsModel) sizeForm() {
	if m.form == nil || m.width == 0 {
		return
	}
	width := m.width - 4
	if width < 20 {
		width = 20
	}
	height := m.height - settingsReserve
	if height < 6 {
		height = 6
	}
	m.form = m.form.WithWidth(width).WithHeight(height)
}

// saveCmd writes the config and updates the service off the UI thread.
func (m settingsModel) saveCmd() tea.Cmd {
	target, flagConfig := m.target, m.configPath
	backend, binary := m.backend, m.binaryPath
	apiKey := strings.TrimSpace(m.apiKey)
	backendChanged := backend != m.cfg.AnalyzerBackend ||
		(backend != "lemur" && binary != currentBinary(m.cfg))

	return func() tea.Msg {
		if backendChanged {
			if err := setup.SetBackend(target, backend, binary); err != nil {
				return saveDoneMsg{err: err}
			}
		}

		stored := false
		switch {
		case apiKey != "":
			// SetAPIKey restarts the service, which also picks up any
			// config change made just above.
			if err := setup.SetAPIKey(apiKey); err != nil {
				return saveDoneMsg{err: err}
			}
			stored = true
		case backendChanged:
			// serve reads the config once at startup, so without this the
			// backend change would not take effect until the next restart.
			if err := setup.RestartService(); err != nil && !errors.Is(err, setup.ErrNoService) {
				return saveDoneMsg{err: err}
			}
		}

		cfg, err := config.Load(flagConfig)
		if err != nil {
			return saveDoneMsg{err: err}
		}
		return saveDoneMsg{cfg: cfg, apiKeyStored: stored}
	}
}

func (m settingsModel) View() string {
	if m.width < 20 {
		return "cargando…"
	}

	inner := m.width - 2
	header := lipgloss.JoinVertical(
		lipgloss.Left,
		styleBanner.Render(bannerText),
		styleSubtitle.Render("settings  ▓▒░"),
		"",
	)

	switch m.step {
	case stepSaving:
		body := panelBox("SAVING", colorCyan, inner,
			styleAccent.Render("Writing config and restarting the service…"))
		return lipgloss.JoinVertical(lipgloss.Left, header, body)

	case stepResult:
		body := panelBox("ERROR", colorRed, inner,
			styleFail.Render(m.err.Error())+"\n\n"+styleHelp.Render("esc back to menu"))
		return lipgloss.JoinVertical(lipgloss.Left, header, body)

	default:
		return lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			m.form.View(),
			"",
			styleHelp.Render("↑↓/tab move · enter next · esc back (discards changes) · ctrl+c quit"),
		)
	}
}
