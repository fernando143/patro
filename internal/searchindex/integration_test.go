package searchindex_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/vectors"
)

// Task 3.6 — Rebuild() must reconstruct BOTH indexes from markdown alone
// after deleting .state/{vectors,search-index}: they are genuinely
// derived/reconstructable stores, not sources of truth, exactly like the
// existing knowledge library's own index.md.
func TestBothIndexesRecoverFromMarkdownAfterStateDeletion(t *testing.T) {
	root := t.TempDir()
	topicsDir := filepath.Join(root, "topics")
	meetingsDir := filepath.Join(root, "meetings")
	if err := os.MkdirAll(topicsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(meetingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMD(t, topicsDir, "roadmap", "Roadmap", "Q3 planning and deployment goals.")
	writeMD(t, topicsDir, "onboarding", "Onboarding", "New hire checklist.")
	writeMD(t, meetingsDir, "2026-01-05-standup", "Standup", "Discussed the roadmap.")

	stateDir := filepath.Join(root, ".state")
	vectorsPath := filepath.Join(stateDir, "vectors", "topics.json")
	searchIndexPath := filepath.Join(stateDir, "search-index")

	// First-time build: both stores start empty, get rebuilt from markdown.
	vs, err := vectors.NewStore(vectorsPath, embed.NewNop(4), "v1")
	if err != nil {
		t.Fatalf("vectors.NewStore: %v", err)
	}
	if err := vs.Rebuild(context.Background(), topicsDir, nil); err != nil {
		t.Fatalf("vectors Rebuild: %v", err)
	}

	si, err := searchindex.Open(searchIndexPath)
	if err != nil {
		t.Fatalf("searchindex.Open: %v", err)
	}
	if err := si.Rebuild(context.Background(), topicsDir, meetingsDir); err != nil {
		t.Fatalf("searchindex Rebuild: %v", err)
	}
	if err := si.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate an operator (or corruption recovery) deleting both derived
	// state directories entirely, per the design's rollback story.
	if err := os.RemoveAll(filepath.Join(stateDir, "vectors")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(searchIndexPath); err != nil {
		t.Fatal(err)
	}

	// Reopen (fresh, empty) and rebuild both — full state must recover from
	// topics/*.md and meetings/*.md alone, with no other input.
	vs2, err := vectors.NewStore(vectorsPath, embed.NewNop(4), "v1")
	if err != nil {
		t.Fatalf("vectors.NewStore (after deletion): %v", err)
	}
	if !vs2.NeedsRebuild() {
		t.Fatal("vectors NeedsRebuild() after deletion = false, want true")
	}
	if err := vs2.Rebuild(context.Background(), topicsDir, nil); err != nil {
		t.Fatalf("vectors Rebuild (recovery): %v", err)
	}
	vresults, err := vs2.Nearest([]float32{1, 0, 0, 0}, 10)
	if err != nil {
		t.Fatalf("vectors Nearest: %v", err)
	}
	vids := map[string]bool{}
	for _, r := range vresults {
		vids[r.ID] = true
	}
	if !vids["roadmap"] || !vids["onboarding"] {
		t.Fatalf("vectors recovered = %+v, want roadmap and onboarding", vresults)
	}

	si2, err := searchindex.Open(searchIndexPath)
	if err != nil {
		t.Fatalf("searchindex.Open (after deletion): %v", err)
	}
	defer si2.Close()
	if err := si2.Rebuild(context.Background(), topicsDir, meetingsDir); err != nil {
		t.Fatalf("searchindex Rebuild (recovery): %v", err)
	}
	hits, err := si2.Query("roadmap", 10)
	if err != nil {
		t.Fatalf("searchindex Query: %v", err)
	}
	sids := map[string]bool{}
	for _, h := range hits {
		sids[h.ID] = true
	}
	if !sids["topic:roadmap"] || !sids["meeting:2026-01-05-standup"] {
		t.Fatalf("searchindex recovered = %+v, want topic:roadmap and meeting:2026-01-05-standup", hits)
	}
}

func writeMD(t *testing.T, dir, slug, title, body string) {
	t.Helper()
	path := filepath.Join(dir, slug+".md")
	content := "# " + title + "\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeMD(%s): %v", slug, err)
	}
}
