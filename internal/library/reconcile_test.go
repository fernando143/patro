package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/types"
	"github.com/fernando143/patro/internal/vectors"
)

// fakeEmbedder is a deterministic, dependency-free Embedder stub for tests.
type fakeEmbedder struct {
	vec []float32
	err error
}

func (f fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return f.vec, f.err
}

// fakeStore is a deterministic, dependency-free NearestFinder stub for
// tests — it never touches internal/vectors' real (concurrent) store, so
// ErrRebuilding and other failure modes can be simulated synchronously.
type fakeStore struct {
	results []vectors.Result
	err     error
}

type fakeMultiStore struct {
	results []embed.RankedResult
	err     error
}

func (f fakeMultiStore) NearestRepresentations(context.Context, embed.Representation, embed.ScoreMode, int) ([]embed.RankedResult, error) {
	return f.results, f.err
}

type fakeRepresenter struct{}

func (fakeRepresenter) Represent(context.Context, embed.Document) (*embed.Representation, error) {
	return &embed.Representation{Chunks: []embed.Chunk{{Kind: "content", Ordinal: 0, TokenCount: 1, Vector: []float32{1, 0}}}}, nil
}

func (f fakeStore) Nearest(_ []float32, _ int) ([]vectors.Result, error) {
	return f.results, f.err
}

func staticDecider(same bool, err error) GrayZoneDecider {
	return func(_ context.Context, _ types.Topic, _ types.TopicRef) (bool, error) {
		return same, err
	}
}

func TestSemanticReconcilerThreeBand(t *testing.T) {
	candidate := types.Topic{Slug: "x-y", Name: "X Y", Content: "content"}
	existing := []types.TopicRef{{Slug: "react-hooks", Name: "React Hooks"}}

	tests := []struct {
		name             string
		score            float64
		decide           GrayZoneDecider
		wantMerged       bool
		wantFlagged      bool
		wantSlug         string
		wantDecideCalled bool
	}{
		{
			name:       "high similarity merges",
			score:      0.93,
			wantMerged: true,
			wantSlug:   "react-hooks",
		},
		{
			name:     "low similarity new topic",
			score:    0.5,
			wantSlug: "x-y",
		},
		{
			name:             "gray zone LLM confirms merge",
			score:            0.80,
			decide:           staticDecider(true, nil),
			wantMerged:       true,
			wantSlug:         "react-hooks",
			wantDecideCalled: true,
		},
		{
			name:             "gray zone LLM denies merge",
			score:            0.80,
			decide:           staticDecider(false, nil),
			wantSlug:         "x-y",
			wantDecideCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			var decide GrayZoneDecider
			if tt.decide != nil {
				decide = func(ctx context.Context, c types.Topic, n types.TopicRef) (bool, error) {
					calls++
					return tt.decide(ctx, c, n)
				}
			}

			r := &SemanticReconciler{
				Embedder:          fakeEmbedder{vec: []float32{1, 0}},
				Store:             fakeStore{results: []vectors.Result{{ID: "react-hooks", Score: tt.score}}},
				MergeThreshold:    0.90,
				NewTopicThreshold: 0.70,
				Decide:            decide,
			}

			res, err := r.Reconcile(context.Background(), candidate, existing)
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if res.Merged != tt.wantMerged {
				t.Errorf("Merged = %v, want %v", res.Merged, tt.wantMerged)
			}
			if res.Flagged != tt.wantFlagged {
				t.Errorf("Flagged = %v, want %v", res.Flagged, tt.wantFlagged)
			}
			if res.Slug != tt.wantSlug {
				t.Errorf("Slug = %q, want %q", res.Slug, tt.wantSlug)
			}
			if res.ProposedSlug != "x-y" {
				t.Errorf("ProposedSlug = %q, want %q", res.ProposedSlug, "x-y")
			}
			wantCalls := 0
			if tt.wantDecideCalled {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Errorf("Decide called %d times, want %d (exactly one LLM call in the gray zone)", calls, wantCalls)
			}
		})
	}
}

func TestSemanticReconcilerNoExistingTopicsIsNewUnflagged(t *testing.T) {
	r := &SemanticReconciler{
		Embedder:          fakeEmbedder{vec: []float32{1, 0}},
		Store:             fakeStore{results: nil},
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
	}
	res, err := r.Reconcile(context.Background(), types.Topic{Slug: "x-y", Name: "X Y"}, nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Merged || res.Flagged {
		t.Errorf("res = %+v, want a plain new unflagged topic", res)
	}
	if res.Slug != "x-y" {
		t.Errorf("Slug = %q, want %q", res.Slug, "x-y")
	}
}

func TestSemanticReconcilerUsesMultiVectorStoreWhenAvailable(t *testing.T) {
	r := &SemanticReconciler{
		Embedder:          fakeEmbedder{err: fmt.Errorf("legacy one-vector path must not run")},
		Store:             fakeStore{err: fmt.Errorf("legacy store must not run")},
		Representer:       fakeRepresenter{},
		MultiStore:        fakeMultiStore{results: []embed.RankedResult{{ID: "react-hooks", Score: 0.95}}},
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
	}
	res, err := r.Reconcile(context.Background(), types.Topic{Slug: "x-y", Name: "X Y", Content: "content"}, []types.TopicRef{{Slug: "react-hooks", Name: "React Hooks"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.Merged || res.Slug != "react-hooks" || res.Score != 0.95 {
		t.Fatalf("resolution = %+v, want multi-vector merge", res)
	}
}

func TestSemanticReconcilerGrayZoneErrorSafeFails(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "reconciliation.json")
	calls := 0
	r := &SemanticReconciler{
		Embedder:          fakeEmbedder{vec: []float32{1, 0}},
		Store:             fakeStore{results: []vectors.Result{{ID: "react-hooks", Score: 0.80}}},
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
		Decide: func(context.Context, types.Topic, types.TopicRef) (bool, error) {
			calls++
			return false, context.DeadlineExceeded
		},
		LedgerPath: ledgerPath,
	}

	res, err := r.Reconcile(context.Background(),
		types.Topic{Slug: "x-y", Name: "X Y"},
		[]types.TopicRef{{Slug: "react-hooks", Name: "React Hooks"}},
	)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Merged {
		t.Error("a gray-zone LLM error/timeout MUST NOT auto-merge under any circumstance")
	}
	if !res.Flagged {
		t.Error("a gray-zone LLM error/timeout MUST flag the new topic needs-reconciliation")
	}
	if res.Slug != "x-y" {
		t.Errorf("Slug = %q, want the proposed slug %q (new topic)", res.Slug, "x-y")
	}
	if calls != 1 {
		t.Errorf("Decide called %d times, want exactly 1", calls)
	}

	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("ledger not written: %v", err)
	}
	if !strings.Contains(string(data), `"flagged": true`) {
		t.Errorf("ledger entry missing flagged=true:\n%s", data)
	}
}

func TestSemanticReconcilerErrRebuildingSafeFails(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "reconciliation.json")
	r := &SemanticReconciler{
		Embedder:          fakeEmbedder{vec: []float32{1, 0}},
		Store:             fakeStore{err: vectors.ErrRebuilding},
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
		LedgerPath:        ledgerPath,
	}

	res, err := r.Reconcile(context.Background(), types.Topic{Slug: "x-y", Name: "X Y"}, nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Merged {
		t.Error("ErrRebuilding MUST NOT be treated as a merge signal")
	}
	if !res.Flagged {
		t.Error("ErrRebuilding MUST flag the new topic needs-reconciliation, so a later reconcile pass picks it up")
	}
	if res.Slug != "x-y" {
		t.Errorf("Slug = %q, want %q (no meeting lost during rebuild)", res.Slug, "x-y")
	}

	if _, err := os.Stat(ledgerPath); err != nil {
		t.Errorf("ledger not written for the ErrRebuilding fail-safe path: %v", err)
	}
}

func TestAddMeetingCtxMergeAnnotatesAndLedgers(t *testing.T) {
	l := newTestLibrary(t)
	writeFile(t, filepath.Join(l.TopicsDir, "react-hooks.md"), "# React Hooks\n")

	ledgerPath := filepath.Join(l.Root, ".state", "reconciliation.json")
	l.Reconciler = &SemanticReconciler{
		Embedder:          fakeEmbedder{vec: []float32{1, 0}},
		Store:             fakeStore{results: []vectors.Result{{ID: "react-hooks", Score: 0.93}}},
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
		LedgerPath:        ledgerPath,
	}

	transcript := &types.TranscriptResult{ID: "t1", Text: "hi"}
	analysis := &types.AnalysisResult{
		Title:  "Weekly Sync",
		Topics: []types.Topic{{Slug: "x-y", Name: "X Y", Content: "some content"}},
	}

	if _, err := l.AddMeetingCtx(context.Background(), transcript, analysis, "/inbox/x.mkv"); err != nil {
		t.Fatalf("AddMeetingCtx: %v", err)
	}

	if _, err := os.Stat(filepath.Join(l.TopicsDir, "x-y.md")); !os.IsNotExist(err) {
		t.Error("expected no x-y.md: the candidate must merge into react-hooks.md, not create a new file")
	}

	got := readFile(t, filepath.Join(l.TopicsDir, "react-hooks.md"))
	if !strings.Contains(got, "Merged from proposed slug `x-y`") {
		t.Errorf("markdown annotation missing merge provenance:\n%s", got)
	}
	if !strings.Contains(got, "0.93") {
		t.Errorf("markdown annotation missing cosine score:\n%s", got)
	}

	data := readFile(t, ledgerPath)
	if !strings.Contains(data, `"proposed_slug": "x-y"`) {
		t.Errorf("ledger entry missing proposed_slug:\n%s", data)
	}
	if !strings.Contains(data, `"merged": true`) {
		t.Errorf("ledger entry missing merged=true:\n%s", data)
	}
}

func TestAddMeetingNilReconcilerUnaffected(t *testing.T) {
	l := newTestLibrary(t)
	// l.Reconciler is nil (zero value): behavior and calls must be
	// identical to the legacy AddMeeting/AppendTopicSection path.
	transcript := &types.TranscriptResult{ID: "t1", Text: "hi"}
	analysis := &types.AnalysisResult{
		Title:  "Weekly Sync",
		Topics: []types.Topic{{Slug: "roadmap", Name: "Roadmap", Content: "- item"}},
	}

	notePath, err := l.AddMeeting(transcript, analysis, "/inbox/x.mkv")
	if err != nil {
		t.Fatalf("AddMeeting: %v", err)
	}
	if _, err := os.Stat(notePath); err != nil {
		t.Errorf("meeting note not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.TopicsDir, "roadmap.md")); err != nil {
		t.Errorf("topic file not written at the exact-slug path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.Root, ".state", "reconciliation.json")); !os.IsNotExist(err) {
		t.Error("no ledger should be written when Reconciler is nil")
	}
}

func TestExistingTopicsRecent(t *testing.T) {
	l := newTestLibrary(t)
	writeFile(t, filepath.Join(l.TopicsDir, "a.md"), "# A\n\n## 2026-01-01 — X\n")
	writeFile(t, filepath.Join(l.TopicsDir, "b.md"), "# B\n\n## 2026-03-01 — X\n")
	writeFile(t, filepath.Join(l.TopicsDir, "c.md"), "# C\n")

	got := l.ExistingTopicsRecent(2)
	want := []types.TopicRef{{Slug: "b", Name: "B"}, {Slug: "a", Name: "A"}}
	if !slices.Equal(got, want) {
		t.Errorf("ExistingTopicsRecent(2) = %+v, want %+v", got, want)
	}

	all := l.ExistingTopicsRecent(-1)
	if len(all) != 3 {
		t.Errorf("ExistingTopicsRecent(-1) = %d entries, want all 3", len(all))
	}
}

// --- GrayZoneCLI: the concrete, subprocess-based gray-zone decider ---

func writeScript(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-cli.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGrayZoneCLIYes(t *testing.T) {
	script := writeScript(t, t.TempDir(), `echo "yes"`)
	decide := GrayZoneCLI(script, time.Second)
	same, err := decide(context.Background(), types.Topic{Name: "X"}, types.TopicRef{Name: "Y", Slug: "y"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !same {
		t.Error("same = false, want true for a \"yes\" answer")
	}
}

func TestGrayZoneCodexYes(t *testing.T) {
	script := writeScript(t, t.TempDir(), `echo '{"type":"item.completed","item":{"type":"agent_message","text":"yes"}}'`)
	decide := GrayZoneCodex(script, time.Second)
	same, err := decide(context.Background(), types.Topic{Name: "X"}, types.TopicRef{Name: "Y", Slug: "y"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !same {
		t.Error("same = false, want true for a Codex \"yes\" answer")
	}
}

func TestGrayZoneCLINo(t *testing.T) {
	script := writeScript(t, t.TempDir(), `echo "no"`)
	decide := GrayZoneCLI(script, time.Second)
	same, err := decide(context.Background(), types.Topic{Name: "X"}, types.TopicRef{Name: "Y", Slug: "y"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if same {
		t.Error("same = true, want false for a \"no\" answer")
	}
}

func TestGrayZoneCLINonZeroExit(t *testing.T) {
	script := writeScript(t, t.TempDir(), `exit 1`)
	decide := GrayZoneCLI(script, time.Second)
	if _, err := decide(context.Background(), types.Topic{Name: "X"}, types.TopicRef{Name: "Y", Slug: "y"}); err == nil {
		t.Fatal("decide() error = nil, want an error on non-zero exit")
	}
}

func TestGrayZoneCLITimeout(t *testing.T) {
	// exec (rather than a plain "sleep 5") replaces the shell with sleep
	// itself, so SIGKILL on context timeout reaches the process actually
	// holding stdout/stderr open and the test returns promptly instead of
	// blocking for the full sleep duration.
	script := writeScript(t, t.TempDir(), `exec sleep 5`)
	decide := GrayZoneCLI(script, 50*time.Millisecond)
	_, err := decide(context.Background(), types.Topic{Name: "X"}, types.TopicRef{Name: "Y", Slug: "y"})
	if err == nil {
		t.Fatal("decide() error = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to mention a timeout", err)
	}
}

// --- ReadLedger / ReconcileFlagged ("patro reconcile", Unit 7) ---

func TestReadLedgerMissingFileReturnsEmpty(t *testing.T) {
	entries, err := ReadLedger(filepath.Join(t.TempDir(), "reconciliation.json"))
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want empty for a missing ledger", entries)
	}
}

func TestReadLedgerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconciliation.json")
	want := LedgerEntry{Slug: "x-y", Name: "X Y", ProposedSlug: "x-y", Score: 0.5, Flagged: true, Timestamp: time.Now().UTC()}
	if err := appendLedger(path, want); err != nil {
		t.Fatalf("appendLedger: %v", err)
	}

	got, err := ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(got) != 1 || got[0].Slug != want.Slug || !got[0].Flagged {
		t.Fatalf("entries = %+v, want one flagged entry for %q", got, want.Slug)
	}
}

func TestReadLedgerCorruptFileReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconciliation.json")
	writeFile(t, path, "{not json")
	if _, err := ReadLedger(path); err == nil {
		t.Error("ReadLedger() error = nil, want an error for corrupt JSON")
	}
}

// TestCountFlagged proves the latest-per-slug dedupe: an older flagged
// record superseded by a later merge (or a later reflag) must not be
// double-counted or counted as still-pending.
func TestCountFlagged(t *testing.T) {
	entries := []LedgerEntry{
		{Slug: "a", Flagged: true, Timestamp: time.Unix(1, 0)},
		{Slug: "a", Flagged: false, Timestamp: time.Unix(2, 0)}, // later: merged, no longer flagged
		{Slug: "b", Flagged: true, Timestamp: time.Unix(1, 0)},
		{Slug: "c", Flagged: false, Timestamp: time.Unix(1, 0)},
	}
	if got := CountFlagged(entries); got != 1 {
		t.Errorf("CountFlagged() = %d, want 1 (only slug b is still flagged)", got)
	}
	if got := CountFlagged(nil); got != 0 {
		t.Errorf("CountFlagged(nil) = %d, want 0", got)
	}
}

func TestReconcileFlaggedNilReconcilerIsNoop(t *testing.T) {
	l := newTestLibrary(t)
	merged, err := l.ReconcileFlagged(context.Background(), filepath.Join(t.TempDir(), "reconciliation.json"), nil)
	if err != nil {
		t.Fatalf("ReconcileFlagged: %v", err)
	}
	if merged != 0 {
		t.Errorf("merged = %d, want 0 (nil Reconciler)", merged)
	}
}

func TestReconcileFlaggedMergesOnNowMatchingScore(t *testing.T) {
	l := newTestLibrary(t)
	writeFile(t, filepath.Join(l.TopicsDir, "react-hooks.md"), "# React Hooks\n")
	writeFile(t, filepath.Join(l.TopicsDir, "x-y.md"), "# X Y\n\nsome flagged content\n")

	ledgerPath := filepath.Join(l.Root, ".state", "reconciliation.json")
	if err := appendLedger(ledgerPath, LedgerEntry{
		Slug: "x-y", Name: "X Y", ProposedSlug: "x-y", Flagged: true, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("appendLedger: %v", err)
	}

	l.Reconciler = &SemanticReconciler{
		Embedder:          fakeEmbedder{vec: []float32{1, 0}},
		Store:             fakeStore{results: []vectors.Result{{ID: "react-hooks", Score: 0.95}}},
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
		LedgerPath:        ledgerPath,
	}

	var progress [][2]int
	merged, err := l.ReconcileFlagged(context.Background(), ledgerPath, func(done, total int) {
		progress = append(progress, [2]int{done, total})
	})
	if err != nil {
		t.Fatalf("ReconcileFlagged: %v", err)
	}
	if merged != 1 {
		t.Fatalf("merged = %d, want 1", merged)
	}
	if len(progress) == 0 {
		t.Error("onProgress was never called")
	}

	if _, err := os.Stat(filepath.Join(l.TopicsDir, "x-y.md")); !os.IsNotExist(err) {
		t.Error("expected x-y.md to be removed once merged into react-hooks.md")
	}
	got := readFile(t, filepath.Join(l.TopicsDir, "react-hooks.md"))
	if !strings.Contains(got, "some flagged content") {
		t.Errorf("react-hooks.md missing the merged content:\n%s", got)
	}
	if !strings.Contains(got, "Merged from proposed slug `x-y`") {
		t.Errorf("react-hooks.md missing merge provenance:\n%s", got)
	}
}

func TestReconcileFlaggedStillNoMatchLeavesTopicUntouched(t *testing.T) {
	l := newTestLibrary(t)
	writeFile(t, filepath.Join(l.TopicsDir, "x-y.md"), "# X Y\n\nsome content\n")

	ledgerPath := filepath.Join(l.Root, ".state", "reconciliation.json")
	if err := appendLedger(ledgerPath, LedgerEntry{
		Slug: "x-y", Name: "X Y", ProposedSlug: "x-y", Flagged: true, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("appendLedger: %v", err)
	}

	l.Reconciler = &SemanticReconciler{
		Embedder:          fakeEmbedder{vec: []float32{1, 0}},
		Store:             fakeStore{results: nil}, // still nothing to compare against
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
		LedgerPath:        ledgerPath,
	}

	merged, err := l.ReconcileFlagged(context.Background(), ledgerPath, nil)
	if err != nil {
		t.Fatalf("ReconcileFlagged: %v", err)
	}
	if merged != 0 {
		t.Errorf("merged = %d, want 0 (still no match)", merged)
	}
	if _, err := os.Stat(filepath.Join(l.TopicsDir, "x-y.md")); err != nil {
		t.Error("x-y.md must still exist: it was not merged")
	}
}

func TestReconcileFlaggedSkipsAlreadyRemovedTopicFile(t *testing.T) {
	l := newTestLibrary(t)
	// No x-y.md on disk: it was already merged/removed by an earlier pass.
	ledgerPath := filepath.Join(l.Root, ".state", "reconciliation.json")
	if err := appendLedger(ledgerPath, LedgerEntry{
		Slug: "x-y", Name: "X Y", ProposedSlug: "x-y", Flagged: true, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("appendLedger: %v", err)
	}

	l.Reconciler = &SemanticReconciler{
		Embedder:          fakeEmbedder{vec: []float32{1, 0}},
		Store:             fakeStore{results: []vectors.Result{{ID: "react-hooks", Score: 0.95}}},
		MergeThreshold:    0.90,
		NewTopicThreshold: 0.70,
		LedgerPath:        ledgerPath,
	}

	merged, err := l.ReconcileFlagged(context.Background(), ledgerPath, nil)
	if err != nil {
		t.Fatalf("ReconcileFlagged: %v", err)
	}
	if merged != 0 {
		t.Errorf("merged = %d, want 0: a missing flagged topic file must be skipped, not error", merged)
	}
}

func TestGrayZoneCLIArgvNotShellInterpreted(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.txt")
	script := writeScript(t, dir, fmt.Sprintf(`echo "$2" > %s`, promptFile))

	dangerous := "candidate `touch " + filepath.Join(dir, "pwned") + "` $(touch " + filepath.Join(dir, "pwned2") + ")"
	decide := GrayZoneCLI(script, time.Second)
	if _, err := decide(context.Background(), types.Topic{Name: dangerous, Content: "c"}, types.TopicRef{Name: "Y", Slug: "y"}); err != nil {
		t.Fatalf("decide: %v", err)
	}

	got := readFile(t, promptFile)
	if !strings.Contains(got, dangerous) {
		t.Errorf("prompt argument was not passed through verbatim:\ngot:  %q\nwant substring: %q", got, dangerous)
	}
	for _, pwned := range []string{filepath.Join(dir, "pwned"), filepath.Join(dir, "pwned2")} {
		if _, err := os.Stat(pwned); err == nil {
			t.Errorf("shell metacharacters in the prompt were interpreted; %s was created", pwned)
		}
	}
}
