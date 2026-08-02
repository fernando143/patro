package vectors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/embed"
)

func newTestStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(dir, "topics.json"), embed.NewNop(4), "v1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestUpsertAndNearestRanksByCosine(t *testing.T) {
	s := newTestStore(t, t.TempDir())

	if err := s.Upsert("a", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Upsert(a): %v", err)
	}
	if err := s.Upsert("b", []float32{0, 1, 0, 0}); err != nil {
		t.Fatalf("Upsert(b): %v", err)
	}
	if err := s.Upsert("c", []float32{0.9, 0.1, 0, 0}); err != nil {
		t.Fatalf("Upsert(c): %v", err)
	}

	results, err := s.Nearest([]float32{1, 0, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("results[0].ID = %q, want a (exact match)", results[0].ID)
	}
	if results[1].ID != "c" {
		t.Errorf("results[1].ID = %q, want c (closest remaining)", results[1].ID)
	}
	if results[0].Score < results[1].Score {
		t.Errorf("results not ranked descending: %+v", results)
	}
}

func TestUpsertRejectsWrongDimension(t *testing.T) {
	s := newTestStore(t, t.TempDir())
	if err := s.Upsert("a", []float32{1, 0}); err == nil {
		t.Error("Upsert() with wrong dim error = nil, want error")
	}
}

func TestNearestOnEmptyStoreReturnsEmpty(t *testing.T) {
	s := newTestStore(t, t.TempDir())
	results, err := s.Nearest([]float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want empty", results)
	}
}

func TestUpsertPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topics.json")

	s1, err := NewStore(path, embed.NewNop(4), "v1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s1.Upsert("a", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	s2, err := NewStore(path, embed.NewNop(4), "v1")
	if err != nil {
		t.Fatalf("NewStore (reopen): %v", err)
	}
	if s2.NeedsRebuild() {
		t.Error("reopened store with matching tag NeedsRebuild() = true, want false")
	}
	results, err := s2.Nearest([]float32{1, 0, 0, 0}, 1)
	if err != nil {
		t.Fatalf("Nearest after reopen: %v", err)
	}
	if len(results) != 1 || results[0].ID != "a" {
		t.Fatalf("results after reopen = %+v, want one hit for a", results)
	}
}

func TestNewStoreFreshDirectoryNeedsRebuild(t *testing.T) {
	s := newTestStore(t, t.TempDir())
	if !s.NeedsRebuild() {
		t.Error("fresh store NeedsRebuild() = false, want true (nothing persisted yet)")
	}
}

func TestNewStoreCorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topics.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path, embed.NewNop(4), "v1"); err == nil {
		t.Error("NewStore() error = nil, want error for corrupt JSON")
	}
}
