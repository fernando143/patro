package vectors

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/platform/layout"
)

// TestOpenRepresentationStoreUnknownBackendErrors pins the contract that
// failure is an error, never a nil store with a nil error. The four call
// sites this constructor replaced each hand-rolled their own nil handling,
// and two of them treated a nil result as "disabled" rather than "broken".
func TestOpenRepresentationStoreUnknownBackendErrors(t *testing.T) {
	store, embedder, err := OpenRepresentationStore(context.Background(), t.TempDir(), "no-such-backend")
	if err == nil {
		t.Fatal("OpenRepresentationStore error = nil, want an error for an unknown backend")
	}
	if store != nil {
		t.Errorf("store = %v, want nil on failure", store)
	}
	if embedder != nil {
		t.Errorf("embedder = %v, want nil on failure", embedder)
	}
	if !strings.Contains(err.Error(), "embedding backend unavailable") {
		t.Errorf("error = %q, want it to name the unavailable backend", err)
	}
}

// TestOpenRepresentationStoreUsesLayoutPath pins the store location so the
// constructor cannot silently relocate an existing snapshot: every caller
// previously built this path by hand as
// filepath.Join(stateDir, "vectors", "topics.json").
func TestOpenRepresentationStoreUsesLayoutPath(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "vectors", "topics.json")
	if got := layout.State(dir).VectorStore(); got != want {
		t.Fatalf("layout.State(%q).VectorStore() = %q, want %q", dir, got, want)
	}
}
