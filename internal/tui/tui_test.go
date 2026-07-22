package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/status"
)

func testRoot(s screen) rootModel {
	return rootModel{
		screen: s,
		menu:   newMenu(),
		dash:   model{cfg: &config.Config{AnalyzerBackend: "kimi"}},
	}
}

// The dashboard's poll must keep running while the user is on another
// screen, so its data is already current when they come back.
func TestRootRoutesDashboardMsgsWhileAway(t *testing.T) {
	for _, s := range []screen{screenMenu, screenSettings} {
		m := testRoot(s)
		snap := &status.Snapshot{ProcessedSession: 7}

		nm, _ := m.Update(dataMsg(dashboardData{snap: snap, processedTotal: 42}))
		root := nm.(rootModel)

		if root.dash.data.processedTotal != 42 {
			t.Errorf("screen %d: dashboard data not updated (processedTotal = %d, want 42)",
				s, root.dash.data.processedTotal)
		}
		if root.screen != s {
			t.Errorf("screen changed from %d to %d on a data message", s, root.screen)
		}
	}
}

// Keys belong to the active screen only: a menu keypress must not move the
// dashboard's failure cursor and vice versa.
func TestRootRoutesKeysToActiveScreenOnly(t *testing.T) {
	m := testRoot(screenMenu)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	root := nm.(rootModel)

	if root.menu.cursor != 1 {
		t.Errorf("menu cursor = %d, want 1", root.menu.cursor)
	}
	if root.dash.failSel != 0 {
		t.Errorf("dashboard failSel = %d, want 0 (key should not reach it)", root.dash.failSel)
	}
}

func TestRootBackMsgReturnsToMenu(t *testing.T) {
	m := testRoot(screenDashboard)
	nm, _ := m.Update(backMsg{})
	if got := nm.(rootModel).screen; got != screenMenu {
		t.Errorf("screen = %d after backMsg, want screenMenu (%d)", got, screenMenu)
	}
}

func TestRootCtrlCQuitsFromEveryScreen(t *testing.T) {
	for _, s := range []screen{screenMenu, screenDashboard, screenSettings} {
		m := testRoot(s)
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if cmd == nil {
			t.Fatalf("screen %d: ctrl+c produced no command", s)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("screen %d: ctrl+c did not quit", s)
		}
	}
}

// A settings save swaps the config both models read and clears the API-key
// alert, which is otherwise computed once at startup.
func TestRootCfgReloadedRefreshesDashboard(t *testing.T) {
	m := testRoot(screenSettings)
	fresh := &config.Config{AnalyzerBackend: "claude"}

	nm, _ := m.Update(cfgReloadedMsg{cfg: fresh, apiKeyStored: true})
	root := nm.(rootModel)

	if root.dash.cfg.AnalyzerBackend != "claude" {
		t.Errorf("dashboard backend = %q, want claude", root.dash.cfg.AnalyzerBackend)
	}
	if !root.dash.apiKeySet {
		t.Error("apiKeySet is false after storing a key")
	}
	if root.screen != screenMenu {
		t.Errorf("screen = %d, want screenMenu after a save", root.screen)
	}
}
