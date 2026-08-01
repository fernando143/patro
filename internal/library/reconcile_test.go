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
