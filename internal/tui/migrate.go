package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/migration"
)

type migrationAPI interface {
	BuildPlan(context.Context) (migration.Plan, error)
	Apply(context.Context, migration.Plan, []migration.Proposal) (migration.Result, error)
}

type migratePhase int

const (
	migrateLoading migratePhase = iota
	migrateReview
	migrateConfirm
	migrateApplying
	migrateResult
)

type proposalDecision int

const (
	decisionPending proposalDecision = iota
	decisionAccepted
	decisionRejected
)

type migrationLoadedMsg struct {
	service migrationAPI
	plan    migration.Plan
	err     error
}

type migrationAppliedMsg struct {
	result migration.Result
	err    error
}

type migrateModel struct {
	cfg           *config.Config
	service       migrationAPI
	plan          migration.Plan
	decisions     []proposalDecision
	phase         migratePhase
	cursor        int
	err           error
	result        migration.Result
	width, height int
	load          func() (migrationAPI, migration.Plan, error)
}

func newMigrate(cfg *config.Config, width, height int) migrateModel {
	m := migrateModel{cfg: cfg, width: width, height: height, phase: migrateLoading}
	m.load = func() (migrationAPI, migration.Plan, error) {
		service, err := migration.ConfiguredService(cfg)
		if err != nil {
			return nil, migration.Plan{}, err
		}
		plan, err := service.BuildPlan(context.Background())
		return service, plan, err
	}
	return m
}

func (m migrateModel) Init() tea.Cmd { return m.loadCmd() }

func (m migrateModel) loadCmd() tea.Cmd {
	load := m.load
	return func() tea.Msg {
		service, plan, err := load()
		return migrationLoadedMsg{service: service, plan: plan, err: err}
	}
}

func (m migrateModel) applyCmd() tea.Cmd {
	accepted := m.accepted()
	service, plan := m.service, m.plan
	return func() tea.Msg {
		result, err := service.Apply(context.Background(), plan, accepted)
		return migrationAppliedMsg{result: result, err: err}
	}
}

func (m migrateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case migrationLoadedMsg:
		m.service, m.plan, m.err = msg.service, msg.plan, msg.err
		m.decisions = make([]proposalDecision, len(msg.plan.Proposals))
		m.cursor = 0
		m.phase = migrateReview
		return m, nil
	case migrationAppliedMsg:
		m.result, m.err, m.phase = msg.result, msg.err, migrateResult
		return m, nil
	case tea.KeyMsg:
		return m.handleMigrateKey(msg.String())
	}
	return m, nil
}

func (m migrateModel) handleMigrateKey(key string) (tea.Model, tea.Cmd) {
	// Applying mutates Markdown and rebuilds both derived indexes. It is an
	// uninterruptible UI phase: only migrationAppliedMsg may advance it.
	if m.phase == migrateApplying {
		return m, nil
	}
	if key == "q" {
		return m, tea.Quit
	}
	if key == "esc" {
		if m.phase == migrateConfirm {
			m.phase = migrateReview
			return m, nil
		}
		return m, func() tea.Msg { return backMsg{} }
	}
	if m.phase == migrateLoading {
		return m, nil
	}
	if m.phase == migrateResult {
		if key == "r" || key == "enter" {
			m.phase, m.err = migrateLoading, nil
			return m, m.loadCmd()
		}
		return m, nil
	}
	if m.phase == migrateConfirm {
		switch key {
		case "y", "enter":
			m.phase = migrateApplying
			return m, m.applyCmd()
		case "n":
			m.phase = migrateReview
		}
		return m, nil
	}

	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor+1 < len(m.plan.Proposals) {
			m.cursor++
		}
	case "a", " ":
		if len(m.decisions) > 0 {
			m.decisions[m.cursor] = decisionAccepted
		}
	case "x":
		if len(m.decisions) > 0 {
			m.decisions[m.cursor] = decisionRejected
		}
	case "A":
		for i := range m.decisions {
			m.decisions[i] = decisionAccepted
		}
	case "r":
		m.phase, m.err = migrateLoading, nil
		return m, m.loadCmd()
	case "enter":
		if len(m.accepted()) > 0 {
			m.phase = migrateConfirm
		}
	}
	return m, nil
}

func (m migrateModel) counts() (accepted, rejected, pending int) {
	for _, d := range m.decisions {
		switch d {
		case decisionAccepted:
			accepted++
		case decisionRejected:
			rejected++
		default:
			pending++
		}
	}
	return
}

func (m migrateModel) accepted() []migration.Proposal {
	var accepted []migration.Proposal
	for i, d := range m.decisions {
		if d == decisionAccepted {
			accepted = append(accepted, m.plan.Proposals[i])
		}
	}
	return accepted
}

func (m migrateModel) View() string {
	if m.width < minRenderWidth {
		return "cargando migración…"
	}
	inner := innerWidth(m.width)
	var title, body, help string
	switch m.phase {
	case migrateLoading:
		title, body, help = "MIGRATE", "Computing historical topic plan…", "esc back · q quit"
	case migrateApplying:
		title, body, help = "APPLYING", "Validating hashes, backing up files, and rebuilding indexes…", "keys disabled · migration in progress"
	case migrateResult:
		title, help = "MIGRATION RESULT", "r/enter recompute · esc back · q quit"
		if m.err != nil {
			body = "Error: " + m.err.Error()
			if m.result.BackupDir != "" {
				body += "\nBackup: " + m.result.BackupDir
			}
		} else {
			body = fmt.Sprintf("Applied %d merge(s); removed %d source file(s).", m.result.Merged, m.result.Removed) +
				"\nFiles affected: " + fmt.Sprint(m.result.FilesAffected) + "\nBackup: " + m.result.BackupDir
		}
	case migrateConfirm:
		a, r, p := m.counts()
		title, help = "CONFIRM MIGRATION", "y/enter apply · n/esc cancel · q quit"
		if inner < 30 {
			body = fmt.Sprintf("MUTATION CONFIRM\nA%d R%d P%d\n%d topics + index\nBackup before write", a, r, p, a*2)
		} else {
			body = fmt.Sprintf("This will mutate the knowledge library.\nAccepted: %d  Rejected: %d  Pending: %d\nFiles affected: %d topic files + index.md\nA timestamped backup is created before any write.", a, r, p, a*2)
		}
	default:
		title, help = "MIGRATE", "↑↓ navigate · a accept · x reject · A accept all · r refresh · enter apply · esc back"
		body = m.reviewBody(inner)
	}
	subtitle := "historical topic migration · every merge requires approval"
	if m.height < 12 {
		subtitle = ""
	}
	title = truncate(title, inner-3)
	var frame func(string) string
	if m.height < 10 {
		frame = func(content string) string {
			return lipgloss.JoinVertical(lipgloss.Left,
				panelBox(title, colorMagenta, inner, content),
				styleHelp.Render(truncate(help, m.width)))
		}
	} else {
		chrome := screenChrome{width: m.width, height: m.height, subtitle: subtitle, help: help}
		frame = func(content string) string { return chrome.render(panelBox(title, colorMagenta, inner, content)) }
	}
	rows := m.height - reserveRows(frame)
	if rows < 1 {
		rows = 1
	}
	return frame(fitMigrationBody(body, inner-2, rows))
}

func (m migrateModel) reviewBody(width int) string {
	if m.err != nil {
		return "Error: " + m.err.Error()
	}
	if len(m.plan.Proposals) == 0 {
		return "No historical topic merges proposed.\nAll topics remain unchanged."
	}
	a, r, p := m.counts()
	proposal := m.plan.Proposals[m.cursor]
	status := []string{"PENDING", "ACCEPTED", "REJECTED"}[m.decisions[m.cursor]]
	if width < 30 {
		shortStatus := []string{"PEND", "ACPT", "RJCT"}[m.decisions[m.cursor]]
		return fmt.Sprintf("%d/%d %s A%dR%dP%d\n%s → %s\ncos %.4f\n%d/%d → %d/%d",
			m.cursor+1, len(m.plan.Proposals), shortStatus, a, r, p,
			proposal.SourceSlug, proposal.TargetSlug, proposal.Score,
			proposal.SourceBytes, proposal.SourceSections, proposal.TargetBytes, proposal.TargetSections)
	}
	return fmt.Sprintf("Proposal %d/%d [%s] · A%d R%d P%d\n%s (%s) → %s (%s)\nCosine similarity: %.4f\nImpact: source %d bytes/%d sections · target %d bytes/%d sections\nSource: %s\nTarget: %s\nSource sha256: %s\nTarget sha256: %s",
		m.cursor+1, len(m.plan.Proposals), status, a, r, p,
		proposal.SourceTitle, proposal.SourceSlug, proposal.TargetTitle, proposal.TargetSlug, proposal.Score,
		proposal.SourceBytes, proposal.SourceSections, proposal.TargetBytes, proposal.TargetSections,
		proposal.SourcePath, proposal.TargetPath, proposal.SourceHash, proposal.TargetHash)
}

func fitMigrationBody(body string, width, rows int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(body, "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for i := range lines {
		lines[i] = truncate(lines[i], width)
	}
	return strings.Join(lines, "\n")
}
