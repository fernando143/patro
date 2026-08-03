package setup

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// readConfig loads path back into a generic map so tests can assert on keys
// SetBackend does not know about.
func readConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	data := map[string]any{}
	if err := yaml.Unmarshal(raw, &data); err != nil {
		t.Fatalf("cannot parse %s: %v", path, err)
	}
	return data
}

// writeConfigFile seeds a config file for a test.
func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("cannot seed config: %v", err)
	}
	return path
}

const fullConfig = `inbox: /home/u/Videos/obs
library: /home/u/knowledge
analyzer_backend: kimi
kimi_path: /usr/local/bin/kimi
claude_path: /usr/local/bin/claude
codex_path: /usr/local/bin/codex
video_extensions:
- .mkv
- .mp4
stability_checks: 3
stability_interval_seconds: 7
some_unknown_key: keep-me
`

func TestSetBackendPreservesUnknownKeys(t *testing.T) {
	path := writeConfigFile(t, fullConfig)

	if err := SetBackend(path, "claude", "/opt/claude"); err != nil {
		t.Fatalf("SetBackend: %v", err)
	}

	got := readConfig(t, path)
	if got["analyzer_backend"] != "claude" {
		t.Errorf("analyzer_backend = %v, want claude", got["analyzer_backend"])
	}
	if got["claude_path"] != "/opt/claude" {
		t.Errorf("claude_path = %v, want /opt/claude", got["claude_path"])
	}
	// The other backend's path must survive so switching back keeps it.
	if got["kimi_path"] != "/usr/local/bin/kimi" {
		t.Errorf("kimi_path = %v, want it preserved", got["kimi_path"])
	}
	for key, want := range map[string]any{
		"inbox":                      "/home/u/Videos/obs",
		"library":                    "/home/u/knowledge",
		"stability_checks":           3,
		"stability_interval_seconds": 7,
		"some_unknown_key":           "keep-me",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	exts, ok := got["video_extensions"].([]any)
	if !ok || len(exts) != 2 {
		t.Errorf("video_extensions = %v, want the original two entries", got["video_extensions"])
	}
}

func TestSetBackendLemurLeavesBinaryPaths(t *testing.T) {
	path := writeConfigFile(t, fullConfig)

	if err := SetBackend(path, "lemur", "/ignored"); err != nil {
		t.Fatalf("SetBackend: %v", err)
	}

	got := readConfig(t, path)
	if got["analyzer_backend"] != "lemur" {
		t.Errorf("analyzer_backend = %v, want lemur", got["analyzer_backend"])
	}
	// lemur is hosted: neither path key is written nor deleted.
	if got["kimi_path"] != "/usr/local/bin/kimi" {
		t.Errorf("kimi_path = %v, want it preserved", got["kimi_path"])
	}
	if got["claude_path"] != "/usr/local/bin/claude" {
		t.Errorf("claude_path = %v, want it preserved", got["claude_path"])
	}
	if _, ok := got["lemur_path"]; ok {
		t.Error("lemur_path was written; lemur needs no binary")
	}
}

func TestSetBackendCodexWritesCodexPath(t *testing.T) {
	path := writeConfigFile(t, fullConfig)

	if err := SetBackend(path, "codex", "/opt/codex"); err != nil {
		t.Fatalf("SetBackend: %v", err)
	}

	got := readConfig(t, path)
	if got["analyzer_backend"] != "codex" || got["codex_path"] != "/opt/codex" {
		t.Errorf("got %v, want codex backend and path", got)
	}
}

func TestSetBackendCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	if err := SetBackend(path, "kimi", "/usr/bin/kimi"); err != nil {
		t.Fatalf("SetBackend: %v", err)
	}

	got := readConfig(t, path)
	if got["analyzer_backend"] != "kimi" || got["kimi_path"] != "/usr/bin/kimi" {
		t.Errorf("got %v, want a config naming the kimi backend and path", got)
	}
}

func TestSetBackendRejectsUnknownBackend(t *testing.T) {
	path := writeConfigFile(t, fullConfig)

	if err := SetBackend(path, "gpt", "/usr/bin/gpt"); err == nil {
		t.Fatal("SetBackend accepted an unknown backend")
	}
	// The file must be untouched after a rejected write.
	if got := readConfig(t, path); got["analyzer_backend"] != "kimi" {
		t.Errorf("analyzer_backend = %v, want the original kimi", got["analyzer_backend"])
	}
}

func TestSetBackendRejectsMalformedYAML(t *testing.T) {
	// A hand-written file we cannot parse must not be silently truncated.
	path := writeConfigFile(t, "inbox: [unclosed\n")

	if err := SetBackend(path, "claude", "/opt/claude"); err == nil {
		t.Fatal("SetBackend accepted malformed YAML")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if string(raw) != "inbox: [unclosed\n" {
		t.Errorf("file was modified: %q", raw)
	}
}

func TestWriteConfigMkdirAllError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "nested", "config.yaml")

	err := WriteConfig(path, Values{Inbox: "/in", Library: "/lib", Backend: "kimi", BinaryPath: "/bin/kimi"})
	if err == nil {
		t.Error("WriteConfig() error = nil, want error when the parent directory cannot be created")
	}
}

func TestWriteConfigNilThresholdsPreservesExisting(t *testing.T) {
	path := writeConfigFile(t, fullConfig+"merge_threshold: 0.85\nnew_topic_threshold: 0.65\ntopic_prompt_limit: 30\n")

	if err := WriteConfig(path, Values{Inbox: "/in", Library: "/lib", Backend: "kimi", BinaryPath: "/bin/kimi"}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got := readConfig(t, path)
	if got["merge_threshold"] != 0.85 {
		t.Errorf("merge_threshold = %v, want 0.85 preserved (nil pointer must not overwrite)", got["merge_threshold"])
	}
	if got["new_topic_threshold"] != 0.65 {
		t.Errorf("new_topic_threshold = %v, want 0.65 preserved", got["new_topic_threshold"])
	}
	if got["topic_prompt_limit"] != 30 {
		t.Errorf("topic_prompt_limit = %v, want 30 preserved", got["topic_prompt_limit"])
	}
}

func TestWriteConfigThresholdsRoundTrip(t *testing.T) {
	path := writeConfigFile(t, fullConfig)
	merge, newTopic, limit := 0.92, 0.72, 40

	err := WriteConfig(path, Values{
		Inbox: "/in", Library: "/lib", Backend: "kimi", BinaryPath: "/bin/kimi",
		MergeThreshold: &merge, NewTopicThreshold: &newTopic, TopicPromptLimit: &limit,
	})
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got := readConfig(t, path)
	if got["merge_threshold"] != merge {
		t.Errorf("merge_threshold = %v, want %v", got["merge_threshold"], merge)
	}
	if got["new_topic_threshold"] != newTopic {
		t.Errorf("new_topic_threshold = %v, want %v", got["new_topic_threshold"], newTopic)
	}
	if got["topic_prompt_limit"] != limit {
		t.Errorf("topic_prompt_limit = %v, want %v", got["topic_prompt_limit"], limit)
	}
	if got["some_unknown_key"] != "keep-me" {
		t.Errorf("some_unknown_key = %v, want it preserved", got["some_unknown_key"])
	}
}

func TestSetThresholdsPreservesUnknownKeys(t *testing.T) {
	path := writeConfigFile(t, fullConfig)

	if err := SetThresholds(path, 0.93, 0.71, 25); err != nil {
		t.Fatalf("SetThresholds: %v", err)
	}

	got := readConfig(t, path)
	if got["merge_threshold"] != 0.93 {
		t.Errorf("merge_threshold = %v, want 0.93", got["merge_threshold"])
	}
	if got["new_topic_threshold"] != 0.71 {
		t.Errorf("new_topic_threshold = %v, want 0.71", got["new_topic_threshold"])
	}
	if got["topic_prompt_limit"] != 25 {
		t.Errorf("topic_prompt_limit = %v, want 25", got["topic_prompt_limit"])
	}
	// Every other key, including the analyzer backend and its path, must
	// survive untouched.
	if got["analyzer_backend"] != "kimi" || got["kimi_path"] != "/usr/local/bin/kimi" {
		t.Errorf("backend settings not preserved: %v", got)
	}
	if got["some_unknown_key"] != "keep-me" {
		t.Errorf("some_unknown_key = %v, want it preserved", got["some_unknown_key"])
	}
}

func TestSetThresholdsCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	if err := SetThresholds(path, 0.9, 0.7, 50); err != nil {
		t.Fatalf("SetThresholds: %v", err)
	}

	got := readConfig(t, path)
	if got["merge_threshold"] != 0.9 || got["new_topic_threshold"] != 0.7 || got["topic_prompt_limit"] != 50 {
		t.Errorf("got %v, want the given thresholds", got)
	}
}

func TestSetThresholdsRejectsMalformedYAML(t *testing.T) {
	path := writeConfigFile(t, "inbox: [unclosed\n")

	if err := SetThresholds(path, 0.9, 0.7, 50); err == nil {
		t.Fatal("SetThresholds accepted malformed YAML")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if string(raw) != "inbox: [unclosed\n" {
		t.Errorf("file was modified: %q", raw)
	}
}

func TestWriteConfigSwapsBackendPath(t *testing.T) {
	path := writeConfigFile(t, fullConfig)

	if err := WriteConfig(path, Values{
		Inbox: "/in", Library: "/lib", Backend: "claude", BinaryPath: "/opt/claude",
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got := readConfig(t, path)
	if got["claude_path"] != "/opt/claude" {
		t.Errorf("claude_path = %v, want /opt/claude", got["claude_path"])
	}
	// The wizard owns the file, so the unused backend path is dropped.
	if _, ok := got["kimi_path"]; ok {
		t.Error("kimi_path survived a wizard write to the claude backend")
	}
	if got["some_unknown_key"] != "keep-me" {
		t.Errorf("some_unknown_key = %v, want it preserved", got["some_unknown_key"])
	}
}
