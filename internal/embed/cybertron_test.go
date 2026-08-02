package embed

import (
	"context"
	"sync"
	"testing"
)

// testCybertronEmbedder loads the real cybertron backend once and shares it
// across sub-tests: loading decodes a ~90MB gob-serialized BERT checkpoint,
// so paying that cost once keeps the suite fast while still exercising the
// real production factory (embed.New("cybertron"), not a shortcut).
var (
	testCybertronOnce     sync.Once
	testCybertronEmbedder Embedder
	testCybertronErr      error
)

func newTestCybertronEmbedder(t *testing.T) Embedder {
	t.Helper()
	testCybertronOnce.Do(func() {
		testCybertronEmbedder, testCybertronErr = New("cybertron")
	})
	if testCybertronErr != nil {
		t.Fatalf("New(\"cybertron\") returned error: %v", testCybertronErr)
	}
	return testCybertronEmbedder
}

func TestCybertronIsRegisteredInProductionRegistry(t *testing.T) {
	found := false
	for _, name := range Available() {
		if name == "cybertron" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Available() = %v, want it to contain %q", Available(), "cybertron")
	}
}

func TestCybertronName(t *testing.T) {
	e := newTestCybertronEmbedder(t)
	if got := e.Name(); got != "cybertron" {
		t.Errorf("Name() = %q, want %q", got, "cybertron")
	}
}

func TestCybertronDim(t *testing.T) {
	e := newTestCybertronEmbedder(t)
	// all-MiniLM-L6-v2 (the embedded checkpoint) has hidden_size = 384.
	if got := e.Dim(); got != 384 {
		t.Errorf("Dim() = %d, want 384", got)
	}
}

func TestCybertronEmbedIsUnitNorm(t *testing.T) {
	e := newTestCybertronEmbedder(t)

	vec, err := e.Embed(context.Background(), "The cat sat on the mat.")
	if err != nil {
		t.Fatalf("Embed() error: %v", err)
	}
	if len(vec) != e.Dim() {
		t.Fatalf("Embed() returned %d dims, want %d", len(vec), e.Dim())
	}

	var sumSquares float64
	for _, f := range vec {
		sumSquares += float64(f) * float64(f)
	}
	if diff := sumSquares - 1.0; diff < -1e-3 || diff > 1e-3 {
		t.Errorf("||Embed()|| = %v, want a unit vector (sum of squares ~1.0)", sumSquares)
	}
}

// TestCybertronProducesGenuineContextualEmbeddings is the behavioral test
// that distinguishes a real sentence encoder from a non-contextual
// bag-of-token-embeddings average (the defect that disqualified the zerfoo
// candidate, Unit 1c): a paraphrase must score a materially higher cosine
// similarity than an unrelated sentence. A degenerate "average the static
// token embedding table" implementation would only weakly separate these,
// since it ignores word order, attention, and composition entirely.
func TestCybertronProducesGenuineContextualEmbeddings(t *testing.T) {
	e := newTestCybertronEmbedder(t)
	ctx := context.Background()

	base, err := e.Embed(ctx, "The cat sat on the mat.")
	if err != nil {
		t.Fatalf("Embed(base) error: %v", err)
	}
	paraphrase, err := e.Embed(ctx, "A feline rested on the rug.")
	if err != nil {
		t.Fatalf("Embed(paraphrase) error: %v", err)
	}
	unrelated, err := e.Embed(ctx, "The stock market crashed yesterday.")
	if err != nil {
		t.Fatalf("Embed(unrelated) error: %v", err)
	}

	cosParaphrase := dot(base, paraphrase) // unit-norm vectors: dot == cosine
	cosUnrelated := dot(base, unrelated)

	if cosParaphrase <= cosUnrelated {
		t.Fatalf("cos(base,paraphrase) = %v, cos(base,unrelated) = %v; want paraphrase similarity strictly greater",
			cosParaphrase, cosUnrelated)
	}
	// A near-degenerate encoder (e.g. static-table averaging) still passes
	// the ordering check loosely; require a real margin to catch it.
	if cosParaphrase-cosUnrelated < 0.2 {
		t.Fatalf("similarity margin too small: cos(base,paraphrase)=%v cos(base,unrelated)=%v, want a margin >= 0.2",
			cosParaphrase, cosUnrelated)
	}
}

func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func TestCybertronEmbedIsDeterministic(t *testing.T) {
	e := newTestCybertronEmbedder(t)
	ctx := context.Background()

	v1, err := e.Embed(ctx, "deterministic check")
	if err != nil {
		t.Fatalf("Embed() error: %v", err)
	}
	v2, err := e.Embed(ctx, "deterministic check")
	if err != nil {
		t.Fatalf("Embed() error: %v", err)
	}
	if len(v1) != len(v2) {
		t.Fatalf("Embed() lengths differ: %d vs %d", len(v1), len(v2))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("Embed() is not deterministic: v1[%d]=%v v2[%d]=%v", i, v1[i], i, v2[i])
		}
	}
}
