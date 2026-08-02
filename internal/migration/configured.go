package migration

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/vectors"
)

// ConfiguredService builds a migration service from the active application config.
func ConfiguredService(cfg *config.Config) (*Service, error) {
	embedder, err := embed.New(cfg.EmbeddingBackend)
	if err != nil {
		return nil, fmt.Errorf("migration: embedding backend unavailable: %w", err)
	}
	s := &Service{
		LibraryRoot: cfg.Library,
		StateDir:    cfg.StateDir(),
		Threshold:   cfg.MergeThreshold,
		Embedder:    embedder,
	}
	s.RebuildDerived = func(ctx context.Context) error {
		store, err := vectors.NewStore(filepath.Join(cfg.StateDir(), "vectors", "topics.json"), embedder, embedder.Name())
		if err != nil {
			return err
		}
		if err := store.Rebuild(ctx, filepath.Join(cfg.Library, "topics"), nil); err != nil {
			return err
		}
		idx, err := searchindex.Open(cfg.SearchIndexDir())
		if err != nil {
			return err
		}
		defer idx.Close()
		return idx.Rebuild(ctx, filepath.Join(cfg.Library, "topics"), filepath.Join(cfg.Library, "meetings"))
	}
	return s, nil
}
