// Package pipeline orchestrates: video -> transcript -> analysis -> library.
//
// The real work is delegated to two injected callables — a TranscribeFunc
// and an AnalyzeFunc — so the --mock CLI flag can swap in the deterministic
// fakes defined here instead of sprinkling conditionals through the
// pipeline.
//
// This is a port of scribe/pipeline.py; the mock transcripts and analyses
// are byte-for-byte identical to the Python ones.
package pipeline

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/fernando143/patro/internal/analyzer"
	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/library"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/transcriber"
	"github.com/fernando143/patro/internal/types"
)

// TranscribeFunc turns one video file into a transcript.
type TranscribeFunc func(ctx context.Context, videoPath string, cfg *config.Config) (*types.TranscriptResult, error)

// AnalyzeFunc distills a transcript into structured knowledge, given the
// topics already present in the library.
type AnalyzeFunc func(ctx context.Context, t *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error)

// ------------------------------------------------------------------- real fns

// RealTranscribe uploads the video to AssemblyAI and waits for the
// transcript, using the API key from the environment.
func RealTranscribe(ctx context.Context, videoPath string, cfg *config.Config) (*types.TranscriptResult, error) {
	apiKey, err := cfg.APIKey()
	if err != nil {
		return nil, err
	}
	return transcriber.Transcribe(ctx, videoPath, apiKey)
}

// MakeAnalyzeFunc returns the real analyzer selected by
// cfg.AnalyzerBackend: "lemur" calls AssemblyAI's hosted LLM with the API
// key from the environment; "kimi"/"claude" shell out to the local CLI.
func MakeAnalyzeFunc(cfg *config.Config) AnalyzeFunc {
	if cfg.AnalyzerBackend == "lemur" {
		return func(ctx context.Context, t *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error) {
			apiKey, err := cfg.APIKey()
			if err != nil {
				return nil, err
			}
			return analyzer.AnalyzeLeMUR(ctx, t.ID, apiKey, existing, t.Language)
		}
	}
	return func(ctx context.Context, t *types.TranscriptResult, existing []types.TopicRef) (*types.AnalysisResult, error) {
		return analyzer.AnalyzeCLI(ctx, t, existing, cfg)
	}
}

// ------------------------------------------------------------------- mock fns

// MockTranscribe returns a deterministic fake transcript derived from the
// file name (no API calls).
func MockTranscribe(_ context.Context, videoPath string, _ *config.Config) (*types.TranscriptResult, error) {
	base := filepath.Base(videoPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	sum := sha1.Sum([]byte(base)) //nolint:gosec // parity with Python's hashlib.sha1
	digest := hex.EncodeToString(sum[:])[:12]
	text := "This is a mock transcript for the recording '" + stem + "'. " +
		"The team discussed the product roadmap and the budget review. " +
		"No audio was actually processed; AssemblyAI was not called."
	return &types.TranscriptResult{
		ID:   "mock-" + digest,
		Text: text,
		Chapters: []types.Chapter{
			{Headline: "Roadmap discussion", Gist: "roadmap", Start: 0, End: 90000},
			{Headline: "Budget review", Gist: "budget", Start: 90000, End: 210000},
		},
		Utterances: []types.Utterance{
			{Speaker: "A", Text: "Welcome to the mock meeting about " + stem + "."},
			{Speaker: "B", Text: "Let's review the roadmap and then the budget."},
			{Speaker: "A", Text: "Agreed, the roadmap comes first."},
		},
		Language: "en",
	}, nil
}

// MockAnalyze returns a deterministic fake analysis with two sample topics
// (no API calls).
func MockAnalyze(_ context.Context, t *types.TranscriptResult, _ []types.TopicRef) (*types.AnalysisResult, error) {
	return &types.AnalysisResult{
		Title: "Mock analysis of " + t.ID,
		Summary: "Mock summary: this analysis was generated locally without calling AssemblyAI. " +
			"It stands in for the LeMUR output so the full pipeline can be verified offline.",
		KeyPoints: []string{"The pipeline ran end to end in mock mode", "No API key was required"},
		Decisions: []string{"Ship the mock mode as the default verification path"},
		ActionItems: []types.ActionItem{
			{Owner: "unassigned", Task: "Set ASSEMBLYAI_API_KEY and run a real transcription"},
		},
		Topics: []types.Topic{
			{
				Slug: "product-roadmap",
				Name: "Product roadmap",
				Content: "- The roadmap was reviewed during this mock meeting.\n" +
					"- Priorities were reaffirmed for the next quarter.",
			},
			{
				Slug: "budget-review",
				Name: "Budget review",
				Content: "- The budget was reviewed with no major deviations.\n" +
					"- A follow-up review was scheduled.",
			},
		},
	}, nil
}

// ------------------------------------------------------------------ pipeline

// ProcessVideo processes one video end to end. It returns the meeting note
// path, or "" when the file was skipped (already processed). Errors are
// propagated to the caller, which logs them.
func ProcessVideo(ctx context.Context, videoPath string, cfg *config.Config, st *state.State, tf TranscribeFunc, af AnalyzeFunc) (string, error) {
	if st.IsProcessed(videoPath) {
		logging.Infof("Skipping %s (already processed)", filepath.Base(videoPath))
		return "", nil
	}

	logging.Infof("Processing %s ...", filepath.Base(videoPath))
	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		return "", err
	}

	transcript, err := tf(ctx, videoPath, cfg)
	if err != nil {
		return "", err
	}
	analysis, err := af(ctx, transcript, lib.ExistingTopics())
	if err != nil {
		return "", err
	}
	notePath, err := lib.AddMeeting(transcript, analysis, videoPath)
	if err != nil {
		return "", err
	}

	if err := st.MarkProcessed(videoPath, transcript.ID); err != nil {
		return "", err
	}
	logging.Infof("Done: %s -> %s", filepath.Base(videoPath), notePath)
	return notePath, nil
}
