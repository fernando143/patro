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
	if cfg.EmbeddingBackend != "cybertron" {
		t.Errorf("EmbeddingBackend = %q, want %q", cfg.EmbeddingBackend, "cybertron")
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
embedding_backend: CYBERTRON
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
	if cfg.EmbeddingBackend != "cybertron" {
		t.Errorf("EmbeddingBackend = %q, want %q", cfg.EmbeddingBackend, "cybertron")
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

func TestLoadInvalidEmbeddingBackend(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "embedding_backend: bogus\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() succeeded, want an invalid-embedding-backend error")
	}
	for _, want := range []string{"bogus", strings.Join(ValidEmbeddingBackends(), ", ")} {
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

func TestLoadMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "inbox: [unclosed\n")

	if _, err := Load(path); err == nil {
		t.Fatal("Load() succeeded on malformed YAML, want an error")
	}
}

func TestLoadEmptyFlagUsesLocalConfigYAML(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "stability_checks: 9\n")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if cfg.StabilityChecks != 9 {
		t.Errorf("StabilityChecks = %d, want 9 (from ./config.yaml)", cfg.StabilityChecks)
	}
	if cfg.Path != filepath.Join(dir, "config.yaml") {
		t.Errorf("Path = %q, want the local config.yaml", cfg.Path)
	}
}

func TestLoadEmptyFlagFallsBackToUserConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userConfigDir := filepath.Join(home, ".config", "patro")
	if err := os.MkdirAll(userConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, userConfigDir, "stability_checks: 3\n")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	noLocalConfig := t.TempDir()
	if err := os.Chdir(noLocalConfig); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if cfg.StabilityChecks != 3 {
		t.Errorf("StabilityChecks = %d, want 3 (from the user config path)", cfg.StabilityChecks)
	}
}

func TestLoadEmptyFlagNoConfigAnywhereUsesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if cfg.Path != "" {
		t.Errorf("Path = %q, want empty when no config file exists anywhere", cfg.Path)
	}
	if cfg.Dir != dir {
		t.Errorf("Dir = %q, want the cwd %q", cfg.Dir, dir)
	}
}

func TestValidAnalyzerBackendsReturnsIndependentCopy(t *testing.T) {
	got := ValidAnalyzerBackends()
	if len(got) == 0 {
		t.Fatal("ValidAnalyzerBackends() returned no backends")
	}
	got[0] = "corrupted"

	again := ValidAnalyzerBackends()
	if again[0] == "corrupted" {
		t.Error("mutating a returned slice corrupted the package's internal list")
	}
}

func TestValidEmbeddingBackendsReturnsIndependentCopy(t *testing.T) {
	got := ValidEmbeddingBackends()
	if len(got) == 0 {
		t.Fatal("ValidEmbeddingBackends() returned no backends")
	}
	got[0] = "corrupted"

	again := ValidEmbeddingBackends()
	if again[0] == "corrupted" {
		t.Error("mutating a returned slice corrupted the package's internal list")
	}
}

func TestUserConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".config", "patro", "config.yaml")
	if got := UserConfigPath(); got != want {
		t.Errorf("UserConfigPath() = %q, want %q", got, want)
	}
}

func TestAPIKeyMissingAndSet(t *testing.T) {
	cfg := &Config{}

	t.Setenv(APIKeyEnvVar, "")
	if _, err := cfg.APIKey(); err == nil {
		t.Error("APIKey() = nil error, want an error when unset")
	}

	t.Setenv(APIKeyEnvVar, "  secret-key  ")
	got, err := cfg.APIKey()
	if err != nil {
		t.Fatalf("APIKey() error = %v, want nil", err)
	}
	if got != "secret-key" {
		t.Errorf("APIKey() = %q, want trimmed %q", got, "secret-key")
	}
}

func TestBinaryPathOr(t *testing.T) {
	blank := "   "
	set := "/opt/bin/kimi"

	if got := binaryPathOr(nil, "fallback"); got != "fallback" {
		t.Errorf("binaryPathOr(nil) = %q, want fallback", got)
	}
	if got := binaryPathOr(&blank, "fallback"); got != "fallback" {
		t.Errorf("binaryPathOr(blank) = %q, want fallback", got)
	}
	if got := binaryPathOr(&set, "fallback"); got != set {
		t.Errorf("binaryPathOr(set) = %q, want %q", got, set)
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
