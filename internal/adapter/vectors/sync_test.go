package vectors

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/adapter/embed"
)

type fixtureRepresenter struct{}

func (fixtureRepresenter) Represent(_ context.Context, document embed.Document) (*embed.Representation, error) {
	identity := v2Identity()
	return &embed.Representation{
		SchemaVersion: 2, DocumentID: document.ID, SourceHash: hashSource(document.Text), Backend: identity.Backend,
		ModelID: identity.ModelID, ModelVersion: identity.ModelVersion, ModelWeightsSHA256: identity.ModelWeightsSHA256,
		TokenizerSHA256: identity.TokenizerSHA256, ChunkerVersion: identity.ChunkerVersion,
		NormalizationVersion: identity.NormalizationVersion, RepresentationFingerprint: embed.Fingerprint(identity),
		Dimension: 2, Chunks: []embed.Chunk{{Kind: "content", Ordinal: 0, TokenCount: 1, SourceStartRune: 0, SourceEndRune: 1, Vector: []float32{1, 0}}},
	}, nil
}

func TestV2SyncReusesSourcesAndDeletesRemovedDocuments(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "topics")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "alpha.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewV2Store(filepath.Join(dir, "topics.json"), v2Identity(), OSCommitFS{})
	if err := store.Sync(context.Background(), source, fixtureRepresenter{}); err != nil {
		t.Fatalf("first Sync() error: %v", err)
	}
	if store.State() != StateCurrent || store.NeedsSync() {
		t.Fatalf("state after sync = %s needs=%v", store.State(), store.NeedsSync())
	}
	if err := os.WriteFile(filepath.Join(source, "beta.md"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "alpha.md")); err != nil {
		t.Fatal(err)
	}
	if err := store.Sync(context.Background(), source, fixtureRepresenter{}); err != nil {
		t.Fatalf("second Sync() error: %v", err)
	}
	entries, needs, err := LoadV2(filepath.Join(dir, "topics.json"), v2Identity())
	if err != nil || needs || len(entries) != 1 || entries[0].DocumentID != "beta" {
		t.Fatalf("persisted entries = %#v needs=%v err=%v, want beta only", entries, needs, err)
	}
}

func TestV2SyncCancellationBeforeCommitPreservesOldSnapshot(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "topics")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "alpha.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewV2Store(filepath.Join(dir, "topics.json"), v2Identity(), OSCommitFS{})
	if err := store.Sync(context.Background(), source, fixtureRepresenter{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "beta.md"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store.SetBeforeCommitHook(cancel)
	err := store.Sync(ctx, source, fixtureRepresenter{})
	if !errors.Is(err, context.Canceled) || store.State() != StateDirty {
		t.Fatalf("cancelled Sync() error=%v state=%s, want cancellation and Dirty", err, store.State())
	}
	entries, needs, err := LoadV2(filepath.Join(dir, "topics.json"), v2Identity())
	if err != nil || needs || len(entries) != 1 || entries[0].DocumentID != "alpha" {
		t.Fatalf("old snapshot after cancellation = %#v needs=%v err=%v", entries, needs, err)
	}
}

func TestV2SyncMasksCancellationAfterCommitIntent(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "topics")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "alpha.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store := NewV2Store(filepath.Join(dir, "topics.json"), v2Identity(), OSCommitFS{})
	store.SetBeforeRenameHook(cancel)
	if err := store.Sync(ctx, source, fixtureRepresenter{}); err != nil {
		t.Fatalf("late-cancel Sync() error = %v, want commit success", err)
	}
	if store.State() != StateCurrent {
		t.Fatalf("state after late cancellation = %s, want Current", store.State())
	}
}

func hashSource(text string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
}
