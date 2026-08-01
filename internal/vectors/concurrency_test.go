package vectors

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Task 3.1 — concurrent Nearest() calls during an in-progress Rebuild() must
// return ErrRebuilding, never a partial/inconsistent result. This is the
// design's highest-risk concurrency case (D10), so it is written before the
// gating implementation exists.
func TestNearestDuringRebuildReturnsErrRebuilding(t *testing.T) {
	dir := t.TempDir()
	source := t.TempDir()
	writeMarkdownFile(t, source, "topic-a", "Topic A", "Some content about topic A.")

	emb := newControlledEmbedder(4, "controlled")
	s, err := NewStore(filepath.Join(dir, "topics.json"), emb, "v1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	rebuildErr := make(chan error, 1)
	go func() {
		rebuildErr <- s.Rebuild(context.Background(), source, nil)
	}()

	select {
	case <-emb.started:
		// Rebuild has genuinely begun embedding; a rebuild is in flight.
	case <-time.After(2 * time.Second):
		t.Fatal("Rebuild never started embedding")
	}

	// Every Nearest call while the rebuild is in flight must fail fast with
	// ErrRebuilding, never a partial or stale-but-plausible result.
	for i := 0; i < 10; i++ {
		results, err := s.Nearest([]float32{0, 0, 0, 1}, 5)
		if !errors.Is(err, ErrRebuilding) {
			t.Fatalf("Nearest() during rebuild error = %v, want ErrRebuilding", err)
		}
		if results != nil {
			t.Fatalf("Nearest() during rebuild results = %v, want nil (no partial result)", results)
		}
	}

	emb.unblock()

	select {
	case err := <-rebuildErr:
		if err != nil {
			t.Fatalf("Rebuild() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Rebuild never finished after unblock")
	}

	results, err := s.Nearest([]float32{0, 0, 0, 1}, 5)
	if err != nil {
		t.Fatalf("Nearest() after rebuild error = %v", err)
	}
	if len(results) != 1 || results[0].ID != "topic-a" {
		t.Fatalf("Nearest() after rebuild = %+v, want one hit for topic-a", results)
	}
}

// Task 3.2 — triggering a second Rebuild() while one is already in flight
// must be a no-op (single-flight gate). Written before the gating code.
func TestSecondRebuildWhileInFlightIsNoop(t *testing.T) {
	dir := t.TempDir()
	source := t.TempDir()
	writeMarkdownFile(t, source, "topic-a", "Topic A", "content")

	emb := newControlledEmbedder(4, "controlled")
	s, err := NewStore(filepath.Join(dir, "topics.json"), emb, "v1")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	firstErr := make(chan error, 1)
	go func() {
		firstErr <- s.Rebuild(context.Background(), source, nil)
	}()

	select {
	case <-emb.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first Rebuild never started")
	}

	// Second trigger while the first is in flight must return immediately
	// (no-op), without launching a second, concurrent embed pass.
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- s.Rebuild(context.Background(), source, nil)
	}()

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Rebuild() (no-op) error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Rebuild() blocked instead of being a no-op")
	}

	// Only the first Rebuild's embed pass should have run so far.
	if got := emb.callCount(); got != 1 {
		t.Fatalf("embed call count while first rebuild in flight = %d, want 1 (single-flight)", got)
	}

	emb.unblock()

	select {
	case err := <-firstErr:
		if err != nil {
			t.Fatalf("first Rebuild() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first Rebuild never finished after unblock")
	}
}
