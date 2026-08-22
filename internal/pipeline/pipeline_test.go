package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/library"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/status"
	"github.com/fernando143/patro/internal/types"
	"github.com/fernando143/patro/internal/vectors"
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
	// TopicPromptLimit mirrors config.Load's default (50, design D7) since
	// this struct literal bypasses Load's defaulting. EmbeddingBackend is
	// deliberately left unset: newReconciler then fails fast on
	// embed.New("") and lib.Reconciler stays nil, preserving these tests'
	// pre-existing exact-slug-only behavior without spinning up a real
	// embedding backend. Dir is rooted in the same temporary directory so the
	// derived search index remains isolated from the repository.
	return &config.Config{Dir: root, Library: filepath.Join(root, "library"), TopicPromptLimit: 50}
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

func TestProcessVideoRebuildsSearchIndexAfterEachVideo(t *testing.T) {
	cfg := newTestCfg(t)
	videoDir := t.TempDir()
	st := state.New(t.TempDir())

	firstVideo := newTestVideo(t, videoDir, "first.mkv")
	firstAnalysis := func(context.Context, *types.TranscriptResult, []types.TopicRef) (*types.AnalysisResult, error) {
		return &types.AnalysisResult{
			Title:   "First planning session",
			Summary: "alphaonly",
			Topics:  []types.Topic{{Slug: "first-topic", Name: "First topic", Content: "First searchable detail."}},
		}, nil
	}
	if _, err := ProcessVideo(context.Background(), firstVideo, cfg, st, nil, MockTranscribe, firstAnalysis); err != nil {
		t.Fatalf("first ProcessVideo error = %v", err)
	}

	idx, err := searchindex.Open(cfg.SearchIndexDir())
	if err != nil {
		t.Fatalf("searchindex.Open after first video: %v", err)
	}
	hits, err := idx.Query("alphaonly", 10)
	if err != nil {
		idx.Close()
		t.Fatalf("Query after first video: %v", err)
	}
	if len(hits) != 1 || hits[0].Kind != searchindex.KindMeeting || hits[0].Title != "First planning session" {
		idx.Close()
		t.Fatalf("hits after first video = %+v, want first meeting", hits)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close after first query: %v", err)
	}

	secondVideo := newTestVideo(t, videoDir, "second.mkv")
	secondAnalysis := func(context.Context, *types.TranscriptResult, []types.TopicRef) (*types.AnalysisResult, error) {
		return &types.AnalysisResult{
			Title:   "Second launch review",
			Summary: "betaonly",
			Topics:  []types.Topic{{Slug: "second-topic", Name: "Second topic", Content: "Second searchable detail."}},
		}, nil
	}
	if _, err := ProcessVideo(context.Background(), secondVideo, cfg, st, nil, MockTranscribe, secondAnalysis); err != nil {
		t.Fatalf("second ProcessVideo error = %v", err)
	}

	idx, err = searchindex.Open(cfg.SearchIndexDir())
	if err != nil {
		t.Fatalf("searchindex.Open after second video: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	hits, err = idx.Query("betaonly", 10)
	if err != nil {
		t.Fatalf("Query after second video: %v", err)
	}
	if len(hits) != 1 || hits[0].Kind != searchindex.KindMeeting || hits[0].Title != "Second launch review" {
		t.Fatalf("hits after second video = %+v, want second meeting", hits)
	}
	if hits, err := idx.Query("alphaonly", 10); err != nil {
		t.Fatalf("Query for first video after second rebuild: %v", err)
	} else if len(hits) != 1 || hits[0].Kind != searchindex.KindMeeting || hits[0].Title != "First planning session" {
		t.Fatalf("first video missing after second rebuild: %+v", hits)
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

	cfg := &config.Config{BinaryPaths: map[string]string{"kimi": kimiPath}, Dir: dir, AnalyzerBackend: "kimi"}
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

// ------------------------------------------------------- Unit 5: topic_prompt_limit (D6/D7)

// TestProcessVideoTopicPromptLimitCapsAndOrdersMostRecentFirst is the
// checkpoint 5.3 fixture: 500 existing topics, each with a distinct dated
// section, prove the analyzer only ever sees at most cfg.TopicPromptLimit
// entries and that they are the most recently updated ones (Library.
// ExistingTopicsRecent, wired at pipeline.go's analyze call site per design
// D6 — BuildPrompt itself is untouched, see analyzer_test.go checkpoint 5.4).
func TestProcessVideoTopicPromptLimitCapsAndOrdersMostRecentFirst(t *testing.T) {
	dir := t.TempDir()
	video := newTestVideo(t, dir, "meeting.mkv")
	cfg := newTestCfg(t)
	cfg.TopicPromptLimit = 50
	st := state.New(t.TempDir())

	const total = 500
	topicsDir := filepath.Join(cfg.Library, "topics")
	if err := os.MkdirAll(topicsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		slug := fmt.Sprintf("topic-%03d", i)
		date := base.AddDate(0, 0, i).Format("2006-01-02")
		content := fmt.Sprintf("# Topic %03d\n\n## %s Some meeting\n\nSome content.\n", i, date)
		path := filepath.Join(topicsDir, slug+".md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	var gotExisting []types.TopicRef
	af := func(ctx context.Context, tr *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error) {
		gotExisting = existing
		return MockAnalyze(ctx, tr, existing)
	}

	if _, err := ProcessVideo(context.Background(), video, cfg, st, nil, MockTranscribe, af); err != nil {
		t.Fatalf("ProcessVideo error = %v", err)
	}

	if len(gotExisting) != cfg.TopicPromptLimit {
		t.Fatalf("len(existing) = %d, want %d (prompt must stay capped at topic_prompt_limit even with %d topics)",
			len(gotExisting), cfg.TopicPromptLimit, total)
	}
	// Most-recent-first: topic-499 has the latest date, so it must be
	// first; the 50th entry is topic-450 (indices 499..450 descending).
	if gotExisting[0].Slug != "topic-499" {
		t.Errorf("existing[0].Slug = %q, want %q (most recently updated topic first)", gotExisting[0].Slug, "topic-499")
	}
	if last := gotExisting[len(gotExisting)-1].Slug; last != "topic-450" {
		t.Errorf("existing[last].Slug = %q, want %q", last, "topic-450")
	}
}

// -------------------------------------------------- unwired reconciler gap

// TestNewReconcilerUnknownBackendReturnsNil covers the graceful-degradation
// path: an unset/unknown embedding_backend (e.g. the zero-value config used
// throughout this file's other tests, or --mock configs that never set one)
// must not fail pipeline processing — newReconciler logs and returns nil,
// so Library.Reconciler stays nil and AddMeetingCtx keeps today's
// exact-slug-only behavior (design D1).
func TestNewReconcilerUnknownBackendReturnsNil(t *testing.T) {
	cfg := &config.Config{Dir: t.TempDir(), EmbeddingBackend: "does-not-exist"}
	if r := newReconciler(cfg); r != nil {
		t.Errorf("newReconciler(unknown backend) = %#v, want nil", r)
	}
}

func TestNewReconcilerEmptyBackendReturnsNil(t *testing.T) {
	cfg := &config.Config{Dir: t.TempDir()}
	if r := newReconciler(cfg); r != nil {
		t.Errorf("newReconciler(empty backend) = %#v, want nil", r)
	}
}

// TestNewReconcilerValidBackendWiresSemanticReconciler proves the real
// wiring: a valid embedding_backend must produce a
// *library.SemanticReconciler with a document representer, a multi-vector
// store rooted at cfg.StateDir()/vectors/topics.json, the configured
// thresholds, a non-nil gray-zone decider, and the ledger path at
// cfg.StateDir()/reconciliation.json.
func TestNewReconcilerValidBackendWiresSemanticReconciler(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{BinaryPaths: map[string]string{"kimi": "kimi"},
		Dir:               dir,
		Library:           filepath.Join(dir, "library"),
		EmbeddingBackend:  "cybertron",
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
		AnalyzerBackend:   "kimi",
	}

	r := newReconciler(cfg)
	sr, ok := r.(*library.SemanticReconciler)
	if !ok {
		t.Fatalf("newReconciler(cybertron) = %#v (%T), want *library.SemanticReconciler", r, r)
	}
	if sr.Similarity == nil {
		t.Error("SemanticReconciler.Similarity = nil, want a topic-similarity implementation")
	}
	if sr.MergeThreshold != 0.90 {
		t.Errorf("MergeThreshold = %v, want 0.90", sr.MergeThreshold)
	}
	if sr.NewTopicThreshold != 0.70 {
		t.Errorf("NewTopicThreshold = %v, want 0.70", sr.NewTopicThreshold)
	}
	if sr.Decide == nil {
		t.Error("Decide (gray-zone decider) = nil, want GrayZoneCLI wired")
	}
	wantLedger := filepath.Join(cfg.StateDir(), "reconciliation.json")
	if sr.LedgerPath != wantLedger {
		t.Errorf("LedgerPath = %q, want %q", sr.LedgerPath, wantLedger)
	}

	// The store must be reachable at the documented, stable path (design
	// D10: ".state/vectors/topics.json") so a representation sync populates
	// the exact same store this reconciler queries.
	wantStore := filepath.Join(cfg.StateDir(), "vectors", "topics.json")
	if err := os.MkdirAll(filepath.Join(cfg.Library, "topics"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	sim, ok := sr.Similarity.(representationSimilarity)
	if !ok {
		t.Fatalf("Similarity = %T, want representationSimilarity", sr.Similarity)
	}
	v2, ok := sim.store.(*vectors.V2Store)
	if !ok {
		t.Fatalf("store = %T, want *vectors.V2Store", sim.store)
	}
	if err := v2.Sync(context.Background(), filepath.Join(cfg.Library, "topics"), sim.representer); err != nil {
		t.Fatalf("Sync error = %v", err)
	}
	if _, err := os.Stat(wantStore); err != nil {
		t.Errorf("vector store file %s not written after Upsert: %v", wantStore, err)
	}
}

// TestNewReconcilerUsesClaudePathForClaudeAnalyzerBackend covers the
// binary-path selection judgment call: the gray-zone LLM binary follows
// cfg.AnalyzerBackend, mirroring MakeAnalyzeFunc's own kimi/claude choice.
func TestNewReconcilerUsesClaudePathForClaudeAnalyzerBackend(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fake-claude")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho yes\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	cfg := &config.Config{BinaryPaths: map[string]string{"claude": scriptPath, "kimi": "kimi-should-not-be-used"},
		Dir:              dir,
		EmbeddingBackend: "cybertron",
		AnalyzerBackend:  "claude",
	}

	r := newReconciler(cfg)
	sr, ok := r.(*library.SemanticReconciler)
	if !ok {
		t.Fatalf("newReconciler(cybertron) = %#v (%T), want *library.SemanticReconciler", r, r)
	}
	same, err := sr.Decide(context.Background(), types.Topic{Name: "a"}, types.TopicRef{Name: "b", Slug: "b"})
	if err != nil {
		t.Fatalf("Decide error = %v, want nil (fake claude script always answers yes)", err)
	}
	if !same {
		t.Error("Decide = false, want true (fake claude script answers yes, proving claude_path was used)")
	}
}

// TestProcessVideoWiresRealReconcilerMergesSemanticDuplicate is the runtime
// harness for the reconciler-wiring gap: it exercises ProcessVideo end to
// end with the real cybertron representer and a real *vectors.V2Store rooted
// at the exact path newReconciler constructs, proving lib.Reconciler is
// genuinely wired (not left nil) and AddMeetingCtx (not the legacy
// AddMeeting) is called.
//
// The test pre-seeds the representation store exactly as a prior sync would
// have, at the same path/backend/model_version newReconciler uses, to prove
// the plumbing this unit owns is correct without reimplementing maintenance.
func TestProcessVideoWiresRealReconcilerMergesSemanticDuplicate(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{BinaryPaths: map[string]string{"kimi": "kimi"},
		Dir:               dir,
		Library:           filepath.Join(dir, "library"),
		EmbeddingBackend:  "cybertron",
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
		TopicPromptLimit:  50,
		AnalyzerBackend:   "kimi",
	}

	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		t.Fatalf("NewLibrary error = %v", err)
	}
	const (
		existingSlug = "product-roadmap"
		existingName = "Product roadmap"
		content      = "We discussed shipping the new dashboard feature next quarter."
	)
	if err := os.WriteFile(filepath.Join(lib.TopicsDir, existingSlug+".md"), []byte("# "+existingName+"\n\n"+content+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	store, embedder, err := vectors.OpenRepresentationStore(context.Background(), cfg.StateDir(), cfg.EmbeddingBackend)
	if err != nil {
		t.Fatalf("OpenRepresentationStore error = %v", err)
	}
	if err := store.Sync(context.Background(), lib.TopicsDir, embedder); err != nil {
		t.Fatalf("Sync error = %v", err)
	}

	video := newTestVideo(t, dir, "meeting.mkv")
	st := state.New(t.TempDir())
	af := func(ctx context.Context, tr *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error) {
		return &types.AnalysisResult{
			Title: "Follow-up",
			Topics: []types.Topic{
				{Slug: "roadmap-followup", Name: existingName, Content: content},
			},
		}, nil
	}

	if _, err := ProcessVideo(context.Background(), video, cfg, st, nil, MockTranscribe, af); err != nil {
		t.Fatalf("ProcessVideo error = %v", err)
	}

	topics, err := filepath.Glob(filepath.Join(cfg.Library, "topics", "*.md"))
	if err != nil {
		t.Fatalf("Glob error = %v", err)
	}
	if len(topics) != 1 {
		t.Fatalf("topics = %v, want exactly 1 (semantic merge into %q expected)", topics, existingSlug)
	}
	data, err := os.ReadFile(topics[0])
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if !strings.Contains(string(data), "Merged from proposed slug `roadmap-followup`") {
		t.Errorf("topic file missing merge annotation:\n%s", data)
	}

	ledgerData, err := os.ReadFile(filepath.Join(cfg.StateDir(), "reconciliation.json"))
	if err != nil {
		t.Fatalf("reconciliation ledger not written: %v", err)
	}
	if !strings.Contains(string(ledgerData), "roadmap-followup") {
		t.Errorf("ledger missing merge entry:\n%s", ledgerData)
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
