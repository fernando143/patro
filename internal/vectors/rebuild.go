package vectors

import (
	"context"
	"os"
	"path/filepath"
	"sort"
)

// Rebuild walks source/*.md (a topics directory), re-embeds every file's
// content with the store's configured embedder, and atomically replaces
// the store's entries — recovering full state from markdown alone (design:
// markdown is the source of truth, the store is derived and deletable).
//
// Rebuild is single-flight: a second call while one is already running
// returns immediately (nil error) as a no-op rather than running two
// concurrent embed passes (design D10). It never blocks the caller's own
// goroutine scheduling — it is meant to be launched in a background
// goroutine by serve/cmd — and Nearest fails fast with ErrRebuilding for
// its entire duration, so ingestion elsewhere is never stalled waiting on
// this store's lock.
//
// onProgress, if non-nil, is called after each file is processed with
// (done, total). It may be nil.
func (s *Store) Rebuild(ctx context.Context, source string, onProgress func(done, total int)) error {
	if !s.rebuilding.CompareAndSwap(false, true) {
		return nil // a rebuild is already in flight: single-flight no-op
	}
	defer s.rebuilding.Store(false)

	files, err := filepath.Glob(filepath.Join(source, "*.md"))
	if err != nil {
		return err
	}
	sort.Strings(files) // deterministic processing order

	total := len(files)
	newEntries := make(map[string][]float32, total)
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}

		data, err := os.ReadFile(f)
		if err != nil {
			// Best-effort: an unreadable file (e.g. a stray directory
			// matching *.md) should not abort the whole rebuild.
			if onProgress != nil {
				onProgress(i+1, total)
			}
			continue
		}

		vec, err := s.embedder.Embed(ctx, string(data))
		if err != nil {
			if onProgress != nil {
				onProgress(i+1, total)
			}
			continue
		}

		newEntries[stem(f)] = vec
		if onProgress != nil {
			onProgress(i+1, total)
		}
	}

	s.mu.Lock()
	s.entries = newEntries
	s.backend, s.dim, s.modelVersion = s.wantBackend, s.wantDim, s.wantModelVersion
	s.needsRebuild = false
	flushErr := s.flushLocked()
	s.mu.Unlock()
	return flushErr
}

// stem returns the file name without its final extension, e.g.
// "topics/foo.md" -> "foo". Mirrors internal/library's private helper of
// the same name.
func stem(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}
