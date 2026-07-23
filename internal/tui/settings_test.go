package tui

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/setup"
)

// pump drives a command the way the Bubble Tea runtime would, expanding
// batches, so the embedded form reaches the state it has on screen.
func pump(t *testing.T, m settingsModel, cmd tea.Cmd) settingsModel {
	t.Helper()
	for i := 0; i < 20 && cmd != nil; i++ {
		msg := cmd()
		if msg == nil {
			return m
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, bc := range batch {
				m = pump(t, m, bc)
			}
			return m
		}
		nm, next := m.Update(msg)
		m, cmd = nm.(settingsModel), next
	}
	return m
}

func newTestSettings(t *testing.T, cfg *config.Config) settingsModel {
	t.Helper()
	return newTestSettingsSized(t, cfg, 100, 40)
}

func newTestSettingsSized(t *testing.T, cfg *config.Config, w, h int) settingsModel {
	t.Helper()
	m, cmd := newSettings(cfg, "", w, h)
	return pump(t, m, cmd)
}

func kimiCfg(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		AnalyzerBackend: "kimi",
		KimiPath:        "/bin/kimi",
		Path:            filepath.Join(t.TempDir(), "config.yaml"),
	}
}

// The settings screen must offer exactly the backends config accepts —
// as a set, since the two deliberately use different display orders.
func TestSettingsBackendOptionsMatchConfig(t *testing.T) {
	var got []string
	for _, c := range backendChoices {
		got = append(got, c.value)
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

// Bubble Tea passes models by value, so huh accessors must be bound to
// storage that survives the copy. Binding them to model fields silently
// discarded every answer the user gave.
func TestSettingsBindingsSurviveModelCopy(t *testing.T) {
	m := newTestSettings(t, kimiCfg(t))

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(settingsModel)

	if m.vals.backend != "claude" {
		t.Fatalf("backend = %q after moving the select down, want claude "+
			"(form values are not reaching the live model)", m.vals.backend)
	}
}

func TestSettingsAdvancesThroughSteps(t *testing.T) {
	m := newTestSettings(t, kimiCfg(t))
	if m.step != stepBackend {
		t.Fatalf("step = %d, want stepBackend", m.step)
	}

	// Pick claude, then submit the backend step.
	m.vals.backend = "claude"
	m = pump(t, m, m.advance())
	if m.step != stepPath {
		t.Fatalf("step = %d after choosing claude, want stepPath", m.step)
	}

	m = pump(t, m, m.advance())
	if m.step != stepKey {
		t.Fatalf("step = %d, want stepKey", m.step)
	}

	// esc walks back the same way.
	m = pump(t, m, m.back())
	if m.step != stepPath {
		t.Fatalf("step = %d after esc, want stepPath", m.step)
	}
	m = pump(t, m, m.back())
	if m.step != stepBackend {
		t.Fatalf("step = %d after esc, want stepBackend", m.step)
	}
}

// lemur is hosted, so the CLI-path step is skipped in both directions.
func TestSettingsSkipsPathStepForHostedBackend(t *testing.T) {
	m := newTestSettings(t, kimiCfg(t))
	m.vals.backend = "lemur"

	m = pump(t, m, m.advance())
	if m.step != stepKey {
		t.Fatalf("step = %d for lemur, want stepKey (path step must be skipped)", m.step)
	}
	m = pump(t, m, m.back())
	if m.step != stepBackend {
		t.Fatalf("step = %d going back from lemur, want stepBackend", m.step)
	}
	if got := detectBinary("lemur"); got != "" {
		t.Errorf("detectBinary(lemur) = %q, want empty (hosted backends have no CLI)", got)
	}
}

// The path field is optional when detection succeeded and required when it
// did not, which is the whole point of showing the detection panel.
func TestSettingsPathIsOptionalOnlyWhenDetected(t *testing.T) {
	if err := optionalExecutable(""); err != nil {
		t.Errorf("optionalExecutable(\"\") = %v, want nil (blank keeps the detected path)", err)
	}
	if err := optionalExecutable("/nope/not-real"); err == nil {
		t.Error("optionalExecutable accepted a non-executable path")
	}
	if err := setup.ValidateExecutable(""); err == nil {
		t.Error("ValidateExecutable accepted a blank path; it is the required-field validator")
	}
}

func TestSettingsBinaryPathPrefersOverride(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("cannot create fake executable: %v", err)
	}

	m := newTestSettings(t, kimiCfg(t))
	m.detected = "/detected/claude"

	if got := m.binaryPath(); got != "/detected/claude" {
		t.Errorf("binaryPath() = %q, want the detected path when no override is set", got)
	}
	m.vals.customPath = exe
	if got := m.binaryPath(); got != exe {
		t.Errorf("binaryPath() = %q, want the override %q", got, exe)
	}
}

func TestCurrentBinary(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{"kimi", &config.Config{AnalyzerBackend: "kimi", KimiPath: "/bin/kimi", ClaudePath: "/bin/claude"}, "/bin/kimi"},
		{"claude", &config.Config{AnalyzerBackend: "claude", KimiPath: "/bin/kimi", ClaudePath: "/bin/claude"}, "/bin/claude"},
		{"lemur is hosted", &config.Config{AnalyzerBackend: "lemur", KimiPath: "/bin/kimi"}, ""},
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
	cfg := &config.Config{AnalyzerBackend: "kimi", KimiPath: "/bin/kimi", Path: "/etc/patro/config.yaml"}
	m := newTestSettings(t, cfg)

	if m.target != "/etc/patro/config.yaml" {
		t.Errorf("target = %q, want the resolved config path", m.target)
	}
	if m.vals.backend != "kimi" {
		t.Errorf("backend = %q, want the current backend preselected", m.vals.backend)
	}
}

// The size must reach the model: sizeForm measures the chrome by rendering it,
// so a narrow or short terminal exercises a different code path than the
// default 100x40.
func TestSettingsViewDoesNotPanic(t *testing.T) {
	steps := []settingsStep{stepBackend, stepPath, stepKey, stepSaving, stepResult}
	for _, size := range []struct{ w, h int }{{100, 40}, {60, 24}, {30, 10}, {15, 5}} {
		for _, step := range steps {
			for _, backend := range []string{"claude", "lemur"} {
				for _, detected := range []string{"", "/usr/bin/claude"} {
					m := newTestSettingsSized(t, kimiCfg(t), size.w, size.h)
					m.vals.backend = backend
					m.detected = detected
					m.err = errors.New("something went wrong")
					m = pump(t, m, m.enter(step))
					if got := m.View(); got == "" {
						t.Errorf("%dx%d step %d backend %s: empty view",
							size.w, size.h, step, backend)
					}
				}
			}
		}
	}
}
