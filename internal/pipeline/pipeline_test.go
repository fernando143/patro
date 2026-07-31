package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/status"
	"github.com/fernando143/patro/internal/types"
)

// newTestVideo creates a real file on disk: state.IsProcessed/MarkProcessed
// stat the video path, so a bare string path is not enough.
func newTestVideo(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake video bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	return path
}

func newTestCfg(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	return &config.Config{Library: filepath.Join(root, "library")}
}

var errBoom = errors.New("boom")

func failTranscribe(context.Context, string, *config.Config) (*types.TranscriptResult, error) {
	return nil, errBoom
}

func failAnalyze(context.Context, *types.TranscriptResult, []types.TopicRef) (*types.AnalysisResult, error) {
	return nil, errBoom
}

func TestProcessVideoHappyPathMock(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	st := state.New(t.TempDir())

	notePath, err := ProcessVideo(context.Background(), video, cfg, st, nil, MockTranscribe, MockAnalyze)
	if err != nil {
		t.Fatalf("ProcessVideo error = %v, want nil", err)
	}
	if notePath == "" {
		t.Fatal("ProcessVideo notePath = \"\", want non-empty")
	}
	if _, err := os.Stat(notePath); err != nil {
		t.Errorf("meeting note not written at %s: %v", notePath, err)
	}
	if !st.IsProcessed(video) {
		t.Error("state.IsProcessed = false after successful ProcessVideo, want true")
	}
}

func TestProcessVideoSkipsAlreadyProcessed(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	stDir := t.TempDir()
	st := state.New(stDir)
	if err := st.MarkProcessed(video, "prior-transcript-id"); err != nil {
		t.Fatalf("MarkProcessed setup error = %v", err)
	}

	called := false
	tf := func(context.Context, string, *config.Config) (*types.TranscriptResult, error) {
		called = true
		return nil, errBoom
	}

	notePath, err := ProcessVideo(context.Background(), video, cfg, st, nil, tf, MockAnalyze)
	if err != nil {
		t.Fatalf("ProcessVideo error = %v, want nil (skip)", err)
	}
	if notePath != "" {
		t.Errorf("ProcessVideo notePath = %q, want \"\" for a skipped file", notePath)
	}
	if called {
		t.Error("transcribe func was called for an already-processed file, want skip before it runs")
	}
}

func TestProcessVideoLibraryInitError(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	root := t.TempDir()
	// A plain file where the library root should be: os.MkdirAll fails
	// because a path component is not a directory.
	libPath := filepath.Join(root, "library")
	if err := os.WriteFile(libPath, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	cfg := &config.Config{Library: filepath.Join(libPath, "nested")}
	st := state.New(t.TempDir())

	called := false
	tf := func(context.Context, string, *config.Config) (*types.TranscriptResult, error) {
		called = true
		return nil, errBoom
	}

	_, err := ProcessVideo(context.Background(), video, cfg, st, nil, tf, MockAnalyze)
	if err == nil {
		t.Fatal("ProcessVideo error = nil, want error when the library root cannot be created")
	}
	if called {
		t.Error("transcribe func was called despite a library init failure")
	}
}

func TestProcessVideoTranscribeError(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	st := state.New(t.TempDir())

	called := false
	af := func(context.Context, *types.TranscriptResult, []types.TopicRef) (*types.AnalysisResult, error) {
		called = true
		return nil, errBoom
	}

	_, err := ProcessVideo(context.Background(), video, cfg, st, nil, failTranscribe, af)
	if !errors.Is(err, errBoom) {
		t.Fatalf("ProcessVideo error = %v, want errBoom", err)
	}
	if called {
		t.Error("analyze func was called despite a transcribe failure")
	}
	if st.IsProcessed(video) {
		t.Error("state marked processed despite a transcribe failure")
	}
}

func TestProcessVideoAnalyzeError(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	st := state.New(t.TempDir())

	_, err := ProcessVideo(context.Background(), video, cfg, st, nil, MockTranscribe, failAnalyze)
	if !errors.Is(err, errBoom) {
		t.Fatalf("ProcessVideo error = %v, want errBoom", err)
	}
	if st.IsProcessed(video) {
		t.Error("state marked processed despite an analyze failure")
	}
	entries, _ := filepath.Glob(filepath.Join(cfg.Library, "meetings", "*.md"))
	if len(entries) != 0 {
		t.Errorf("meeting notes written despite an analyze failure: %v", entries)
	}
}

func TestProcessVideoAddMeetingError(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	st := state.New(t.TempDir())

	// Pre-create the library layout exactly as NewLibrary would, then strip
	// write permission on transcripts/ so the first write inside AddMeeting
	// (WriteTranscript) fails.
	transcriptsDir := filepath.Join(cfg.Library, "transcripts")
	if err := os.MkdirAll(transcriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Library, "topics"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Library, "meetings"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.Chmod(transcriptsDir, 0o555); err != nil {
		t.Fatalf("Chmod error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(transcriptsDir, 0o755) })

	_, err := ProcessVideo(context.Background(), video, cfg, st, nil, MockTranscribe, MockAnalyze)
	if err == nil {
		t.Fatal("ProcessVideo error = nil, want error when the library cannot write the transcript")
	}
	if st.IsProcessed(video) {
		t.Error("state marked processed despite a library write failure")
	}
}

func TestProcessVideoMarkProcessedError(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)

	stDir := t.TempDir()
	st := state.New(stDir)
	// Force MarkProcessed's saveLocked to fail: the state dir itself is
	// replaced by an unwritable one after State has already resolved its
	// file path against it.
	if err := os.Chmod(stDir, 0o555); err != nil {
		t.Fatalf("Chmod error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stDir, 0o755) })

	notePath, err := ProcessVideo(context.Background(), video, cfg, st, nil, MockTranscribe, MockAnalyze)
	if err == nil {
		t.Fatal("ProcessVideo error = nil, want error when state cannot persist")
	}
	// The note was written to the library before the state write failed —
	// this pipeline step ordering means a MarkProcessed failure leaves a
	// written note that will be reprocessed on the next run.
	if notePath != "" {
		t.Errorf("ProcessVideo notePath = %q, want \"\" alongside a non-nil error", notePath)
	}
}

func TestProcessVideoTrackerNilSafe(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	st := state.New(t.TempDir())

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ProcessVideo panicked with a nil tracker: %v", r)
		}
	}()
	if _, err := ProcessVideo(context.Background(), video, cfg, st, nil, MockTranscribe, MockAnalyze); err != nil {
		t.Fatalf("ProcessVideo error = %v, want nil", err)
	}
}

func TestProcessVideoTrackerStageTransitions(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	stateDir := t.TempDir()
	st := state.New(stateDir)
	tracker, err := status.NewTracker(stateDir)
	if err != nil {
		t.Fatalf("NewTracker error = %v", err)
	}

	var stageDuringTranscribe, stageDuringAnalyze status.Stage
	tf := func(ctx context.Context, videoPath string, c *config.Config) (*types.TranscriptResult, error) {
		snap, err := status.Read(stateDir)
		if err != nil || snap == nil || snap.Current == nil {
			t.Fatalf("status.Read during transcribe = %+v, %v", snap, err)
		}
		stageDuringTranscribe = snap.Current.Stage
		return MockTranscribe(ctx, videoPath, c)
	}
	af := func(ctx context.Context, tr *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error) {
		snap, err := status.Read(stateDir)
		if err != nil || snap == nil || snap.Current == nil {
			t.Fatalf("status.Read during analyze = %+v, %v", snap, err)
		}
		stageDuringAnalyze = snap.Current.Stage
		return MockAnalyze(ctx, tr, existing)
	}

	if _, err := ProcessVideo(context.Background(), video, cfg, st, tracker, tf, af); err != nil {
		t.Fatalf("ProcessVideo error = %v", err)
	}
	if stageDuringTranscribe != status.StageTranscribing {
		t.Errorf("stage during transcribe = %q, want %q", stageDuringTranscribe, status.StageTranscribing)
	}
	if stageDuringAnalyze != status.StageAnalyzing {
		t.Errorf("stage during analyze = %q, want %q", stageDuringAnalyze, status.StageAnalyzing)
	}

	final, err := status.Read(stateDir)
	if err != nil {
		t.Fatalf("status.Read final error = %v", err)
	}
	if final.Current != nil {
		t.Errorf("final Current = %+v, want nil after Done", final.Current)
	}
	if final.ProcessedSession != 1 {
		t.Errorf("final ProcessedSession = %d, want 1", final.ProcessedSession)
	}
}

func TestProcessVideoPassesExistingTopics(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	st := state.New(t.TempDir())

	// Seed one existing topic file before the run so ExistingTopics() is
	// non-empty when ProcessVideo builds the library.
	topicsDir := filepath.Join(cfg.Library, "topics")
	if err := os.MkdirAll(topicsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(topicsDir, "product-roadmap.md"), []byte("# Product roadmap\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	var gotExisting []types.TopicRef
	af := func(ctx context.Context, tr *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error) {
		gotExisting = existing
		return MockAnalyze(ctx, tr, existing)
	}

	if _, err := ProcessVideo(context.Background(), video, cfg, st, nil, MockTranscribe, af); err != nil {
		t.Fatalf("ProcessVideo error = %v", err)
	}
	if len(gotExisting) != 1 || gotExisting[0].Slug != "product-roadmap" {
		t.Errorf("existing topics passed to analyze = %+v, want one ref with slug product-roadmap", gotExisting)
	}
}

func TestMakeAnalyzeFuncCLIBackendDelegatesToAnalyzeCLI(t *testing.T) {
	dir := t.TempDir()
	stream := `{"role":"assistant","content":"{\"meeting\": {\"title\": \"FromCLI\", \"summary\": \"s\"}}"}` + "\n"
	streamFile := filepath.Join(dir, "stream.jsonl")
	if err := os.WriteFile(streamFile, []byte(stream), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	kimiPath := filepath.Join(dir, "fake-kimi")
	if err := os.WriteFile(kimiPath, []byte("#!/bin/sh\ncat \""+streamFile+"\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	cfg := &config.Config{Dir: dir, AnalyzerBackend: "kimi", KimiPath: kimiPath}
	af := MakeAnalyzeFunc(cfg)

	result, err := af(context.Background(), &types.TranscriptResult{ID: "mtg1", Language: "en"}, nil)
	if err != nil {
		t.Fatalf("analyze func error = %v", err)
	}
	if result.Title != "FromCLI" {
		t.Errorf("Title = %q, want %q (MakeAnalyzeFunc did not delegate to AnalyzeCLI)", result.Title, "FromCLI")
	}
}

func TestMakeAnalyzeFuncLemurWithoutAPIKey(t *testing.T) {
	t.Setenv(config.APIKeyEnvVar, "")
	cfg := &config.Config{AnalyzerBackend: "lemur"}
	af := MakeAnalyzeFunc(cfg)

	_, err := af(context.Background(), &types.TranscriptResult{ID: "t1", Language: "en"}, nil)
	if err == nil {
		t.Fatal("lemur analyze func error = nil, want error when ASSEMBLYAI_API_KEY is unset")
	}
}

func TestRealTranscribeWithoutAPIKey(t *testing.T) {
	t.Setenv(config.APIKeyEnvVar, "")
	cfg := &config.Config{}

	_, err := RealTranscribe(context.Background(), "irrelevant.mkv", cfg)
	if err == nil {
		t.Fatal("RealTranscribe error = nil, want error when ASSEMBLYAI_API_KEY is unset")
	}
}

func TestMockTranscribeIsDeterministic(t *testing.T) {
	a, err := MockTranscribe(context.Background(), "/inbox/standup.mkv", nil)
	if err != nil {
		t.Fatalf("MockTranscribe error = %v", err)
	}
	b, err := MockTranscribe(context.Background(), "/other/dir/standup.mkv", nil)
	if err != nil {
		t.Fatalf("MockTranscribe error = %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("MockTranscribe ID = %q vs %q, want identical hash for same base name", a.ID, b.ID)
	}
	if a.Text != b.Text {
		t.Error("MockTranscribe text differs for the same base name")
	}
	if len(a.Chapters) != 2 || len(a.Utterances) != 3 {
		t.Errorf("MockTranscribe chapters/utterances = %d/%d, want 2/3", len(a.Chapters), len(a.Utterances))
	}
	if a.Language != "en" {
		t.Errorf("MockTranscribe language = %q, want \"en\"", a.Language)
	}
}

func TestMockAnalyzeShape(t *testing.T) {
	tr := &types.TranscriptResult{ID: "mock-abc123"}
	res, err := MockAnalyze(context.Background(), tr, nil)
	if err != nil {
		t.Fatalf("MockAnalyze error = %v", err)
	}
	if res.Title != "Mock analysis of mock-abc123" {
		t.Errorf("MockAnalyze title = %q", res.Title)
	}
	if len(res.Topics) != 2 || res.Topics[0].Slug != "product-roadmap" || res.Topics[1].Slug != "budget-review" {
		t.Errorf("MockAnalyze topics = %+v, want fixed product-roadmap/budget-review slugs", res.Topics)
	}
	if len(res.ActionItems) != 1 {
		t.Errorf("MockAnalyze action items = %+v, want exactly one", res.ActionItems)
	}
}
