package embed

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeEmbedder is a minimal Embedder used to exercise the registry in
// isolation, without depending on any real backend adapter (those land in
// later units).
type fakeEmbedder struct {
	name string
	dim  int
}

func (f fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, f.dim), nil
}
func (f fakeEmbedder) Dim() int     { return f.dim }
func (f fakeEmbedder) Name() string { return f.name }

// stubRegistry replaces the package-level registry for the duration of a
// test and returns a func that restores the original table.
func stubRegistry(t *testing.T, table map[string]factory) {
	t.Helper()
	original := registry
	registry = table
	t.Cleanup(func() { registry = original })
}

func TestAvailableListsProductionBackends(t *testing.T) {
	// go-sentex was dropped and zerfoo was blocked before registration
	// (see design D9 amendment and the tasks Unit 1c finding); cybertron
	// (Unit 1d) is the only backend that passed verification and is
	// registered. Available() must reflect exactly the compiled-in table,
	// not a hardcoded aspirational list.
	got := Available()
	want := []string{"cybertron"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Available() = %v, want %v", got, want)
	}
}

func TestNewOnUnknownNameInProductionRegistry(t *testing.T) {
	if _, err := New("anything-unregistered"); err == nil {
		t.Fatal("New(\"anything-unregistered\") did not return an error")
	}
}

func TestAvailableIsSortedIndependentCopy(t *testing.T) {
	stubRegistry(t, map[string]factory{
		"zeta":  func() (Embedder, error) { return fakeEmbedder{name: "zeta"}, nil },
		"alpha": func() (Embedder, error) { return fakeEmbedder{name: "alpha"}, nil },
	})

	got := Available()
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Available() = %v, want %v", got, want)
	}

	// Mutating the returned slice must not corrupt the registry.
	got[0] = "corrupted"
	again := Available()
	if again[0] == "corrupted" {
		t.Error("mutating a returned slice corrupted the package's internal registry")
	}
}

func TestNewReturnsRegisteredBackend(t *testing.T) {
	stubRegistry(t, map[string]factory{
		"fake": func() (Embedder, error) { return fakeEmbedder{name: "fake", dim: 3}, nil },
	})

	e, err := New("fake")
	if err != nil {
		t.Fatalf("New(\"fake\") returned error: %v", err)
	}
	if e.Name() != "fake" {
		t.Errorf("Name() = %q, want %q", e.Name(), "fake")
	}
	if e.Dim() != 3 {
		t.Errorf("Dim() = %d, want 3", e.Dim())
	}
}

func TestNewUnknownNameListsAvailable(t *testing.T) {
	stubRegistry(t, map[string]factory{
		"alpha": func() (Embedder, error) { return fakeEmbedder{name: "alpha"}, nil },
	})

	_, err := New("bogus")
	if err == nil {
		t.Fatal("New(\"bogus\") did not return an error")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error %q does not mention the unknown name %q", err, "bogus")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("error %q does not list the available backend %q", err, "alpha")
	}
}

func TestNewPropagatesFactoryError(t *testing.T) {
	wantErr := errors.New("boom")
	stubRegistry(t, map[string]factory{
		"broken": func() (Embedder, error) { return nil, wantErr },
	})

	_, err := New("broken")
	if !errors.Is(err, wantErr) {
		t.Fatalf("New(\"broken\") error = %v, want %v", err, wantErr)
	}
}

func TestNopEmbedderIsDeterministicAndUnitNorm(t *testing.T) {
	e := NewNop(8)
	if e.Dim() != 8 {
		t.Fatalf("Dim() = %d, want 8", e.Dim())
	}
	if e.Name() == "" {
		t.Fatal("Name() is empty")
	}

	ctx := context.Background()
	v1, err := e.Embed(ctx, "hello world")
	if err != nil {
		t.Fatalf("Embed() error: %v", err)
	}
	if len(v1) != 8 {
		t.Fatalf("Embed() returned %d dims, want 8", len(v1))
	}

	v2, err := e.Embed(ctx, "hello world")
	if err != nil {
		t.Fatalf("Embed() error: %v", err)
	}
	if !reflect.DeepEqual(v1, v2) {
		t.Error("Embed() is not deterministic for the same input")
	}

	v3, err := e.Embed(ctx, "a different string")
	if err != nil {
		t.Fatalf("Embed() error: %v", err)
	}
	if reflect.DeepEqual(v1, v3) {
		t.Error("Embed() returned the same vector for different inputs")
	}

	var sumSquares float64
	for _, f := range v1 {
		sumSquares += float64(f) * float64(f)
	}
	if diff := sumSquares - 1.0; diff < -1e-4 || diff > 1e-4 {
		t.Errorf("||Embed()|| = %v, want a unit vector (sum of squares ~1.0)", sumSquares)
	}
}

func TestNopEmbedderIsNotInProductionRegistry(t *testing.T) {
	// nopEmbedder is a test/--mock double, not a config-selectable
	// embedding_backend value (design D9: only real, verified adapters are
	// registered — currently just cybertron).
	for _, name := range Available() {
		if name == "nop" {
			t.Error("nop embedder must not be registered as a selectable backend")
		}
	}
}
