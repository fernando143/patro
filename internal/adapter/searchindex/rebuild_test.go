package searchindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeMarkdown(t *testing.T, dir, slug, title, body string) {
	t.Helper()
	path := filepath.Join(dir, slug+".md")
	content := "# " + title + "\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeMarkdown(%s): %v", slug, err)
	}
}

// Task 3.5 — Rebuild walks topics/*.md and meetings/*.md and repopulates
// the index from scratch.
func TestRebuildIndexesTopicsAndMeetings(t *testing.T) {
	topicsDir := t.TempDir()
	meetingsDir := t.TempDir()
	writeMarkdown(t, topicsDir, "roadmap", "Roadmap", "Q3 planning and deployment goals.")
	writeMarkdown(t, meetingsDir, "2026-01-05-standup", "Standup", "Discussed the roadmap and blockers.")

	idx := newTestIndex(t)
	if err := idx.Rebuild(context.Background(), topicsDir, meetingsDir); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	hits, err := idx.Query("roadmap", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want 2 (one topic, one meeting)", hits)
	}
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.ID] = true
	}
	if !ids["topic:roadmap"] || !ids["meeting:2026-01-05-standup"] {
		t.Fatalf("hits = %+v, want topic:roadmap and meeting:2026-01-05-standup", hits)
	}
}

// Task 3.6 (search side) — Rebuild fully reconstructs the index after its
// on-disk directory is deleted, recovering entirely from markdown.
func TestRebuildRecoversAfterIndexDirDeleted(t *testing.T) {
	topicsDir := t.TempDir()
	meetingsDir := t.TempDir()
	writeMarkdown(t, topicsDir, "onboarding", "Onboarding", "New hire checklist.")

	indexPath := filepath.Join(t.TempDir(), "search-index")
	idx, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx.Rebuild(context.Background(), topicsDir, meetingsDir); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate ".state/search-index" being deleted (derived state, safe to
	// remove per design).
	if err := os.RemoveAll(indexPath); err != nil {
		t.Fatal(err)
	}

	idx2, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open (after deletion): %v", err)
	}
	defer idx2.Close()
	if err := idx2.Rebuild(context.Background(), topicsDir, meetingsDir); err != nil {
		t.Fatalf("Rebuild (recovery): %v", err)
	}

	hits, err := idx2.Query("onboarding", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "topic:onboarding" {
		t.Fatalf("hits = %+v, want topic:onboarding recovered from markdown", hits)
	}
}

func TestRebuildReplacesStaleDocuments(t *testing.T) {
	topicsDir := t.TempDir()
	meetingsDir := t.TempDir()
	writeMarkdown(t, topicsDir, "old", "Old topic", "content")

	idx := newTestIndex(t)
	if err := idx.Rebuild(context.Background(), topicsDir, meetingsDir); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Remove the old topic file and rebuild again: the stale document must
	// not survive the rebuild (the index is derived, not additive).
	if err := os.Remove(filepath.Join(topicsDir, "old.md")); err != nil {
		t.Fatal(err)
	}
	writeMarkdown(t, topicsDir, "new", "New topic", "content")
	if err := idx.Rebuild(context.Background(), topicsDir, meetingsDir); err != nil {
		t.Fatalf("Rebuild (second pass): %v", err)
	}

	hits, err := idx.Query("topic", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, h := range hits {
		if h.ID == "topic:old" {
			t.Fatalf("hits = %+v, want stale topic:old removed", hits)
		}
	}
}
