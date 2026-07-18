package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/types"
)

func TestAssistantTextStringContent(t *testing.T) {
	stream := `{"role":"assistant","content":"first"}
{"role":"assistant","content":"second"}`
	if got := assistantText(stream); got != "first\nsecond" {
		t.Errorf("assistantText = %q, want %q", got, "first\nsecond")
	}
}

func TestAssistantTextBlockContent(t *testing.T) {
	stream := `{"role":"assistant","content":[{"type":"text","text":"alpha"},{"type":"tool_use","text":"skip me"},{"type":"text","text":""},{"type":"text"},{"type":"text","text":"beta"}]}`
	if got := assistantText(stream); got != "alpha\nbeta" {
		t.Errorf("assistantText = %q, want %q", got, "alpha\nbeta")
	}
}

func TestAssistantTextSkipsNoise(t *testing.T) {
	stream := `
{"role":"system","content":"meta"}
{"role":"user","content":"question"}

not valid JSON at all
["a", "json", "array"]
"a json string"
{"role":"assistant","content":"kept"}
{"role":"tool_result","content":"result"}
{"content":"no role"}
{"role":"assistant"}
`
	if got := assistantText(stream); got != "kept" {
		t.Errorf("assistantText = %q, want %q", got, "kept")
	}
}

func TestAssistantTextEmpty(t *testing.T) {
	if got := assistantText("\n \n\t\n"); got != "" {
		t.Errorf("assistantText = %q, want empty", got)
	}
}

func TestWriteTranscriptFileWithUtterances(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), ".state")
	tr := &types.TranscriptResult{
		ID: "abc123",
		Utterances: []types.Utterance{
			{Speaker: "A", Text: "hello"},
			{Speaker: "B", Text: "hi there"},
		},
	}
	path, err := writeTranscriptFile(tr, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateDir, "tmp", "transcript-abc123.txt"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Speaker A: hello\n\nSpeaker B: hi there\n"; string(data) != want {
		t.Errorf("content = %q, want %q", data, want)
	}
}

func TestWriteTranscriptFilePlainText(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), ".state")
	tr := &types.TranscriptResult{ID: "x y", Text: "raw transcript text"}
	path, err := writeTranscriptFile(tr, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "raw transcript text\n"; string(data) != want {
		t.Errorf("content = %q, want %q", data, want)
	}
}

// writeFakeCLI writes an executable shell script behaving as a fake
// kimi/claude CLI and returns its path.
func writeFakeCLI(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyzeCLISuccess(t *testing.T) {
	dir := t.TempDir()

	inner := `{"meeting": {"title": "FromCLI", "summary": "via cli"}, "topics": [{"slug": "cli-topic"}]}`
	assistantLine, err := json.Marshal(map[string]any{"role": "assistant", "content": inner})
	if err != nil {
		t.Fatal(err)
	}
	introLine, err := json.Marshal(map[string]any{"role": "assistant", "content": "Here is the analysis:"})
	if err != nil {
		t.Fatal(err)
	}
	streamFile := filepath.Join(dir, "stream.jsonl")
	stream := "{\"role\":\"system\",\"type\":\"meta\"}\n" +
		string(introLine) + "\n" +
		"not json\n" +
		string(assistantLine) + "\n" +
		"{\"role\":\"user\",\"content\":\"ignored\"}\n"
	if err := os.WriteFile(streamFile, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}

	// The fake CLI records its cwd and argv, then emits the canned stream.
	kimiPath := writeFakeCLI(t, dir, "fake-kimi",
		"#!/bin/sh\n{ pwd; printf '%s\\n' \"$@\"; } > invocation.txt\ncat \""+streamFile+"\"\n")

	cfg := &config.Config{Dir: dir, AnalyzerBackend: "kimi", KimiPath: kimiPath}
	tr := &types.TranscriptResult{
		ID:       "mtg1",
		Language: "en",
		Utterances: []types.Utterance{
			{Speaker: "A", Text: "let us migrate"},
		},
	}

	result, err := AnalyzeCLI(context.Background(), tr, []types.TopicRef{{Slug: "old", Name: "Old"}}, cfg)
	if err != nil {
		t.Fatalf("AnalyzeCLI: %v", err)
	}
	if result.Title != "FromCLI" {
		t.Errorf("Title = %q, want %q", result.Title, "FromCLI")
	}
	if result.Summary != "via cli" {
		t.Errorf("Summary = %q, want %q", result.Summary, "via cli")
	}
	if len(result.Topics) != 1 || result.Topics[0].Slug != "cli-topic" || result.Topics[0].Name != "cli-topic" {
		t.Errorf("Topics = %v", result.Topics)
	}

	// The CLI ran in cfg.Dir with `-p <prompt> --output-format stream-json`.
	invocation, err := os.ReadFile(filepath.Join(dir, "invocation.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(invocation)
	if !strings.HasPrefix(text, dir+"\n-p\n") {
		t.Errorf("invocation should start with cwd and -p, got %q", text[:min(80, len(text))])
	}
	if !strings.HasSuffix(text, "--output-format\nstream-json\n") {
		t.Errorf("invocation should end with --output-format stream-json, got %q", text[max(0, len(text)-60):])
	}
	transcriptFile := filepath.Join(dir, ".state", "tmp", "transcript-mtg1.txt")
	if !strings.Contains(text, transcriptFile) {
		t.Errorf("prompt should reference the transcript file %q", transcriptFile)
	}
	if !strings.Contains(text, "You are analyzing a meeting transcript") {
		t.Error("prompt should contain the analyzer instructions")
	}
	if !strings.Contains(text, "- old: Old") {
		t.Error("prompt should list existing topics")
	}

	// The temporary transcript file is always removed afterwards.
	if _, err := os.Stat(transcriptFile); !os.IsNotExist(err) {
		t.Errorf("transcript file %q should have been removed", transcriptFile)
	}
}

func TestAnalyzeCLIBinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	tr := &types.TranscriptResult{ID: "m1", Text: "text"}

	cfg := &config.Config{Dir: dir, AnalyzerBackend: "kimi", KimiPath: filepath.Join(dir, "no-such-kimi")}
	_, err := AnalyzeCLI(context.Background(), tr, nil, cfg)
	if err == nil {
		t.Fatal("expected error for missing kimi binary")
	}
	want := fmt.Sprintf("'%s' executable not found. Install Kimi Code CLI "+
		"(https://www.kimi.com/code), adjust kimi_path in config.yaml, "+
		"or set analyzer_backend: lemur in config.yaml.", cfg.KimiPath)
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	cfg = &config.Config{Dir: dir, AnalyzerBackend: "claude", ClaudePath: filepath.Join(dir, "no-such-claude")}
	_, err = AnalyzeCLI(context.Background(), tr, nil, cfg)
	if err == nil {
		t.Fatal("expected error for missing claude binary")
	}
	want = fmt.Sprintf("'%s' executable not found. Install Claude Code CLI, "+
		"adjust claude_path in config.yaml, or switch analyzer_backend to kimi/lemur.", cfg.ClaudePath)
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestAnalyzeCLINonZeroExit(t *testing.T) {
	dir := t.TempDir()
	kimiPath := writeFakeCLI(t, dir, "fake-kimi",
		"#!/bin/sh\necho 'fatal: something broke' >&2\nexit 3\n")

	cfg := &config.Config{Dir: dir, AnalyzerBackend: "kimi", KimiPath: kimiPath}
	tr := &types.TranscriptResult{ID: "m2", Text: "text"}
	_, err := AnalyzeCLI(context.Background(), tr, nil, cfg)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if want := "kimi exited with code 3: fatal: something broke"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestAnalyzeCLIStderrTruncatedTo1000(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"i=0; while [ $i -lt 1500 ]; do printf 'e' >&2; i=$((i+1)); done\n" +
		"exit 1\n"
	claudePath := writeFakeCLI(t, dir, "fake-claude", script)

	cfg := &config.Config{Dir: dir, AnalyzerBackend: "claude", ClaudePath: claudePath}
	tr := &types.TranscriptResult{ID: "m3", Text: "text"}
	_, err := AnalyzeCLI(context.Background(), tr, nil, cfg)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if want := "claude exited with code 1: " + strings.Repeat("e", 1000); err.Error() != want {
		t.Errorf("error length = %d, want truncated stderr of 1000 chars", len(err.Error()))
	}
}

func TestAnalyzeCLINoAssistantText(t *testing.T) {
	dir := t.TempDir()
	claudePath := writeFakeCLI(t, dir, "fake-claude",
		"#!/bin/sh\necho '{\"role\":\"user\",\"content\":\"no assistant here\"}'\n")

	cfg := &config.Config{Dir: dir, AnalyzerBackend: "claude", ClaudePath: claudePath}
	tr := &types.TranscriptResult{ID: "m4", Text: "text"}
	_, err := AnalyzeCLI(context.Background(), tr, nil, cfg)
	if err == nil {
		t.Fatal("expected error for empty assistant text")
	}
	if want := "claude produced no assistant text in its stream-json output"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	// The temporary transcript file is removed even on failure.
	transcriptFile := filepath.Join(dir, ".state", "tmp", "transcript-m4.txt")
	if _, statErr := os.Stat(transcriptFile); !os.IsNotExist(statErr) {
		t.Errorf("transcript file %q should have been removed", transcriptFile)
	}
}

func TestAnalyzeCLIUnknownBackend(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Dir: dir, AnalyzerBackend: "lemur"}
	tr := &types.TranscriptResult{ID: "m5", Text: "text"}
	if _, err := AnalyzeCLI(context.Background(), tr, nil, cfg); err == nil {
		t.Fatal("expected error for non-CLI backend")
	}
}
