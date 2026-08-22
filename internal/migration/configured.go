package migration

import (
	"context"
	"fmt"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/vectors"

	"github.com/fernando143/patro/internal/layout"
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
		Representer: embedder,
	}
	s.RebuildDerived = func(ctx context.Context) error {
		storePath := layout.State(cfg.StateDir()).VectorStore()
		lib := layout.Library(cfg.Library)
		topicsDir := lib.Topics()
		sample, err := embedder.Represent(ctx, embed.Document{ID: "identity", Text: "# Identity\n\nidentity"})
		if err != nil {
			return fmt.Errorf("migration: initializing representation identity: %w", err)
		}
		if sample == nil {
			return fmt.Errorf("migration: representation backend returned nil identity")
		}
		v2 := vectors.NewV2Store(storePath, sample.Identity(), vectors.OSCommitFS{})
		if err := v2.Sync(ctx, topicsDir, embedder); err != nil {
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
