package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/setup"
)

// backendChoice is one analyzer backend as offered by the settings screen.
type backendChoice struct {
	value string
	label string
	// hosted backends run in AssemblyAI's cloud and need no local binary.
	hosted bool
}

// backendChoices are the backends the settings screen offers, in display
// order. The set must match config.ValidAnalyzerBackends.
var backendChoices = []backendChoice{
	{value: "kimi", label: "kimi   — local Kimi CLI"},
	{value: "claude", label: "claude — local Claude CLI"},
	{value: "lemur", label: "lemur  — hosted by AssemblyAI, no local CLI", hosted: true},
}

// backendOptions builds fresh huh options. huh mutates option state, so the
// slice must not be shared between forms.
func backendOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(backendChoices))
	for _, c := range backendChoices {
		opts = append(opts, huh.NewOption(c.label, c.value))
	}
	return opts
}

// isHosted reports whether backend runs in the cloud and needs no CLI path.
func isHosted(backend string) bool {
	for _, c := range backendChoices {
		if c.value == backend {
			return c.hosted
		}
	}
	return false
}

// settingsStep is the stage of the settings flow currently on screen. Each
// stage owns its own form, built when the stage is entered, so every field is
// seeded with values that are already known — nothing has to be patched into
// a form that is already on screen.
type settingsStep int

const (
	stepBackend settingsStep = iota
	stepPath
	stepKey
	stepSaving
	stepResult
)

// stepNames label the steps of the flow for the header.
var stepNames = map[settingsStep]string{
	stepBackend: "backend",
	stepPath:    "cli path",
	stepKey:     "api key & save",
}

// submitMsg fires when the current step's form is submitted; saveDoneMsg
// carries the result of writing the config and updating the service.
type (
	submitMsg   struct{}
	saveDoneMsg struct {
		cfg          *config.Config
		apiKeyStored bool
		err          error
	}
)

// settingsValues holds every value bound into a huh form.
//
// These live behind a pointer on purpose. Bubble Tea passes models by value,
// so binding huh's accessors to fields of the model itself would capture the
// address of a copy that is discarded after the current Update: the form
// would write the user's answers into a dead stack frame and the live model
// would never see them.
type settingsValues struct {
	backend    string
	customPath string
	apiKey     string
	confirm    bool
}

// settingsModel edits the analyzer backend and the AssemblyAI API key.
type settingsModel struct {
	form *huh.Form
	vals *settingsValues

	cfg        *config.Config
	configPath string // the --config flag
	target     string // the config file we actually write

	// detected is the backend CLI found on PATH, "" when the lookup failed.
	detected string

	step   settingsStep
	err    error
	width  int
	height int
}

// newSettings builds a fresh settings screen positioned at the first step.
func newSettings(cfg *config.Config, flagConfig string, width, height int) (settingsModel, tea.Cmd) {
	m := settingsModel{
		vals:       &settingsValues{backend: cfg.AnalyzerBackend},
		cfg:        cfg,
		configPath: flagConfig,
		width:      width,
		height:     height,
	}

	// Prefer the file config.Load actually resolved. Falling back to
	// ConfigPath can name a file that does not exist yet, which would move
	// the state dir on the next load — hence the warning in the header.
	m.target = cfg.Path
	if m.target == "" {
		m.target = setup.ConfigPath(flagConfig)
	}

	return m, m.enter(stepBackend)
}

// flow is the ordered list of steps this backend walks through — the single
// definition of the sequence, so navigation and the "step N/M" header can
// never disagree about it. A hosted backend has no CLI path to point at.
func (m settingsModel) flow() []settingsStep {
	if isHosted(m.vals.backend) {
		return []settingsStep{stepBackend, stepKey}
	}
	return []settingsStep{stepBackend, stepPath, stepKey}
}

// enter switches to step and builds the form that belongs to it.
func (m *settingsModel) enter(step settingsStep) tea.Cmd {
	m.step = step
	switch step {
	case stepBackend:
		m.form = m.backendForm()
	case stepPath:
		m.form = m.pathForm()
	case stepKey:
		m.form = m.keyForm()
	default:
		m.form = nil
		return nil
	}
	m.sizeForm()
	return m.form.Init()
}

// Each step builds its own form, so the theme and keymap would otherwise be
// rebuilt several times per visit. Both are immutable once built — huh's
// fields copy the structs they need out of them — so one instance is shared.
var (
	settingsTheme  = sync.OnceValue(SynthwaveHuhTheme)
	settingsKeyMap = sync.OnceValue(newSettingsKeyMap)
)

// newForm applies the shared theme and keymap to a step's groups.
func newForm(groups ...*huh.Group) *huh.Form {
	f := huh.NewForm(groups...).
		WithTheme(settingsTheme()).
		WithKeyMap(settingsKeyMap()).
		WithShowHelp(true)
	// Embedded forms get no submit/cancel commands of their own: huh only
	// assigns them (to tea.Quit / tea.Interrupt) inside its own Run.
	f.SubmitCmd = func() tea.Msg { return submitMsg{} }
	f.CancelCmd = func() tea.Msg { return backMsg{} }
	return f
}

func (m *settingsModel) backendForm() *huh.Form {
	return newForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Analyzer backend").
			Description("Which model writes the knowledge library.").
			Options(backendOptions()...).
			Value(&m.vals.backend),
	))
}

// pathForm asks for the CLI path. When the binary was detected the field is
// optional — an empty answer keeps the detected path — and it is only
// mandatory when auto-detection came up empty.
func (m *settingsModel) pathForm() *huh.Form {
	input := huh.NewInput().Value(&m.vals.customPath)

	if m.detected != "" {
		input.
			Title("Custom path (optional)").
			Description("Leave blank to use the detected binary above.").
			Placeholder(m.detected).
			Validate(optionalExecutable)
	} else {
		input.
			Title("Path to the " + m.vals.backend + " executable").
			Description("Auto-detection failed, so this one is required.\nExample: /usr/local/bin/" + m.vals.backend).
			Validate(setup.ValidateExecutable)
	}
	return newForm(huh.NewGroup(input))
}

func (m *settingsModel) keyForm() *huh.Form {
	return newForm(huh.NewGroup(
		huh.NewInput().
			Title("AssemblyAI API key").
			Description("Leave blank to keep the current key.\nStored in the service environment, never in config.yaml.").
			EchoMode(huh.EchoModePassword).
			Value(&m.vals.apiKey),
		huh.NewConfirm().
			Title("Save these settings?").
			Description(m.saveSummary()).
			Value(&m.vals.confirm),
	))
}

// optionalExecutable accepts an empty answer, and otherwise requires a real
// executable.
func optionalExecutable(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return setup.ValidateExecutable(s)
}

// newSettingsKeyMap disables the select's "/" filter. Once filtering is
// active huh binds esc to clearing the filter, which would swallow our back
// key — and Select.Filtering(false) does not unbind it.
func newSettingsKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Select.Filter = key.NewBinding(key.WithDisabled())
	return km
}

// detectBinary looks the backend CLI up on PATH, returning "" for hosted
// backends and when the lookup fails.
func detectBinary(backend string) string {
	if isHosted(backend) {
		return ""
	}
	path, err := setup.ResolveBinary(backend)
	if err != nil {
		return ""
	}
	return path
}

// binaryPath is the path that will be written: the user's override when they
// typed one, otherwise whatever was auto-detected. customPath is already
// expanded — advance does that when the path step is submitted.
func (m settingsModel) binaryPath() string {
	if m.vals.customPath != "" {
		return m.vals.customPath
	}
	return m.detected
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
		// esc walks back a step, and leaves the screen from the first one.
		// Safe to intercept: with the select filter disabled no huh field
		// binds esc.
		if msg.String() == "esc" {
			// Bind the command first: advance/back mutate m through a
			// pointer receiver, and the order in which a return statement
			// copies m relative to the call is not specified.
			cmd := m.back()
			return m, cmd
		}

	case submitMsg:
		cmd := m.advance()
		return m, cmd

	case saveDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			m.step, m.form = stepResult, nil
			return m, nil
		}
		cfg, stored := msg.cfg, msg.apiKeyStored
		return m, func() tea.Msg { return cfgReloadedMsg{cfg: cfg, apiKeyStored: stored} }
	}

	if m.form == nil {
		return m, nil
	}
	fm, cmd := m.form.Update(msg)
	m.form = fm.(*huh.Form)
	return m, cmd
}

// advance moves to the next step once the current one is submitted.
func (m *settingsModel) advance() tea.Cmd {
	switch m.step {
	case stepBackend:
		m.detected = detectBinary(m.vals.backend)
		if isHosted(m.vals.backend) {
			m.vals.customPath = ""
		}

	case stepPath:
		// Expand the override once, here, so binaryPath and the detection
		// panel never redo filesystem work on every frame.
		if custom := strings.TrimSpace(m.vals.customPath); custom != "" {
			m.vals.customPath = setup.ExpandPath(custom)
		} else {
			m.vals.customPath = ""
		}

	case stepKey:
		if !m.vals.confirm {
			return func() tea.Msg { return backMsg{} }
		}
		m.step, m.form = stepSaving, nil
		return m.saveCmd()
	}

	flow := m.flow()
	if i := slices.Index(flow, m.step); i >= 0 && i+1 < len(flow) {
		return m.enter(flow[i+1])
	}
	return nil
}

// back steps one screen backwards, leaving settings from the first step.
func (m *settingsModel) back() tea.Cmd {
	if m.step == stepSaving {
		// A save in flight cannot be cancelled; ignore the key.
		return nil
	}
	flow := m.flow()
	if i := slices.Index(flow, m.step); i > 0 {
		return m.enter(flow[i-1])
	}
	return func() tea.Msg { return backMsg{} }
}

// sizeForm keeps the embedded form matched to the window. huh only auto-sizes
// while its own width/height are zero, so once we set them we own sizing.
func (m *settingsModel) sizeForm() {
	if m.form == nil || m.width < 20 {
		return
	}
	width := m.width - 4
	if width < 20 {
		width = 20
	}
	// Measure the chrome rather than reserving a fixed number of rows: the
	// detection panel comes and goes and changes height. An empty body
	// contributes exactly one line to the frame.
	height := m.height - (lipgloss.Height(m.frame("")) - 1)
	if height < 6 {
		height = 6
	}
	m.form = m.form.WithWidth(width).WithHeight(height)
}

// saveSummary describes, in one place, exactly what the confirm step will do.
func (m settingsModel) saveSummary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "backend  %s", m.vals.backend)
	if !isHosted(m.vals.backend) {
		fmt.Fprintf(&b, "\ncli      %s", orDash(m.binaryPath()))
	}
	fmt.Fprintf(&b, "\nconfig   %s", m.target)
	b.WriteString("\nThe background service is restarted so the change takes effect.")
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// saveCmd writes the config and updates the service off the UI thread.
func (m settingsModel) saveCmd() tea.Cmd {
	target, flagConfig := m.target, m.configPath
	backend, binary := m.vals.backend, m.binaryPath()
	apiKey := strings.TrimSpace(m.vals.apiKey)
	backendChanged := backend != m.cfg.AnalyzerBackend ||
		(!isHosted(backend) && binary != currentBinary(m.cfg))

	return func() tea.Msg {
		if backendChanged {
			if !isHosted(backend) && binary == "" {
				return saveDoneMsg{err: fmt.Errorf("no %s executable found; enter its path", backend)}
			}
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

	switch m.step {
	case stepSaving:
		return m.frame(panelBox("SAVING", colorCyan, inner,
			styleAccent.Render("Writing config and restarting the service…")))
	case stepResult:
		return m.frame(panelBox("ERROR", colorRed, inner,
			styleFail.Render(truncate(m.err.Error(), inner*3))))
	default:
		return m.frame(m.form.View())
	}
}

// frame lays the chrome out around whatever the current step renders. It is
// the only definition of the layout: sizeForm measures it with an empty body
// to learn how many rows are left for the form.
func (m settingsModel) frame(body string) string {
	inner := m.width - 2
	sections := []string{
		styleBanner.Render(truncate(bannerText, m.width)),
		styleSubtitle.Render(m.stepLabel()),
		"",
	}
	if panel := m.detectionPanel(inner); panel != "" {
		sections = append(sections, panel)
	}
	sections = append(sections, body, "")
	if help := m.helpLine(); help != "" {
		sections = append(sections, styleHelp.Render(help))
	}
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// stepLabel names the current step so the flow's length is never a surprise.
func (m settingsModel) stepLabel() string {
	if m.step == stepSaving {
		return "settings · saving"
	}
	flow := m.flow()
	i := slices.Index(flow, m.step)
	if i < 0 {
		return "settings"
	}
	return fmt.Sprintf("settings · step %d/%d — %s", i+1, len(flow), stepNames[m.step])
}

// detectionPanel reports the auto-detection result, so the user always knows
// whether they have to supply a path themselves.
func (m settingsModel) detectionPanel(inner int) string {
	if m.step != stepPath && m.step != stepKey {
		return ""
	}
	if isHosted(m.vals.backend) {
		return panelBox("BACKEND", colorPurple, inner,
			styleDim.Render(m.vals.backend+" runs in AssemblyAI's cloud — no local CLI needed."))
	}

	var body, title string
	border := colorGreen
	if m.detected != "" {
		title = "DETECTED"
		body = styleActive.Render("✓ ") + styleAccent.Render(m.vals.backend) +
			styleDim.Render(" found at ") + truncate(m.detected, inner-24)
		if m.vals.customPath != "" && m.step == stepKey {
			body += "\n" + styleDim.Render("  overridden by ") + truncate(m.binaryPath(), inner-20)
		}
	} else {
		title = "NOT DETECTED"
		border = colorYellow
		body = styleAlert.Render("⚠ "+m.vals.backend+" was not found on PATH.") + "\n" +
			styleDim.Render("  Enter its full path below, or install it and reopen settings.")
	}
	return panelBox(title, border, inner, body)
}

func (m settingsModel) helpLine() string {
	switch m.step {
	case stepBackend:
		return "↑↓ choose · enter next · esc back to menu · ctrl+c quit"
	case stepPath:
		return "enter next · esc back to backend · ctrl+c quit"
	case stepSaving:
		return ""
	case stepResult:
		return "esc back to menu"
	default:
		return "tab move · enter confirm · esc back · ctrl+c quit"
	}
}
