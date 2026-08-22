package migration

import (
	"context"
	"fmt"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/vectors"

	"github.com/fernando143/patro/internal/layout"
)

// ConfiguredService builds a migration service from the active application config.
func ConfiguredService(cfg *config.Config) (*Service, error) {
	_, embedder, err := vectors.OpenRepresentationStore(context.Background(), cfg.StateDir(), cfg.EmbeddingBackend)
	if err != nil {
		return nil, fmt.Errorf("migration: %w", err)
	}
	s := &Service{
		LibraryRoot: cfg.Library,
		StateDir:    cfg.StateDir(),
		Threshold:   cfg.MergeThreshold,
		Representer: embedder,
	}
	s.RebuildDerived = func(ctx context.Context) error {
		lib := layout.Library(cfg.Library)
		topicsDir := lib.Topics()
		// The store and the embedder that syncs it must be the same pair:
		// the store stamps its snapshot with that embedder's identity.
		v2, syncEmbedder, err := vectors.OpenRepresentationStore(ctx, cfg.StateDir(), cfg.EmbeddingBackend)
		if err != nil {
			return fmt.Errorf("migration: %w", err)
		}
		if err := v2.Sync(ctx, topicsDir, syncEmbedder); err != nil {
			return err
		}
		idx, err := searchindex.Open(cfg.SearchIndexDir())
		if err != nil {
			return err
		}
		defer idx.Close()
		return idx.Rebuild(ctx, lib.Topics(), lib.Meetings())
	}
	return s, nil
}
