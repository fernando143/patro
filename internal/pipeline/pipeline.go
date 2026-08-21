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
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fernando143/patro/internal/analyzer"
	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/library"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/status"
	"github.com/fernando143/patro/internal/transcriber"
	"github.com/fernando143/patro/internal/types"
	"github.com/fernando143/patro/internal/vectors"
)

// grayZoneTimeoutSeconds bounds a single gray-zone reconciliation LLM call
// (library.GrayZoneCLI): a short yes/no question about two topics, not a
// full transcript analysis, so it uses a much smaller budget than the
// analyzer's own CLI timeout (internal/analyzer/cli.go's cliTimeoutSeconds
// = 600s).
const grayZoneTimeoutSeconds = 60

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
// key from the environment; "kimi"/"claude"/"codex" shell out to a local CLI.
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

// newReconciler builds the production Reconciler wired to the configured
// multi-vector representation backend and store (design D1/D2/D7/D9/D10).
// It returns nil — never an error — on any construction failure
// (unknown/misconfigured embedding_backend, a backend without document
// representation support, or a store that cannot be initialized): Library.
// Reconciler is nil-safe and falls back to today's exact-slug-only behavior
// (design D1), so a missing/misconfigured representation backend never blocks
// video processing. A dirty or missing v2 snapshot remains wired and
// safe-fails through vectors.ErrV2NeedsRebuild during reconciliation rather
// than querying an incomplete representation set.
// NewReconciler is the exported form of newReconciler, so cmd/patro can wire
// the identical production Reconciler for "patro reconcile" (Unit 7) without
// duplicating this construction logic.
func NewReconciler(cfg *config.Config) library.Reconciler {
	return newReconciler(cfg)
}

func newReconciler(cfg *config.Config) library.Reconciler {
	embedder, err := embed.New(cfg.EmbeddingBackend)
	if err != nil {
		logging.Warnf("reconciliation disabled: %v", err)
		return nil
	}

	representer, ok := embedder.(library.DocumentRepresenter)
	if !ok {
		logging.Warnf("reconciliation disabled: embedding backend %q does not support document representations", embedder.Name())
		return nil
	}

	storePath := filepath.Join(cfg.StateDir(), "vectors", "topics.json")
	sample, err := representer.Represent(context.Background(), embed.Document{ID: "identity", Text: "# Identity\n\nidentity"})
	if err != nil {
		logging.Warnf("reconciliation disabled: cannot initialize representation identity: %v", err)
		return nil
	}
	if sample == nil {
		logging.Warnf("reconciliation disabled: representation backend returned nil identity")
		return nil
	}
	v2 := vectors.NewV2Store(storePath, sample.Identity(), vectors.OSCommitFS{})

	// The gray-zone LLM binary follows the configured analyzer backend,
	// mirroring MakeAnalyzeFunc's own CLI choice: *_path values are always
	// populated with a default (internal/config),
	// so this never needs its own separate config key. lemur (hosted, no
	// local CLI) falls back to kimi_path, matching kimi's status as the
	// project's default local CLI.
	binaryPath := cfg.KimiPath
	if cfg.AnalyzerBackend == "claude" {
		binaryPath = cfg.ClaudePath
	} else if cfg.AnalyzerBackend == "codex" {
		binaryPath = cfg.CodexPath
	}

	decide := library.GrayZoneCLI(binaryPath, grayZoneTimeoutSeconds*time.Second)
	if cfg.AnalyzerBackend == "codex" {
		decide = library.GrayZoneCodex(binaryPath, grayZoneTimeoutSeconds*time.Second)
	}

	return &library.SemanticReconciler{
		Representer:       representer,
		MultiStore:        v2,
		MergeThreshold:    cfg.MergeThreshold,
		NewTopicThreshold: cfg.NewTopicThreshold,
		Decide:            decide,
		LedgerPath:        filepath.Join(cfg.StateDir(), "reconciliation.json"),
	}
}

// ProcessVideo processes one video end to end. It returns the meeting note
// path, or "" when the file was skipped (already processed). Errors are
// propagated to the caller, which logs them.
func ProcessVideo(ctx context.Context, videoPath string, cfg *config.Config, st *state.State, tracker *status.Tracker, tf TranscribeFunc, af AnalyzeFunc) (string, error) {
	if st.IsProcessed(videoPath) {
		logging.Infof("Skipping %s (already processed)", filepath.Base(videoPath))
		return "", nil
	}

	logging.Infof("Processing %s ...", filepath.Base(videoPath))
	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		return "", err
	}
	lib.Reconciler = newReconciler(cfg)

	tracker.Start(videoPath)
	tracker.Stage(status.StageTranscribing)
	transcript, err := tf(ctx, videoPath, cfg)
	if err != nil {
		return "", err
	}
	tracker.Stage(status.StageAnalyzing)
	analysis, err := af(ctx, transcript, lib.ExistingTopicsRecent(cfg.TopicPromptLimit))
	if err != nil {
		return "", err
	}
	tracker.Stage(status.StageWriting)
	notePath, err := lib.AddMeetingCtx(ctx, transcript, analysis, videoPath)
	if err != nil {
		return "", err
	}
	if err := rebuildSearchIndex(ctx, cfg); err != nil {
		return "", err
	}

	if err := st.MarkProcessed(videoPath, transcript.ID); err != nil {
		return "", err
	}
	logging.Infof("Done: %s -> %s", filepath.Base(videoPath), notePath)
	tracker.Done(videoPath, analysis.Title)
	return notePath, nil
}

// rebuildSearchIndex refreshes the derived BM25 index from the Markdown
// library after a successful meeting write. The source files are written
// before this call and the video is marked processed only after it succeeds,
// so a failed rebuild leaves the video eligible for a retry.
func rebuildSearchIndex(ctx context.Context, cfg *config.Config) error {
	idx, err := searchindex.Open(cfg.SearchIndexDir())
	if err != nil {
		return fmt.Errorf("open search index: %w", err)
	}

	rebuildErr := idx.Rebuild(ctx, filepath.Join(cfg.Library, "topics"), filepath.Join(cfg.Library, "meetings"))
	closeErr := idx.Close()
	if rebuildErr != nil {
		return fmt.Errorf("rebuild search index: %w", rebuildErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close search index: %w", closeErr)
	}
	return nil
}
