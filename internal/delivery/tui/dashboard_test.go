package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernando143/patro/internal/adapter/status"
	"github.com/fernando143/patro/internal/platform/config"
)

func TestNewDashboard(t *testing.T) {
	cfg := &config.Config{AnalyzerBackend: "kimi"}
	m := newDashboard(cfg, "/tmp/config.yaml")

	if m.cfg != cfg {
		t.Error("newDashboard did not keep the given config")
	}
	if m.configPath != "/tmp/config.yaml" {
		t.Errorf("configPath = %q, want /tmp/config.yaml", m.configPath)
	}
	if !m.followLog {
		t.Error("followLog = false, want true on a fresh dashboard")
	}
}

func TestDashboardInitReturnsBatchedCommands(t *testing.T) {
	m := newDashboard(&config.Config{AnalyzerBackend: "kimi"}, "")
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned a nil command, want a batch of spinner/load/tick")
	}
}

func TestTickCmdProducesTickMsg(t *testing.T) {
	cmd := tickCmd()
	if cmd == nil {
		t.Fatal("tickCmd() = nil")
	}
	msg := cmd()
	if _, ok := msg.(tickMsg); !ok {
		t.Errorf("tickCmd() produced %T, want tickMsg", msg)
	}
}

func TestHandleKeyQuit(t *testing.T) {
	m := model{}
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
	}
	for _, key := range keys {
		_, cmd := m.handleKey(key)
		if cmd == nil {
			t.Fatalf("handleKey(%v) returned nil cmd, want tea.Quit", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("handleKey(%v) did not quit", key)
		}
	}
}

func TestHandleKeyEsc(t *testing.T) {
	m := model{}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("handleKey(esc) returned nil cmd, want backMsg")
	}
	if _, ok := cmd().(backMsg); !ok {
		t.Error("handleKey(esc) did not emit backMsg")
	}
}

func TestHandleKeyToggleFollow(t *testing.T) {
	m := model{followLog: true}
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	got := nm.(model)
	if got.followLog {
		t.Error("followLog still true after pressing f once")
	}
	nm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if !nm.(model).followLog {
		t.Error("followLog still false after pressing f twice")
	}
}

func TestHandleKeyRefreshTriggersLoad(t *testing.T) {
	m := model{cfg: &config.Config{AnalyzerBackend: "kimi"}}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("handleKey(r) returned nil cmd, want the load command")
	}
	msg := cmd()
	if _, ok := msg.(dataMsg); !ok {
		t.Errorf("handleKey(r) produced %T, want dataMsg", msg)
	}
}

func TestHandleKeyTabTogglesFocus(t *testing.T) {
	m := model{focus: focusLog}
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := nm.(model)
	if got.focus != focusFailures {
		t.Errorf("focus = %v, want focusFailures after tab", got.focus)
	}
	nm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if nm.(model).focus != focusLog {
		t.Errorf("focus = %v, want focusLog after a second tab", nm.(model).focus)
	}
}

func TestHandleKeyUpDownOnFailures(t *testing.T) {
	snap := &status.Snapshot{Failures: []status.Failure{{File: "a"}, {File: "b"}, {File: "c"}}}
	m := model{focus: focusFailures, data: dashboardData{snap: snap}, failSel: 1}

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if got := nm.(model).failSel; got != 2 {
		t.Errorf("failSel = %d after down, want 2", got)
	}
	nm, _ = nm.(model).handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if got := nm.(model).failSel; got != 2 {
		t.Errorf("failSel = %d after down at the end, want clamped at 2", got)
	}

	m = model{focus: focusFailures, data: dashboardData{snap: snap}, failSel: 1}
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if got := nm.(model).failSel; got != 0 {
		t.Errorf("failSel = %d after up, want 0", got)
	}
	nm, _ = nm.(model).handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if got := nm.(model).failSel; got != 0 {
		t.Errorf("failSel = %d after up at the start, want clamped at 0", got)
	}
}

func TestHandleKeyUpDownOnLogUnfollows(t *testing.T) {
	tests := []struct {
		name     string
		key      tea.KeyType
		atBottom bool
		wantY    int
	}{
		{name: "up", key: tea.KeyUp, atBottom: true, wantY: 1},
		{name: "down", key: tea.KeyDown, wantY: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{focus: focusLog, followLog: true, ready: true}
			m.log = viewport.New(80, 2)
			m.log.SetContent("one\ntwo\nthree\nfour")
			if tt.atBottom {
				m.log.GotoBottom()
			}

			nm, _ := m.handleKey(tea.KeyMsg{Type: tt.key})
			got := nm.(model)
			if got.followLog {
				t.Error("followLog still true after moving the log manually")
			}
			if got.log.YOffset != tt.wantY {
				t.Errorf("log YOffset = %d, want %d", got.log.YOffset, tt.wantY)
			}
		})
	}
}

func TestHandleKeyEnterOnFailuresCallsRetry(t *testing.T) {
	snap := &status.Snapshot{Failures: []status.Failure{{File: "clip.mkv"}}}
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-patro")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	m := model{
		focus:   focusFailures,
		data:    dashboardData{snap: snap},
		failSel: 0,
		cfg:     &config.Config{Inbox: dir},
		exePath: exe,
	}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("handleKey(enter) on a failure returned nil cmd")
	}
	msg := cmd()
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("handleKey(enter) produced %T, want toastMsg", msg)
	}
	if toast == "" {
		t.Error("retry toast is empty")
	}
}

func TestHandleKeyEnterOnLogIsNoop(t *testing.T) {
	m := model{focus: focusLog}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("handleKey(enter) on the log pane returned a non-nil cmd, want no-op")
	}
}

func TestOpenWebMissingExePath(t *testing.T) {
	m := model{}
	msg := m.openWeb()()
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("openWeb() produced %T, want toastMsg", msg)
	}
	if toast == "" {
		t.Error("toast message is empty for a missing exePath")
	}
}

func TestOpenWebLaunchesSubprocess(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-patro")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	m := model{exePath: exe, configPath: "/tmp/config.yaml"}
	msg := m.openWeb()()
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("openWeb() produced %T, want toastMsg", msg)
	}
	if toast == "" {
		t.Error("toast message is empty after launching the subprocess")
	}
}

func TestHandleKeyMTriggersReconcile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-patro")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	m := model{exePath: exe, configPath: "/tmp/config.yaml"}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if cmd == nil {
		t.Fatal("handleKey(m) returned nil cmd, want the reconcile command")
	}
	msg := cmd()
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("handleKey(m) produced %T, want toastMsg", msg)
	}
	if toast == "" {
		t.Error("reconcile toast is empty")
	}
}

func TestReconcileNowMissingExePath(t *testing.T) {
	m := model{}
	msg := m.reconcileNow()()
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("reconcileNow() produced %T, want toastMsg", msg)
	}
	if toast == "" {
		t.Error("toast message is empty for a missing exePath")
	}
}

func TestReconcileNowLaunchesSubprocess(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-patro")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	m := model{exePath: exe, configPath: "/tmp/config.yaml"}
	msg := m.reconcileNow()()
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("reconcileNow() produced %T, want toastMsg", msg)
	}
	if toast == "" {
		t.Error("toast message is empty after launching the subprocess")
	}
}

func TestDashboardMaintenanceNilSnapshot(t *testing.T) {
	d := dashboardData{}
	if got := d.maintenance(); got != nil {
		t.Errorf("maintenance() = %v, want nil for a nil snapshot", got)
	}
}

func TestDashboardMaintenanceReadsSnapshot(t *testing.T) {
	maint := &status.Maintenance{Phase: status.PhaseReconciling, Done: 2, Total: 5}
	d := dashboardData{snap: &status.Snapshot{Maintenance: maint}}
	if got := d.maintenance(); got != maint {
		t.Errorf("maintenance() = %v, want %v", got, maint)
	}
}

func TestRetrySelectedOutOfRangeIsNoop(t *testing.T) {
	m := model{data: dashboardData{snap: &status.Snapshot{}}, failSel: 5}
	if cmd := m.retrySelected(); cmd != nil {
		t.Error("retrySelected() with an out-of-range selection returned a non-nil cmd")
	}
}

func TestRetrySelectedMissingExePath(t *testing.T) {
	snap := &status.Snapshot{Failures: []status.Failure{{File: "clip.mkv"}}}
	m := model{data: dashboardData{snap: snap}, cfg: &config.Config{Inbox: t.TempDir()}}
	cmd := m.retrySelected()
	if cmd == nil {
		t.Fatal("retrySelected() returned nil cmd")
	}
	toast, ok := cmd().(toastMsg)
	if !ok {
		t.Fatalf("retrySelected() produced %T, want toastMsg", toast)
	}
	if toast == "" {
		t.Error("toast message is empty for a missing exePath")
	}
}

func TestDashboardFailuresNilSnapshot(t *testing.T) {
	d := dashboardData{}
	if got := d.failures(); got != nil {
		t.Errorf("failures() = %v, want nil for a nil snapshot", got)
	}
}
