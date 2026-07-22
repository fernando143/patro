package tui

import (
	"errors"
	"path/filepath"
	"sort"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/setup"
)

// The settings screen must offer exactly the backends config accepts —
// as a set, since the two deliberately use different display orders.
func TestSettingsBackendOptionsMatchConfig(t *testing.T) {
	var got []string
	for _, opt := range backendOptions {
		got = append(got, opt.Value)
	}
	want := config.ValidAnalyzerBackends()
	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("settings offers %v, config accepts %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("settings offers %v, config accepts %v", got, want)
		}
	}
}

func TestCurrentBinary(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "kimi",
			cfg:  &config.Config{AnalyzerBackend: "kimi", KimiPath: "/bin/kimi", ClaudePath: "/bin/claude"},
			want: "/bin/kimi",
		},
		{
			name: "claude",
			cfg:  &config.Config{AnalyzerBackend: "claude", KimiPath: "/bin/kimi", ClaudePath: "/bin/claude"},
			want: "/bin/claude",
		},
		{
			name: "lemur is hosted and has no binary",
			cfg:  &config.Config{AnalyzerBackend: "lemur", KimiPath: "/bin/kimi", ClaudePath: "/bin/claude"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := currentBinary(tc.cfg); got != tc.want {
				t.Errorf("currentBinary() = %q, want %q", got, tc.want)
			}
		})
	}
}

// newSettings must target the file config.Load resolved, not re-derive one:
// writing to a different path can move the state dir out from under serve.
func TestNewSettingsTargetsResolvedConfigPath(t *testing.T) {
	cfg := &config.Config{
		AnalyzerBackend: "kimi",
		KimiPath:        "/bin/kimi",
		Path:            "/etc/patro/config.yaml",
	}
	m, _ := newSettings(cfg, "", 100, 40)

	if m.target != "/etc/patro/config.yaml" {
		t.Errorf("target = %q, want the resolved config path", m.target)
	}
	if m.backend != "kimi" {
		t.Errorf("backend = %q, want the current backend preselected", m.backend)
	}
	if m.binaryPath != "/bin/kimi" {
		t.Errorf("binaryPath = %q, want the current backend's path", m.binaryPath)
	}
}

// Switching the backend must re-detect the CLI path and push it into the
// visible input. huh reads a bound value pointer only once, when Value is
// called, so the model has to re-seed the field itself.
func TestSettingsReseedsBinaryPathOnBackendChange(t *testing.T) {
	cfg := &config.Config{
		AnalyzerBackend: "kimi",
		KimiPath:        "/bin/kimi",
		Path:            filepath.Join(t.TempDir(), "config.yaml"),
	}
	m, _ := newSettings(cfg, "", 100, 40)
	if m.binaryPath != "/bin/kimi" {
		t.Fatalf("binaryPath = %q, want the seeded kimi path", m.binaryPath)
	}

	// Simulate the select moving to lemur, which is hosted and takes no path.
	m.backend = "lemur"
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = nm.(settingsModel)

	if m.binaryPath != "" {
		t.Errorf("binaryPath = %q after switching to lemur, want empty", m.binaryPath)
	}
	if m.lastBackend != "lemur" {
		t.Errorf("lastBackend = %q, want lemur", m.lastBackend)
	}

	// Switching to a CLI backend re-detects it from PATH.
	m.backend = "claude"
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = nm.(settingsModel)

	want := ""
	if found, err := setup.ResolveBinary("claude"); err == nil {
		want = found
	}
	if m.binaryPath != want {
		t.Errorf("binaryPath = %q after switching to claude, want %q (from PATH)", m.binaryPath, want)
	}
}

func TestSettingsViewDoesNotPanic(t *testing.T) {
	cfg := &config.Config{AnalyzerBackend: "kimi", KimiPath: "/bin/kimi", Path: "/tmp/config.yaml"}
	for _, size := range []struct{ w, h int }{{100, 40}, {60, 24}, {30, 10}} {
		for _, step := range []settingsStep{stepForm, stepSaving, stepResult} {
			m, _ := newSettings(cfg, "", size.w, size.h)
			m.step = step
			m.err = errors.New("something went wrong")
			if got := m.View(); got == "" {
				t.Errorf("%dx%d step %d: empty view", size.w, size.h, step)
			}
		}
	}
}
