package searchindex

import (
	"path/filepath"
	"testing"
)

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(filepath.Join(t.TempDir(), "search-index"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func TestIndexAndQueryRanksByRelevance(t *testing.T) {
	idx := newTestIndex(t)

	docs := []Document{
		{ID: "topic:alpha", Kind: KindTopic, Title: "Alpha", Content: "Discussion about the deployment pipeline and CI."},
		{ID: "topic:beta", Kind: KindTopic, Title: "Beta", Content: "Notes about lunch preferences."},
		{ID: "meeting:m1", Kind: KindMeeting, Title: "Weekly sync", Content: "Reviewed the deployment pipeline status."},
	}
	for _, d := range docs {
		if err := idx.Index(d); err != nil {
			t.Fatalf("Index(%s): %v", d.ID, err)
		}
	}

	hits, err := idx.Query("deployment pipeline", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want 2 matches", hits)
	}
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.ID] = true
		if h.Score <= 0 {
			t.Errorf("hit %s has non-positive score %v", h.ID, h.Score)
		}
	}
	if !ids["topic:alpha"] || !ids["meeting:m1"] {
		t.Fatalf("hits = %+v, want topic:alpha and meeting:m1", hits)
	}
}

func TestQueryNoMatchesReturnsEmpty(t *testing.T) {
	idx := newTestIndex(t)
	if err := idx.Index(Document{ID: "topic:a", Kind: KindTopic, Title: "A", Content: "hello world"}); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Query("nonexistentterm", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %+v, want empty", hits)
	}
}

func TestOpenReopensExistingIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "search-index")
	idx1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx1.Index(Document{ID: "topic:a", Kind: KindTopic, Title: "A", Content: "persisted content"}); err != nil {
		t.Fatal(err)
	}
	if err := idx1.Close(); err != nil {
		t.Fatal(err)
	}

	idx2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (reopen): %v", err)
	}
	defer idx2.Close()

	hits, err := idx2.Query("persisted", 10)
	if err != nil {
		t.Fatalf("Query after reopen: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "topic:a" {
		t.Fatalf("hits after reopen = %+v, want topic:a", hits)
	}
}
