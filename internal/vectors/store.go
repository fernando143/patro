// Package vectors is a flat, brute-force cosine similarity store for topic
// embeddings, persisted to a single JSON file.
//
// It is deliberately not backed by bleve or any HNSW/ANN library: at the
// scale of hundreds to low thousands of topics, a brute-force scan is
// sub-millisecond and needs no index structure at all (design D2). All
// embed.Embedder backends return L2-normalized vectors, so cosine
// similarity reduces to a plain dot product.
//
// The store is backend-tagged and self-invalidating (design D10): the
// persisted file records {backend, dim, model_version}, and a mismatch on
// load discards the stale entries rather than risk comparing vectors from
// two different embedding spaces. NeedsRebuild reports when that has
// happened (or when nothing has been persisted yet); the caller decides
// when to run Rebuild — this package never triggers one on its own, so it
// never blocks whatever constructed the store.
//
// This package does not import internal/status: progress reporting during
// Rebuild is a plain callback, not a Tracker dependency, keeping vectors
// independent of the serve process (design D12 "Layering").
package vectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/fernando143/patro/internal/embed"
)

// ErrRebuilding is returned by Nearest while a Rebuild is in flight. It is
// the fail-safe signal the reconciler maps to "new topic, flagged for
// review" rather than risk answering from a half-rebuilt, inconsistent
// store (design D10 threat matrix: "concurrent state mutation").
var ErrRebuilding = errors.New("vectors: rebuild in progress")

// entry is one persisted {id, vector} pair.
type entry struct {
	ID     string    `json:"id"`
	Vector []float32 `json:"vector"`
}

// fileFormat is the on-disk JSON shape. The backend/dim/model_version tag
// is what makes the store self-invalidating: switching embedding_backend
// (or the underlying model) is detected instead of silently producing
// meaningless similarity scores.
type fileFormat struct {
	Backend      string  `json:"backend"`
	Dim          int     `json:"dim"`
	ModelVersion string  `json:"model_version"`
	Entries      []entry `json:"entries"`
}

// Result is one nearest-neighbor hit, ranked by descending cosine
// similarity.
type Result struct {
	ID    string
	Score float64
}

// Store is a flat cosine similarity store over topic vectors.
type Store struct {
	path     string
	embedder embed.Embedder

	// want* is the tag a valid, up-to-date store must carry: the embedder's
	// own identity plus the caller-supplied model version. It never changes
	// after construction.
	wantBackend      string
	wantDim          int
	wantModelVersion string

	mu           sync.RWMutex
	backend      string // tag actually persisted (may be stale)
	dim          int
	modelVersion string
	entries      map[string][]float32
	needsRebuild bool

	// rebuilding gates Nearest (fail fast, never a partial result) and
	// single-flights Rebuild (a second trigger while one runs is a no-op).
	// It is true for the Rebuild call's entire duration, including the
	// final entries swap, so there is no window where Nearest could
	// observe a half-rebuilt store.
	rebuilding atomic.Bool
}

// NewStore opens (or initializes) the store persisted at path. embedder
// identifies the current embedding backend (its Name/Dim become the
// store's expected tag); modelVersion further distinguishes model updates
// within the same backend (design D10).
//
// A missing file, or a persisted tag that does not match
// {embedder.Name(), embedder.Dim(), modelVersion}, leaves the store empty
// with NeedsRebuild() true — the caller decides when to run Rebuild, so
// construction never blocks on it.
func NewStore(path string, embedder embed.Embedder, modelVersion string) (*Store, error) {
	s := &Store{
		path:             path,
		embedder:         embedder,
		wantBackend:      embedder.Name(),
		wantDim:          embedder.Dim(),
		wantModelVersion: modelVersion,
		entries:          map[string][]float32{},
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.needsRebuild = true
			return s, nil
		}
		return nil, err
	}

	var ff fileFormat
	if err := json.Unmarshal(raw, &ff); err != nil {
		return nil, fmt.Errorf("vectors: parsing %s: %w", path, err)
	}

	if ff.Backend != s.wantBackend || ff.Dim != s.wantDim || ff.ModelVersion != s.wantModelVersion {
		// Stale tag: these vectors live in a different embedding space.
		// Discard them rather than risk a wrong similarity comparison.
		s.needsRebuild = true
		return s, nil
	}

	for _, e := range ff.Entries {
		s.entries[e.ID] = e.Vector
	}
	s.backend, s.dim, s.modelVersion = ff.Backend, ff.Dim, ff.ModelVersion
	return s, nil
}

// NeedsRebuild reports whether the store has no valid data for the current
// backend/dim/model_version — either nothing was ever persisted, or the
// persisted tag did not match. The caller (serve startup, patro reconcile)
// decides when to act on this by calling Rebuild.
func (s *Store) NeedsRebuild() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.needsRebuild
}

// Upsert stores (or replaces) the vector for id. vec must have the store's
// configured dimensionality.
func (s *Store) Upsert(id string, vec []float32) error {
	if len(vec) != s.wantDim {
		return fmt.Errorf("vectors: Upsert(%q): vector has dim %d, want %d", id, len(vec), s.wantDim)
	}
	cp := make([]float32, len(vec))
	copy(cp, vec)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = cp
	s.backend, s.dim, s.modelVersion = s.wantBackend, s.wantDim, s.wantModelVersion
	s.needsRebuild = false
	return s.flushLocked()
}

// Nearest returns up to k entries ranked by descending cosine similarity to
// vec. It returns ErrRebuilding — never a partial or stale result — while a
// Rebuild is in flight (design D10).
func (s *Store) Nearest(vec []float32, k int) ([]Result, error) {
	if s.rebuilding.Load() {
		return nil, ErrRebuilding
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]Result, 0, len(s.entries))
	for id, v := range s.entries {
		results = append(results, Result{ID: id, Score: cosine(vec, v)})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID // deterministic tie-break
	})
	if k >= 0 && k < len(results) {
		results = results[:k]
	}
	return results, nil
}

// cosine computes the dot product of a and b. Every embed.Embedder backend
// returns L2-normalized (unit-length) vectors, so the dot product equals
// cosine similarity (design D2) without a separate normalization step.
func cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// flushLocked writes the store atomically (temp file + rename), mirroring
// internal/status's flush pattern. The caller must hold s.mu.
func (s *Store) flushLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic file content

	ff := fileFormat{
		Backend:      s.backend,
		Dim:          s.dim,
		ModelVersion: s.modelVersion,
		Entries:      make([]entry, 0, len(ids)),
	}
	for _, id := range ids {
		ff.Entries = append(ff.Entries, entry{ID: id, Vector: s.entries[id]})
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".topics-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ff); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
