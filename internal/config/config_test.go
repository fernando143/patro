package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes content to <dir>/config.yaml and returns its path.
func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaultsForMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "config.yaml")

	cfg, err := Load(missing)
	if err != nil {
		t.Fatalf("Load(%q): %v", missing, err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Videos/obs"); cfg.Inbox != want {
		t.Errorf("Inbox = %q, want %q", cfg.Inbox, want)
	}
	if want := filepath.Join(dir, "knowledge"); cfg.Library != want {
		t.Errorf("Library = %q, want %q", cfg.Library, want)
	}
	wantExt := []string{".mkv", ".mp4", ".mov", ".webm"}
	if strings.Join(cfg.VideoExtensions, ",") != strings.Join(wantExt, ",") {
		t.Errorf("VideoExtensions = %v, want %v", cfg.VideoExtensions, wantExt)
	}
	if cfg.StabilityChecks != 2 {
		t.Errorf("StabilityChecks = %d, want 2", cfg.StabilityChecks)
	}
	if cfg.StabilityIntervalSeconds != 5 {
		t.Errorf("StabilityIntervalSeconds = %d, want 5", cfg.StabilityIntervalSeconds)
	}
	if cfg.AnalyzerBackend != "kimi" {
		t.Errorf("AnalyzerBackend = %q, want %q", cfg.AnalyzerBackend, "kimi")
	}
	if cfg.KimiPath != "kimi" {
		t.Errorf("KimiPath = %q, want %q", cfg.KimiPath, "kimi")
	}
	if cfg.ClaudePath != "claude" {
		t.Errorf("ClaudePath = %q, want %q", cfg.ClaudePath, "claude")
	}
	if cfg.Dir != dir {
		t.Errorf("Dir = %q, want %q", cfg.Dir, dir)
	}
	if want := filepath.Join(dir, ".state"); cfg.StateDir() != want {
		t.Errorf("StateDir() = %q, want %q", cfg.StateDir(), want)
	}
	if want := filepath.Join(dir, "patro.log"); cfg.LogFile() != want {
		t.Errorf("LogFile() = %q, want %q", cfg.LogFile(), want)
	}
}

func TestLoadMergesYAMLOverrides(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
inbox: ~/recordings
library: ./notes
stability_checks: 4
analyzer_backend: LeMUR
kimi_path: /opt/kimi
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "recordings"); cfg.Inbox != want {
		t.Errorf("Inbox = %q, want %q", cfg.Inbox, want)
	}
	if want := filepath.Join(dir, "notes"); cfg.Library != want {
		t.Errorf("Library = %q, want %q", cfg.Library, want)
	}
	if cfg.StabilityChecks != 4 {
		t.Errorf("StabilityChecks = %d, want 4", cfg.StabilityChecks)
	}
	// Keys absent from the YAML keep their defaults.
	if cfg.StabilityIntervalSeconds != 5 {
		t.Errorf("StabilityIntervalSeconds = %d, want 5", cfg.StabilityIntervalSeconds)
	}
	// Backend is trimmed and lowercased.
	if cfg.AnalyzerBackend != "lemur" {
		t.Errorf("AnalyzerBackend = %q, want %q", cfg.AnalyzerBackend, "lemur")
	}
	if cfg.KimiPath != "/opt/kimi" {
		t.Errorf("KimiPath = %q, want %q", cfg.KimiPath, "/opt/kimi")
	}
	if cfg.ClaudePath != "claude" {
		t.Errorf("ClaudePath = %q, want %q", cfg.ClaudePath, "claude")
	}
}

func TestLoadInvalidBackend(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "analyzer_backend: bogus\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() succeeded, want an invalid-backend error")
	}
	for _, want := range []string{"bogus", "kimi, lemur, claude"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoadNormalizesExtensions(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "video_extensions: [MKV, mp4, .MOV, ' .WebM ']\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	want := []string{".mkv", ".mp4", ".mov", ".webm"}
	if strings.Join(cfg.VideoExtensions, ",") != strings.Join(want, ",") {
		t.Errorf("VideoExtensions = %v, want %v", cfg.VideoExtensions, want)
	}

	for _, video := range []string{"a.mkv", "/tmp/B.MP4", "clip.Mov"} {
		if !cfg.IsVideo(video) {
			t.Errorf("IsVideo(%q) = false, want true", video)
		}
	}
	for _, other := range []string{"a.txt", "noext", "a.mkv.bak"} {
		if cfg.IsVideo(other) {
			t.Errorf("IsVideo(%q) = true, want false", other)
		}
	}
}

func TestLoadResolvesRelativePathsAgainstDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, nested, "inbox: inbox\nlibrary: ../shared\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if want := filepath.Join(nested, "inbox"); cfg.Inbox != want {
		t.Errorf("Inbox = %q, want %q", cfg.Inbox, want)
	}
	if want := filepath.Join(dir, "sub", "shared"); cfg.Library != want {
		t.Errorf("Library = %q, want %q", cfg.Library, want)
	}
	if cfg.Dir != nested {
		t.Errorf("Dir = %q, want %q", cfg.Dir, nested)
	}
}

func TestLoadAbsolutePathsAreKept(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "abs-inbox")
	path := writeConfig(t, dir, "inbox: "+inbox+"\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if cfg.Inbox != inbox {
		t.Errorf("Inbox = %q, want %q", cfg.Inbox, inbox)
	}
}
