// Package maintenance keeps patro's derived artifacts consistent with the
// knowledge library.
//
// Two things can fall behind the Markdown that is the source of truth: the
// multi-vector representation store, which self-invalidates when the
// embedding model changes, and the BM25 index, which simply may not exist
// yet on a first run. Reconciliation of previously flagged topics then runs
// against the now-current store.
//
// Both "patro reconcile" and serve's startup integrity check drive through
// Run, which is a use case rather than CLI parsing and so does not belong
// in package main.
package maintenance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fernando143/patro/internal/adapter/ledger"
	"github.com/fernando143/patro/internal/adapter/searchindex"
	"github.com/fernando143/patro/internal/adapter/status"
	"github.com/fernando143/patro/internal/adapter/vectors"
	"github.com/fernando143/patro/internal/app/pipeline"
	"github.com/fernando143/patro/internal/domain/knowledge"
	"github.com/fernando143/patro/internal/platform/config"
	"github.com/fernando143/patro/internal/platform/layout"
	"github.com/fernando143/patro/internal/platform/logging"
)

// newReconciler is pipeline's production wiring, aliased so this package
// reads as one use case rather than a tour of the packages it composes.
var newReconciler = pipeline.NewReconciler

// Run performs ensure-index (rebuild the multi-vector
// representation store and/or the BM25 search index when missing or
// model-version-mismatched — design D10) and then re-attempts reconciliation
// for every flagged topic against the now-current store. Both "patro
// reconcile" (on demand) and serve's own startup integrity check drive
// through this one function, reporting progress through
// tracker.Maintenance* — tracker may be nil (e.g. status.json could not be
// opened), which every Tracker method already tolerates.
//
// The representation store's self-invalidating tag (design D10) is exactly
// NeedsSync(); bleve's BM25 index has no equivalent backend/model concept
// to mismatch, so its ensure-index trigger is simply "the index directory
// did not exist yet" (first run / pre-Unit-7 library) — Rebuild is
// otherwise left alone rather than paying a full re-embed/reindex cost on
// every single serve startup or reconcile call.
func Run(ctx context.Context, cfg *config.Config, tracker *status.Tracker) error {
	libPaths := layout.Library(cfg.Library)
	topicsDir := libPaths.Topics()
	store, embedder, err := vectors.OpenRepresentationStore(ctx, cfg.StateDir(), cfg.EmbeddingBackend)
	if err != nil {
		return fmt.Errorf("maintenance: %w", err)
	}
	if store.NeedsSync() {
		files, _ := filepath.Glob(filepath.Join(topicsDir, "*.md"))
		tracker.MaintenanceStart(status.PhaseRebuildingIndex, len(files))
		rebuildErr := store.Sync(ctx, topicsDir, embedder)
		tracker.MaintenanceDone()
		if rebuildErr != nil {
			return fmt.Errorf("maintenance: rebuilding vector representations: %w", rebuildErr)
		}
	}

	searchIndexPath := cfg.SearchIndexDir()
	_, statErr := os.Stat(searchIndexPath)
	searchIndexExisted := statErr == nil

	searchIdx, err := searchindex.Open(searchIndexPath)
	if err != nil {
		return fmt.Errorf("maintenance: opening search index: %w", err)
	}
	defer func() {
		if err := searchIdx.Close(); err != nil {
			logging.Warnf("maintenance: closing search index: %v", err)
		}
	}()

	if !searchIndexExisted {
		if err := searchIdx.Rebuild(ctx, topicsDir, libPaths.Meetings()); err != nil {
			return fmt.Errorf("maintenance: rebuilding search index: %w", err)
		}
	}

	lib, err := knowledge.NewLibrary(cfg.Library)
	if err != nil {
		return fmt.Errorf("maintenance: opening library: %w", err)
	}
	lib.Reconciler = newReconciler(cfg)
	if lib.Reconciler == nil {
		return nil // reconciliation disabled (e.g. unknown embedding backend): nothing more to do
	}

	ledgerPath := layout.State(cfg.StateDir()).Ledger()
	entries, err := ledger.Read(ledgerPath)
	if err != nil {
		return fmt.Errorf("maintenance: reading reconciliation ledger: %w", err)
	}
	if flaggedTotal := ledger.CountFlagged(entries); flaggedTotal > 0 {
		tracker.MaintenanceStart(status.PhaseReconciling, flaggedTotal)
		_, reconcileErr := lib.ReconcileFlagged(ctx, ledgerPath, func(done, _ int) {
			tracker.MaintenanceProgress(done)
		})
		tracker.MaintenanceDone()
		if reconcileErr != nil {
			return fmt.Errorf("maintenance: reconciling flagged topics: %w", reconcileErr)
		}
	}
	return nil
}
