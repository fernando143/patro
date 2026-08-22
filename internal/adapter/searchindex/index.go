// Package searchindex wraps bleve for BM25 full-text search only.
//
// It deliberately never touches bleve's vector/kNN field mapping or query
// types: those require the `vectors` build tag, which pulls in
// blevesearch/go-faiss — a cgo binding — and would break this project's
// CGO_ENABLED=0 cross-compiled builds (design D3). Semantic similarity is
// handled entirely by the separate, pure-Go internal/vectors package;
// searchindex only ever uses bleve's default, pure-Go BM25 scoring path
// (bleve.New/Open + query.QueryStringQuery), which is unaffected by the
// vectors tag.
package searchindex

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"

	"github.com/blevesearch/bleve/v2"
)

// ErrRebuilding is returned by Index and Query while a Rebuild is in
// flight, mirroring internal/vectors' fail-fast contract: never a partial
// or stale result while the index is being reconstructed.
var ErrRebuilding = errors.New("searchindex: rebuild in progress")

// Kind values distinguish topic notes from meeting notes in search results.
const (
	KindTopic   = "topic"
	KindMeeting = "meeting"
)

// Document is one full-text indexed record.
type Document struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Hit is one ranked search result.
type Hit struct {
	ID    string
	Kind  string
	Title string
	Score float64
}

// Index is a BM25 full-text index persisted to a directory on disk.
type Index struct {
	mu   sync.RWMutex
	path string
	idx  bleve.Index

	// rebuilding gates Index/Query (fail fast) and single-flights Rebuild,
	// mirroring internal/vectors' Store.
	rebuilding atomic.Bool
}

// Open opens the index persisted at path, creating an empty BM25 index
// there if none exists yet.
func Open(path string) (*Index, error) {
	idx, err := bleve.Open(path)
	if err != nil {
		if errors.Is(err, bleve.ErrorIndexPathDoesNotExist) {
			idx, err = bleve.New(path, bleve.NewIndexMapping())
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return &Index{path: path, idx: idx}, nil
}

// Close releases the underlying bleve index.
func (i *Index) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.idx.Close()
}

// Index adds or replaces d in the index.
func (i *Index) Index(d Document) error {
	if i.rebuilding.Load() {
		return ErrRebuilding
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.idx.Index(d.ID, d)
}

// Query runs a BM25 full-text query and returns up to limit hits ranked by
// descending score.
func (i *Index) Query(q string, limit int) ([]Hit, error) {
	if i.rebuilding.Load() {
		return nil, ErrRebuilding
	}
	i.mu.RLock()
	defer i.mu.RUnlock()

	sq := bleve.NewQueryStringQuery(q)
	req := bleve.NewSearchRequestOptions(sq, limit, 0, false)
	req.Fields = []string{"kind", "title"}

	res, err := i.idx.Search(req)
	if err != nil {
		return nil, err
	}

	hits := make([]Hit, 0, len(res.Hits))
	for _, h := range res.Hits {
		hit := Hit{ID: h.ID, Score: h.Score}
		if v, ok := h.Fields["kind"].(string); ok {
			hit.Kind = v
		}
		if v, ok := h.Fields["title"].(string); ok {
			hit.Title = v
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

// removeAllIfExists is a small os.RemoveAll wrapper used by Rebuild so a
// missing directory is not treated as an error.
func removeAllIfExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.RemoveAll(path)
}
