package searchindex

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"

	"github.com/fernando143/patro/internal/platform/layout"
)

// Rebuild walks topicsDir/*.md and meetingsDir/*.md and atomically
// replaces the index with a fresh one built from their content —
// recovering full state from markdown alone (the index is derived and
// deletable; markdown is the source of truth).
//
// Rebuild is single-flight: a second call while one is already running
// returns immediately (nil error) as a no-op, mirroring internal/vectors.
func (i *Index) Rebuild(ctx context.Context, topicsDir, meetingsDir string) error {
	if !i.rebuilding.CompareAndSwap(false, true) {
		return nil // a rebuild is already in flight: single-flight no-op
	}
	defer i.rebuilding.Store(false)

	docs, err := collectDocs(topicsDir, KindTopic, meetingsDir, KindMeeting)
	if err != nil {
		return err
	}

	tmpPath := i.path + ".rebuild-tmp"
	if err := removeAllIfExists(tmpPath); err != nil {
		return err
	}
	fresh, err := bleve.New(tmpPath, bleve.NewIndexMapping())
	if err != nil {
		return err
	}
	for _, d := range docs {
		if err := ctx.Err(); err != nil {
			fresh.Close()
			removeAllIfExists(tmpPath)
			return err
		}
		if err := fresh.Index(d.ID, d); err != nil {
			fresh.Close()
			removeAllIfExists(tmpPath)
			return err
		}
	}
	if err := fresh.Close(); err != nil {
		removeAllIfExists(tmpPath)
		return err
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if i.idx != nil {
		if err := i.idx.Close(); err != nil {
			return err
		}
	}
	if err := removeAllIfExists(i.path); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, i.path); err != nil {
		return err
	}
	reopened, err := bleve.Open(i.path)
	if err != nil {
		return err
	}
	i.idx = reopened
	return nil
}

// collectDocs walks topicsDir/*.md and meetingsDir/*.md, in that order, and
// builds one Document per readable file, using its first "# " heading as
// the title (falling back to the file's stem) and the full file body as
// content.
func collectDocs(topicsDir, topicsKind, meetingsDir, meetingsKind string) ([]Document, error) {
	var docs []Document
	for _, pair := range []struct {
		dir  string
		kind string
	}{
		{topicsDir, topicsKind},
		{meetingsDir, meetingsKind},
	} {
		files, err := filepath.Glob(filepath.Join(pair.dir, "*.md"))
		if err != nil {
			return nil, err
		}
		sort.Strings(files) // deterministic order
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue // best-effort: skip unreadable entries
			}
			id := pair.kind + ":" + layout.Stem(f)
			docs = append(docs, Document{
				ID:      id,
				Kind:    pair.kind,
				Title:   titleOf(string(data), layout.Stem(f)),
				Content: string(data),
			})
		}
	}
	return docs, nil
}

// stem returns the file name without its final extension.
// titleOf extracts the first "# " heading from markdown text, falling back
// to fallback when there is none.
func titleOf(text, fallback string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSuffix(line, "\r")
	if strings.HasPrefix(line, "# ") {
		return strings.TrimSpace(line[2:])
	}
	return fallback
}
