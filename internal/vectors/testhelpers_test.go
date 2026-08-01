package vectors

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fernando143/patro/internal/embed"
)

// writeMarkdownFile writes a minimal topic/meeting markdown fixture and
// returns its path, mirroring the "topics/*.md" shape internal/library
// produces (a "# Title" heading followed by body content).
func writeMarkdownFile(t *testing.T, dir, slug, title, body string) string {
	t.Helper()
	path := filepath.Join(dir, slug+".md")
	content := "# " + title + "\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeMarkdownFile(%s): %v", slug, err)
	}
	return path
}

// controlledEmbedder is a deterministic, dependency-free embed.Embedder test
// double whose Embed call can be held open until the test explicitly
// releases it. It lets concurrency tests deterministically observe "a
// Rebuild is genuinely in flight" without relying on timing/sleeps.
type controlledEmbedder struct {
	dim  int
	name string

	startOnce sync.Once
	started   chan struct{} // closed when the first Embed call begins

	mu      sync.Mutex
	release chan struct{} // Embed blocks here until closed

	calls atomic.Int64
}

func newControlledEmbedder(dim int, name string) *controlledEmbedder {
	return &controlledEmbedder{
		dim:     dim,
		name:    name,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

// Embed blocks until unblock is called (or ctx is done), then returns a
// deterministic unit-norm vector derived from text via embed.NewNop.
func (e *controlledEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.calls.Add(1)
	e.startOnce.Do(func() { close(e.started) })
	e.mu.Lock()
	release := e.release
	e.mu.Unlock()
	select {
	case <-release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return embed.NewNop(e.dim).Embed(ctx, text)
}

func (e *controlledEmbedder) Dim() int { return e.dim }

func (e *controlledEmbedder) Name() string { return e.name }

// unblock lets every Embed call currently waiting (and any future ones)
// proceed immediately.
func (e *controlledEmbedder) unblock() {
	e.mu.Lock()
	defer e.mu.Unlock()
	close(e.release)
}

// callCount returns how many times Embed was invoked so far.
func (e *controlledEmbedder) callCount() int64 { return e.calls.Load() }
