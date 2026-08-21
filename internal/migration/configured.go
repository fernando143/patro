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
	representer, ok := embedder.(DocumentRepresenter)
	if !ok {
		return nil, fmt.Errorf("migration: embedding backend %q does not support document representations", embedder.Name())
	}
	s := &Service{
		LibraryRoot: cfg.Library,
		StateDir:    cfg.StateDir(),
		Threshold:   cfg.MergeThreshold,
		Representer: representer,
	}
	s.RebuildDerived = func(ctx context.Context) error {
		storePath := filepath.Join(cfg.StateDir(), "vectors", "topics.json")
		topicsDir := filepath.Join(cfg.Library, "topics")
		sample, err := representer.Represent(ctx, embed.Document{ID: "identity", Text: "# Identity\n\nidentity"})
		if err != nil {
			return fmt.Errorf("migration: initializing representation identity: %w", err)
		}
		if sample == nil {
			return fmt.Errorf("migration: representation backend returned nil identity")
		}
		v2 := vectors.NewV2Store(storePath, sample.Identity(), vectors.OSCommitFS{})
		if err := v2.Sync(ctx, topicsDir, representer); err != nil {
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
