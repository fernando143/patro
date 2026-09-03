package searchindex_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/adapter/searchindex"
)

// Rebuild() must reconstruct the BM25 index from markdown alone after
// deleting its derived state directory; the knowledge library remains the
// source of truth.
func TestSearchIndexRecoversFromMarkdownAfterStateDeletion(t *testing.T) {
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
	searchIndexPath := filepath.Join(stateDir, "search-index")

	// First-time build: the store starts empty and is rebuilt from markdown.
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

	// Simulate an operator (or corruption recovery) deleting the derived
	// state directory entirely.
	if err := os.RemoveAll(searchIndexPath); err != nil {
		t.Fatal(err)
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
