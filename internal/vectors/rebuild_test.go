package vectors

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/embed"
)

// Task 3.3 — Rebuild walks source/*.md, embeds each file's content and
// repopulates the store, reporting progress along the way.
func TestRebuildPopulatesFromMarkdownSource(t *testing.T) {
	dir := t.TempDir()
	source := t.TempDir()
	writeMarkdownFile(t, source, "topic-a", "Topic A", "Alpha content.")
	writeMarkdownFile(t, source, "topic-b", "Topic B", "Beta content.")

	s, err := NewStore(filepath.Join(dir, "topics.json"), embed.NewNop(4), "v1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	var progress [][2]int
	err = s.Rebuild(context.Background(), source, func(done, total int) {
		progress = append(progress, [2]int{done, total})
	})
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if s.NeedsRebuild() {
		t.Error("NeedsRebuild() after successful Rebuild = true, want false")
	}

	// k (10) exceeds the number of entries (2), so every entry is returned
	// regardless of the probe vector's exact direction.
	results, err := s.Nearest([]float32{1, 0, 0, 0}, 10)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range results {
		ids[r.ID] = true
	}
	if !ids["topic-a"] || !ids["topic-b"] {
		t.Fatalf("Nearest after Rebuild = %+v, want both topic-a and topic-b", results)
	}

	if len(progress) == 0 {
		t.Error("onProgress was never called")
	} else if last := progress[len(progress)-1]; last[0] != last[1] || last[1] != 2 {
		t.Errorf("final progress = %v, want done==total==2", last)
	}
}

// Task 3.4 — a backend/dim/model_version mismatch on load invalidates the
// store (D10 self-invalidation); calling Rebuild afterward repopulates it
// under the new tag.
func TestBackendMismatchInvalidatesAndRebuildFixesIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topics.json")
	source := t.TempDir()
	writeMarkdownFile(t, source, "topic-a", "Topic A", "content")

	// Seed a store file tagged for a different backend/dim than the one the
	// test will construct with.
	stale := fileFormat{
		Backend:      "old-backend",
		Dim:          4,
		ModelVersion: "v0",
		Entries: []entry{
			{ID: "stale-topic", Vector: []float32{1, 0, 0, 0}},
		},
	}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(path, embed.NewNop(4), "v1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if !s.NeedsRebuild() {
		t.Fatal("NeedsRebuild() after tag mismatch = false, want true")
	}

	// The stale, wrong-vector-space entry must never surface as a real
	// result: the store is invalidated, not silently reused.
	results, err := s.Nearest([]float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Nearest on invalidated store: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Nearest on invalidated store = %+v, want empty (stale entries discarded)", results)
	}

	if err := s.Rebuild(context.Background(), source, nil); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if s.NeedsRebuild() {
		t.Error("NeedsRebuild() after Rebuild = true, want false")
	}

	results, err = s.Nearest([]float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Nearest after Rebuild: %v", err)
	}
	if len(results) != 1 || results[0].ID != "topic-a" {
		t.Fatalf("Nearest after Rebuild = %+v, want topic-a only", results)
	}

	// The on-disk tag must now reflect the current backend/dim/model_version.
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ff fileFormat
	if err := json.Unmarshal(raw, &ff); err != nil {
		t.Fatal(err)
	}
	if ff.Backend != "nop" || ff.Dim != 4 || ff.ModelVersion != "v1" {
		t.Errorf("persisted tag = %+v, want {nop 4 v1}", ff)
	}
}

func TestRebuildSkipsUnreadableFilesWithoutAborting(t *testing.T) {
	dir := t.TempDir()
	source := t.TempDir()
	writeMarkdownFile(t, source, "topic-a", "Topic A", "content")
	// A directory named *.md is not a readable topic file; Rebuild must skip
	// it rather than fail the whole pass.
	if err := os.MkdirAll(filepath.Join(source, "not-a-file.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(filepath.Join(dir, "topics.json"), embed.NewNop(4), "v1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Rebuild(context.Background(), source, nil); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	results, err := s.Nearest([]float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(results) != 1 || results[0].ID != "topic-a" {
		t.Fatalf("results = %+v, want only topic-a", results)
	}
}
