package vectors

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Task 3.7 — pipeline ingestion must not be blocked by an in-flight
// Rebuild. Only reconciliation degrades (Nearest fails fast with
// ErrRebuilding); nothing waits on the store's internal lock. This proves
// the mechanism the design relies on: Nearest never contends for the write
// lock Rebuild holds only at the very end, so callers checking the store
// while a (potentially slow, real-embedder) rebuild runs never stall.
func TestNearestNonBlockingDuringLongRebuild(t *testing.T) {
	dir := t.TempDir()
	source := t.TempDir()
	for i := 0; i < 5; i++ {
		writeMarkdownFile(t, source, "topic-"+string(rune('a'+i)), "Topic", "content")
	}

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
	case <-time.After(2 * time.Second):
		t.Fatal("Rebuild never started")
	}

	// While the rebuild is genuinely stuck mid-embed (deliberately held
	// open by the controlled embedder, simulating a slow real backend),
	// every ingestion-side probe must return promptly instead of blocking
	// on the store's lock.
	const probes = 50
	const perProbeBudget = 50 * time.Millisecond
	for i := 0; i < probes; i++ {
		start := time.Now()
		_, err := s.Nearest([]float32{1, 0, 0, 0}, 5)
		elapsed := time.Since(start)
		if elapsed > perProbeBudget {
			t.Fatalf("Nearest() call #%d took %v during rebuild, want < %v (must fail fast, not block)", i, elapsed, perProbeBudget)
		}
		if err != ErrRebuilding {
			t.Fatalf("Nearest() call #%d error = %v, want ErrRebuilding", i, err)
		}
	}

	emb.unblock()

	select {
	case err := <-rebuildErr:
		if err != nil {
			t.Fatalf("Rebuild(): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Rebuild never finished after unblock")
	}
}
