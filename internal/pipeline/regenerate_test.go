package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/library"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/types"
)

// spyAnalyze records the "existing" argument it was called with and every
// transcript ID it saw, so tests can assert D-invariant: existing is always
// literal nil in the regenerate path, regardless of what topics exist on
// disk.
func spyAnalyze(calls *[]spyCall, title string) AnalyzeFunc {
	return func(_ context.Context, t *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error) {
		*calls = append(*calls, spyCall{transcriptID: t.ID, existing: existing})
		return &types.AnalysisResult{Title: title, Summary: "regenerated"}, nil
	}
}

type spyCall struct {
	transcriptID string
	existing     []types.TopicRef
}

func TestRegeneratePriorNoteOverwriteReusesDateAndSourceVideo(t *testing.T) {
	cfg := newTestCfg(t)
	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}

	transcriptPath := filepath.Join(lib.TranscriptsDir, "abc123.txt")
	if err := os.WriteFile(transcriptPath, []byte("Speaker A: hello.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	priorPath, err := lib.WriteMeetingNote(
		&types.TranscriptResult{ID: "abc123"},
		&types.AnalysisResult{Title: "Old Title"},
		"original-video.mkv", "2026-01-05",
	)
	if err != nil {
		t.Fatalf("WriteMeetingNote setup: %v", err)
	}

	var calls []spyCall
	notePath, err := Regenerate(context.Background(), transcriptPath, "", cfg, spyAnalyze(&calls, "New Title"))
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if notePath != priorPath {
		t.Errorf("notePath = %q, want exact prior path %q", notePath, priorPath)
	}

	matches, _ := filepath.Glob(filepath.Join(lib.MeetingsDir, "*.md"))
	if len(matches) != 1 {
		t.Errorf("meeting notes = %v, want exactly one file (overwrite, not a new one)", matches)
	}

	content := readTestFile(t, notePath)
	if !strings.Contains(content, "- **Date:** 2026-01-05") {
		t.Errorf("content = %q, want prior date 2026-01-05 reused", content)
	}
	if !strings.Contains(content, "- **Source video:** `original-video.mkv`") {
		t.Errorf("content = %q, want prior source video reused", content)
	}
	if !strings.Contains(content, "# New Title") {
		t.Errorf("content = %q, want new title from re-analysis", content)
	}

	if len(calls) != 1 {
		t.Fatalf("analyze calls = %d, want 1", len(calls))
	}
	if calls[0].existing != nil {
		t.Errorf("existing = %+v, want literal nil (design invariant)", calls[0].existing)
	}
}

func TestRegenerateExternalTranscriptCopiedBeforeAnalysis(t *testing.T) {
	cfg := newTestCfg(t)
	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}

	extDir := t.TempDir()
	extPath := filepath.Join(extDir, "some-recording.txt")
	if err := os.WriteFile(extPath, []byte("Speaker A: this is an external transcript.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var copyExistedAtAnalyzeTime bool
	af := func(_ context.Context, t *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error) {
		matches, _ := filepath.Glob(filepath.Join(lib.TranscriptsDir, "*.txt"))
		copyExistedAtAnalyzeTime = len(matches) == 1
		return &types.AnalysisResult{Title: "External Analysis"}, nil
	}

	notePath, err := Regenerate(context.Background(), extPath, "2026-02-01", cfg, af)
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if notePath == "" {
		t.Fatal("notePath = \"\", want non-empty")
	}
	if !copyExistedAtAnalyzeTime {
		t.Error("the external transcript copy did not exist yet when the analyzer ran (design D5: copy before analysis)")
	}

	copies, _ := filepath.Glob(filepath.Join(lib.TranscriptsDir, "ext-*.txt"))
	if len(copies) != 1 {
		t.Errorf("transcript copies = %v, want exactly one ext-* file", copies)
	}
}

func TestRegenerateMissingDateNoPriorNoteFailsWithNoFileWritten(t *testing.T) {
	cfg := newTestCfg(t)
	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}

	extDir := t.TempDir()
	extPath := filepath.Join(extDir, "no-date-recording.txt")
	if err := os.WriteFile(extPath, []byte("Speaker A: no prior note, no --date.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	called := false
	af := func(context.Context, *types.TranscriptResult, []types.TopicRef) (*types.AnalysisResult, error) {
		called = true
		return &types.AnalysisResult{Title: "Should not happen"}, nil
	}

	notePath, err := Regenerate(context.Background(), extPath, "", cfg, af)
	if err == nil {
		t.Fatal("Regenerate() error = nil, want error when --date is missing and no prior note exists")
	}
	if notePath != "" {
		t.Errorf("notePath = %q, want empty on failure", notePath)
	}
	if called {
		t.Error("analyze func was called despite unresolved date, want fail-fast before analysis")
	}

	transcripts, _ := filepath.Glob(filepath.Join(lib.TranscriptsDir, "*"))
	if len(transcripts) != 0 {
		t.Errorf("transcripts dir = %v, want empty (no copy on date-resolution failure)", transcripts)
	}
	notes, _ := filepath.Glob(filepath.Join(lib.MeetingsDir, "*"))
	if len(notes) != 0 {
		t.Errorf("meetings dir = %v, want empty (no note written on failure)", notes)
	}
}

func TestRegenerateMockDeterministic(t *testing.T) {
	cfg := newTestCfg(t)
	extDir := t.TempDir()
	extPath := filepath.Join(extDir, "deterministic-recording.txt")
	if err := os.WriteFile(extPath, []byte("Speaker A: deterministic content.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	notePath1, err := Regenerate(context.Background(), extPath, "2026-03-01", cfg, MockAnalyze)
	if err != nil {
		t.Fatalf("Regenerate (first run): %v", err)
	}
	content1 := readTestFile(t, notePath1)

	notePath2, err := Regenerate(context.Background(), extPath, "2026-03-01", cfg, MockAnalyze)
	if err != nil {
		t.Fatalf("Regenerate (second run): %v", err)
	}
	content2 := readTestFile(t, notePath2)

	if notePath1 != notePath2 {
		t.Errorf("notePath1 = %q, notePath2 = %q, want the same path (stable content hash + prior-note reuse)", notePath1, notePath2)
	}
	if content1 != content2 {
		t.Errorf("mock regeneration is not deterministic:\nfirst:\n%s\nsecond:\n%s", content1, content2)
	}
}

func TestRegenerateTopicsIndexAndProcessedStateUntouched(t *testing.T) {
	cfg := newTestCfg(t)
	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}

	topicPath := filepath.Join(lib.TopicsDir, "existing-topic.md")
	topicContent := "# Existing Topic\n\n## 2026-01-01 — Old Meeting\n\nOld content.\n"
	if err := os.WriteFile(topicPath, []byte(topicContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	indexPath := filepath.Join(lib.Root, "index.md")
	indexContent := "# Knowledge library\n\nstale, never rebuilt by regenerate\n"
	if err := os.WriteFile(indexPath, []byte(indexContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stateDir := t.TempDir()
	otherVideo := filepath.Join(t.TempDir(), "other-video.mkv")
	if err := os.WriteFile(otherVideo, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st := state.New(stateDir)
	if err := st.MarkProcessed(otherVideo, "unrelated-id"); err != nil {
		t.Fatalf("MarkProcessed setup: %v", err)
	}
	processedPath := filepath.Join(stateDir, "processed.json")
	processedBefore := readTestFile(t, processedPath)

	extDir := t.TempDir()
	extPath := filepath.Join(extDir, "isolation-check.txt")
	if err := os.WriteFile(extPath, []byte("Speaker A: isolation check.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Regenerate(context.Background(), extPath, "2026-04-01", cfg, MockAnalyze); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}

	if got := readTestFile(t, topicPath); got != topicContent {
		t.Errorf("topic file changed:\nbefore:\n%s\nafter:\n%s", topicContent, got)
	}
	if got := readTestFile(t, indexPath); got != indexContent {
		t.Errorf("index.md changed:\nbefore:\n%s\nafter:\n%s", indexContent, got)
	}
	if got := readTestFile(t, processedPath); got != processedBefore {
		t.Errorf("processed.json changed:\nbefore:\n%s\nafter:\n%s", processedBefore, got)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
