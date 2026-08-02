package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernando143/patro/internal/migration"
)

type fakeMigrationAPI struct {
	plan    migration.Plan
	result  migration.Result
	err     error
	applied []migration.Proposal
}

func (f *fakeMigrationAPI) BuildPlan(context.Context) (migration.Plan, error) { return f.plan, f.err }
func (f *fakeMigrationAPI) Apply(_ context.Context, _ migration.Plan, accepted []migration.Proposal) (migration.Result, error) {
	f.applied = append([]migration.Proposal(nil), accepted...)
	return f.result, f.err
}

func migrationFixture() migration.Plan {
	return migration.Plan{Proposals: []migration.Proposal{
		{SourceSlug: "old-a", SourceTitle: "Old A", SourcePath: "/tmp/old-a.md", SourceHash: "aaaaaaaaaaaaaaaa", SourceBytes: 10, SourceSections: 1, TargetSlug: "a", TargetTitle: "A", TargetPath: "/tmp/a.md", TargetHash: "bbbbbbbbbbbbbbbb", TargetBytes: 20, TargetSections: 2, Score: .95},
		{SourceSlug: "old-b", SourceTitle: "Old B", SourcePath: "/tmp/old-b.md", SourceHash: "cccccccccccccccc", SourceBytes: 11, SourceSections: 1, TargetSlug: "b", TargetTitle: "B", TargetPath: "/tmp/b.md", TargetHash: "dddddddddddddddd", TargetBytes: 21, TargetSections: 2, Score: .93},
	}}
}

func loadedMigrate(t *testing.T) (migrateModel, *fakeMigrationAPI) {
	t.Helper()
	fake := &fakeMigrationAPI{plan: migrationFixture(), result: migration.Result{Merged: 1, Removed: 1, BackupDir: "/tmp/backup"}}
	m := migrateModel{phase: migrateLoading, width: 100, height: 40}
	nm, _ := m.Update(migrationLoadedMsg{service: fake, plan: fake.plan})
	return nm.(migrateModel), fake
}

func migrateKey(t *testing.T, m migrateModel, key string) (migrateModel, tea.Cmd) {
	t.Helper()
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return nm.(migrateModel), cmd
}

func TestMigrateDecisionsAndSelectiveApply(t *testing.T) {
	m, fake := loadedMigrate(t)
	m, _ = migrateKey(t, m, "a")
	m, _ = migrateKey(t, m, "j")
	m, _ = migrateKey(t, m, "x")
	if a, r, p := m.counts(); a != 1 || r != 1 || p != 0 {
		t.Fatalf("counts = %d/%d/%d", a, r, p)
	}
	m, _ = migrateKey(t, m, "enter")
	if m.phase != migrateConfirm {
		t.Fatalf("phase = %d", m.phase)
	}
	m, cmd := migrateKey(t, m, "y")
	if m.phase != migrateApplying || cmd == nil {
		t.Fatalf("apply did not start")
	}
	msg := cmd()
	nm, _ := m.Update(msg)
	m = nm.(migrateModel)
	if m.phase != migrateResult || len(fake.applied) != 1 || fake.applied[0].SourceSlug != "old-a" {
		t.Fatalf("selective apply = %+v", fake.applied)
	}
}

func TestMigrateAcceptAllRefreshAndErrorStates(t *testing.T) {
	m, _ := loadedMigrate(t)
	m, _ = migrateKey(t, m, "A")
	if a, _, _ := m.counts(); a != 2 {
		t.Fatalf("accepted = %d", a)
	}
	m.load = func() (migrationAPI, migration.Plan, error) { return nil, migration.Plan{}, errors.New("load failed") }
	m, cmd := migrateKey(t, m, "r")
	if m.phase != migrateLoading || cmd == nil {
		t.Fatal("refresh did not enter loading")
	}
	nm, _ := m.Update(cmd())
	m = nm.(migrateModel)
	if m.err == nil || !strings.Contains(m.View(), "load failed") {
		t.Fatalf("error view = %q", m.View())
	}
}

func TestMigrateConfirmationShowsImpact(t *testing.T) {
	m, _ := loadedMigrate(t)
	m, _ = migrateKey(t, m, "a")
	m, _ = migrateKey(t, m, "enter")
	view := m.View()
	for _, want := range []string{"Accepted: 1", "Rejected: 0", "Pending: 1", "Files affected: 2 topic files + index.md", "timestamped backup"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestMigrateViewShowsReviewDetailsAndEmptyPlan(t *testing.T) {
	m, _ := loadedMigrate(t)
	view := m.View()
	for _, want := range []string{"Old A", "A (a)", "0.9500", "/tmp/old-a.md", "10 bytes", "sha256"} {
		if !strings.Contains(view, want) {
			t.Errorf("review missing %q", want)
		}
	}
	nm, _ := m.Update(migrationLoadedMsg{service: &fakeMigrationAPI{}, plan: migration.Plan{}})
	if view := nm.(migrateModel).View(); !strings.Contains(view, "No historical topic merges proposed") {
		t.Fatalf("empty view = %q", view)
	}
}

func TestMigrateApplyingIgnoresAllKeys(t *testing.T) {
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyRunes, Runes: []rune("a")},
	}
	for _, key := range keys {
		t.Run(key.String(), func(t *testing.T) {
			m, _ := loadedMigrate(t)
			m.phase, m.cursor = migrateApplying, 1
			nm, cmd := m.Update(key)
			got := nm.(migrateModel)
			if cmd != nil {
				t.Fatalf("key %q returned a command during apply", key.String())
			}
			if got.phase != migrateApplying || got.cursor != 1 {
				t.Fatalf("key %q changed applying state: phase=%d cursor=%d", key.String(), got.phase, got.cursor)
			}
		})
	}
}

func TestMigrateViewsFitTerminal(t *testing.T) {
	sizes := []struct{ width, height int }{{20, 8}, {40, 15}, {80, 24}, {100, 40}}
	states := []struct {
		name string
		make func(t *testing.T) migrateModel
	}{
		{"loading", func(t *testing.T) migrateModel { m, _ := loadedMigrate(t); m.phase = migrateLoading; return m }},
		{"review", func(t *testing.T) migrateModel { m, _ := loadedMigrate(t); return m }},
		{"confirm", func(t *testing.T) migrateModel {
			m, _ := loadedMigrate(t)
			m.decisions[0] = decisionAccepted
			m.phase = migrateConfirm
			return m
		}},
		{"applying", func(t *testing.T) migrateModel { m, _ := loadedMigrate(t); m.phase = migrateApplying; return m }},
		{"result", func(t *testing.T) migrateModel {
			m, _ := loadedMigrate(t)
			m.phase = migrateResult
			m.result = migration.Result{Merged: 1, Removed: 1, FilesAffected: 3, BackupDir: "/tmp/a/long/backup/path"}
			return m
		}},
		{"error", func(t *testing.T) migrateModel {
			m, _ := loadedMigrate(t)
			m.phase = migrateResult
			m.err = errors.New("derived index rebuild failed after a long diagnostic message")
			return m
		}},
		{"empty", func(t *testing.T) migrateModel {
			m, _ := loadedMigrate(t)
			m.plan = migration.Plan{}
			m.decisions = nil
			return m
		}},
	}
	for _, state := range states {
		for _, size := range sizes {
			t.Run(state.name+fmt.Sprintf("/%dx%d", size.width, size.height), func(t *testing.T) {
				m := state.make(t)
				m.width, m.height = size.width, size.height
				view := m.View()
				if width := lipgloss.Width(view); width > size.width {
					t.Errorf("width = %d, want <= %d\n%s", width, size.width, view)
				}
				if height := lipgloss.Height(view); height > size.height {
					t.Errorf("height = %d, want <= %d\n%s", height, size.height, view)
				}
			})
		}
	}
}

func TestMigrateCompactViewsPreserveDecisionAndApplyState(t *testing.T) {
	m, _ := loadedMigrate(t)
	m.width, m.height = 20, 8
	view := m.View()
	for _, want := range []string{"1/2 PEND", "old-a → a", "cos 0.9500"} {
		if !strings.Contains(view, want) {
			t.Errorf("compact review missing %q:\n%s", want, view)
		}
	}

	m.phase = migrateApplying
	view = m.View()
	if strings.Contains(view, "q quit") {
		t.Errorf("applying help advertises disabled quit:\n%s", view)
	}
	if !strings.Contains(view, "keys disabled") {
		t.Errorf("applying help does not explain disabled keys:\n%s", view)
	}
}
