package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	return &config.Config{BinaryPaths: map[string]string{"kimi": "/bin/kimi"},
		AnalyzerBackend: "kimi",
		Path:            filepath.Join(t.TempDir(), "config.yaml"),
	}
}

func TestSettingsInitIsNil(t *testing.T) {
	m := newTestSettings(t, kimiCfg(t))
	if cmd := m.Init(); cmd != nil {
		t.Error("settingsModel.Init() = non-nil, want nil (enter() drives startup work instead)")
	}
}

// The settings screen and config validation now read the same registry, so
// this asserts the derivation rather than comparing two hand-kept lists.
// The invariant it used to guard by hand — a comment asking maintainers to
// keep backendChoices in sync with config.ValidAnalyzerBackends — is gone.
func TestSettingsBackendOptionsMatchConfig(t *testing.T) {
	var got []string
	for _, opt := range backendOptions() {
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
			t.Errorf("settings offers %v, config accepts %v", got, want)
			break
		}
	}
}

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
	if m.step != stepThresholds {
		t.Fatalf("step = %d, want stepThresholds", m.step)
	}

	m = pump(t, m, m.advance())
	if m.step != stepKey {
		t.Fatalf("step = %d, want stepKey", m.step)
	}

	// esc walks back the same way.
	m = pump(t, m, m.back())
	if m.step != stepThresholds {
		t.Fatalf("step = %d after esc, want stepThresholds", m.step)
	}
	m = pump(t, m, m.back())
	if m.step != stepPath {
		t.Fatalf("step = %d after esc, want stepPath", m.step)
	}
	m = pump(t, m, m.back())
	if m.step != stepBackend {
		t.Fatalf("step = %d after esc, want stepBackend", m.step)
	}
}

// lemur is hosted, so the CLI-path step is skipped in both directions, but
// the (backend-agnostic) thresholds step still runs.
func TestSettingsSkipsPathStepForHostedBackend(t *testing.T) {
	m := newTestSettings(t, kimiCfg(t))
	m.vals.backend = "lemur"

	m = pump(t, m, m.advance())
	if m.step != stepThresholds {
		t.Fatalf("step = %d for lemur, want stepThresholds (path step must be skipped)", m.step)
	}
	m = pump(t, m, m.advance())
	if m.step != stepKey {
		t.Fatalf("step = %d for lemur, want stepKey", m.step)
	}
	m = pump(t, m, m.back())
	if m.step != stepThresholds {
		t.Fatalf("step = %d going back from stepKey, want stepThresholds", m.step)
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
		{"kimi", &config.Config{BinaryPaths: map[string]string{"kimi": "/bin/kimi", "claude": "/bin/claude"}, AnalyzerBackend: "kimi"}, "/bin/kimi"},
		{"claude", &config.Config{BinaryPaths: map[string]string{"kimi": "/bin/kimi", "claude": "/bin/claude"}, AnalyzerBackend: "claude"}, "/bin/claude"},
		{"codex", &config.Config{BinaryPaths: map[string]string{"codex": "/bin/codex"}, AnalyzerBackend: "codex"}, "/bin/codex"},
		{"lemur is hosted", &config.Config{BinaryPaths: map[string]string{"kimi": "/bin/kimi"}, AnalyzerBackend: "lemur"}, ""},
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
	cfg := &config.Config{BinaryPaths: map[string]string{"kimi": "/bin/kimi"}, AnalyzerBackend: "kimi", Path: "/etc/patro/config.yaml"}
	m := newTestSettings(t, cfg)

	if m.target != "/etc/patro/config.yaml" {
		t.Errorf("target = %q, want the resolved config path", m.target)
	}
	if m.vals.backend != "kimi" {
		t.Errorf("backend = %q, want the current backend preselected", m.vals.backend)
	}
}

func TestSettingsViewShowsResolvedConfigPath(t *testing.T) {
	path := "/home/test/.config/patro/config.yaml"
	cfg := &config.Config{BinaryPaths: map[string]string{"kimi": "/bin/kimi"}, AnalyzerBackend: "kimi", Path: path}
	m := newTestSettings(t, cfg)

	if got := m.View(); !strings.Contains(got, path) {
		t.Errorf("settings view does not show resolved config path %q: %s", path, got)
	}
}

func TestValidateThreshold(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"0", false}, {"1", false}, {"0.9", false}, {"0.70", false},
		{"", true}, {"not a number", true}, {"-0.1", true}, {"1.1", true},
	}
	for _, tc := range cases {
		err := validateThreshold(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateThreshold(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
		}
	}
}

func TestValidatePositiveInt(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"1", false}, {"50", false},
		{"0", true}, {"-1", true}, {"", true}, {"abc", true}, {"1.5", true},
	}
	for _, tc := range cases {
		err := validatePositiveInt(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("validatePositiveInt(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
		}
	}
}

// The 3-band reconciler needs merge > new-topic to have a gray zone at all
// (spec's three-band table) — the thresholds step must reject the inverse.
func TestValidateThresholdOrder(t *testing.T) {
	if err := validateThresholdOrder("0.90", "0.70"); err != nil {
		t.Errorf("validateThresholdOrder(0.90, 0.70) = %v, want nil", err)
	}
	if err := validateThresholdOrder("0.70", "0.90"); err == nil {
		t.Error("validateThresholdOrder(0.70, 0.90) = nil, want an error (no gray zone left)")
	}
	if err := validateThresholdOrder("0.5", "0.5"); err == nil {
		t.Error("validateThresholdOrder(0.5, 0.5) = nil, want an error (equal thresholds leave no gray zone)")
	}
}

// newSettings must seed the thresholds step from the current config, the
// same way it seeds backend.
func TestNewSettingsSeedsThresholdsFromConfig(t *testing.T) {
	cfg := kimiCfg(t)
	cfg.MergeThreshold = 0.93
	cfg.NewTopicThreshold = 0.65
	cfg.TopicPromptLimit = 42

	m := newTestSettings(t, cfg)
	if m.vals.mergeThreshold != "0.93" {
		t.Errorf("mergeThreshold = %q, want 0.93", m.vals.mergeThreshold)
	}
	if m.vals.newTopicThreshold != "0.65" {
		t.Errorf("newTopicThreshold = %q, want 0.65", m.vals.newTopicThreshold)
	}
	if m.vals.topicPromptLimit != "42" {
		t.Errorf("topicPromptLimit = %q, want 42", m.vals.topicPromptLimit)
	}
}

// thresholdValues must detect a genuine change and leave unchanged values
// alone, exactly like backendChanged does for the backend/path step —
// saveCmd only calls setup.SetThresholds (and restarts the service) when
// this reports changed.
func TestThresholdValuesDetectsChange(t *testing.T) {
	cfg := &config.Config{MergeThreshold: 0.90, NewTopicThreshold: 0.70, TopicPromptLimit: 50}

	same := &settingsValues{mergeThreshold: "0.90", newTopicThreshold: "0.70", topicPromptLimit: "50"}
	if _, _, _, changed := thresholdValues(same, cfg); changed {
		t.Error("thresholdValues() changed = true for values identical to cfg")
	}

	changedVals := &settingsValues{mergeThreshold: "0.95", newTopicThreshold: "0.60", topicPromptLimit: "20"}
	merge, newTopic, limit, changed := thresholdValues(changedVals, cfg)
	if !changed {
		t.Fatal("thresholdValues() changed = false, want true")
	}
	if merge != 0.95 || newTopic != 0.60 || limit != 20 {
		t.Errorf("thresholdValues() = %v/%v/%v, want 0.95/0.60/20", merge, newTopic, limit)
	}
}

// Submitting the thresholds step ends with setup.SetThresholds writing the
// parsed values to config.yaml — this is the exact call saveCmd makes when
// thresholdValues reports a change (proved above), exercised here the same
// way TestSetThresholdsPreservesUnknownKeys exercises SetBackend: end to end
// against a real file, without touching setup.SetAPIKey/RestartService
// (which shell out to the real service manager — service_test.go documents
// why those are never exercised directly).
func TestSettingsThresholdsStepPersistsToConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("analyzer_backend: kimi\nkimi_path: /bin/kimi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{BinaryPaths: map[string]string{"kimi": "/bin/kimi"}, AnalyzerBackend: "kimi", Path: path,
		MergeThreshold: 0.90, NewTopicThreshold: 0.70, TopicPromptLimit: 50}

	m := newTestSettings(t, cfg)
	m.vals.mergeThreshold = "0.95"
	m.vals.newTopicThreshold = "0.60"
	m.vals.topicPromptLimit = "20"

	merge, newTopic, limit, changed := thresholdValues(m.vals, m.cfg)
	if !changed {
		t.Fatal("thresholdValues() changed = false after editing every field")
	}
	if err := setup.SetThresholds(m.target, merge, newTopic, limit); err != nil {
		t.Fatalf("setup.SetThresholds: %v", err)
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if reloaded.MergeThreshold != 0.95 || reloaded.NewTopicThreshold != 0.60 || reloaded.TopicPromptLimit != 20 {
		t.Errorf("reloaded thresholds = %v/%v/%v, want 0.95/0.60/20",
			reloaded.MergeThreshold, reloaded.NewTopicThreshold, reloaded.TopicPromptLimit)
	}
	// analyzer_backend must survive the partial edit untouched.
	if reloaded.AnalyzerBackend != "kimi" {
		t.Errorf("analyzer_backend = %q, want kimi preserved", reloaded.AnalyzerBackend)
	}
}

// The size must reach the model: sizeForm measures the chrome by rendering it,
// so a narrow or short terminal exercises a different code path than the
// default 100x40.
func TestSettingsViewDoesNotPanic(t *testing.T) {
	steps := []settingsStep{stepBackend, stepPath, stepThresholds, stepKey, stepSaving, stepResult}
	for _, size := range []struct{ w, h int }{{100, 40}, {60, 24}, {30, 10}, {15, 5}} {
		for _, step := range steps {
			for _, backend := range []string{"claude", "lemur"} {
				for _, detected := range []string{"", "/usr/bin/claude"} {
					m := newTestSettingsSized(t, kimiCfg(t), size.w, size.h)
					m.vals.backend = backend
					m.detected = detected
					m.err = errors.New("something went wrong")
					m = pump(t, m, m.enter(step))
					out := m.View()
					if out == "" {
						t.Errorf("%dx%d step %d backend %s: empty view",
							size.w, size.h, step, backend)
					}
					assertNoOverflow(t, fmt.Sprintf("settings/step-%d/backend-%s", step, backend), out, size.w, size.h)
				}
			}
		}
	}
}
