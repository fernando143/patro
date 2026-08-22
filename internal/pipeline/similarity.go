package pipeline

import (
	"context"
	"errors"

	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/library"
	"github.com/fernando143/patro/internal/types"
)

// representationSimilarity answers the domain's "which existing topic is
// this most like?" using multi-vector representations.
//
// It lives here rather than in internal/library because it is the adapter
// side of that port: the knowledge domain states the question, and this
// composition layer — which already knows about embedders and vector stores
// — supplies an implementation. It lives here rather than in
// internal/vectors because rendering a candidate topic as Markdown is
// library's convention, not the store's.
type representationSimilarity struct {
	representer embed.Embedder
	store       interface {
		NearestRepresentations(context.Context, embed.Representation, embed.ScoreMode, int) ([]embed.RankedResult, error)
	}
}

// Nearest implements library.TopicSimilarity.
func (s representationSimilarity) Nearest(ctx context.Context, candidate types.Topic) (library.NearestTopic, error) {
	representation, err := s.representer.Represent(ctx, embed.Document{
		ID: candidate.Slug,
		// Mirrors how a topic is written to disk, so a candidate is
		// represented the same way the stored topics it is compared against
		// were.
		Text: "# " + candidate.Name + "\n\n" + candidate.Content,
	})
	if err != nil {
		return library.NearestTopic{}, err
	}
	if representation == nil {
		return library.NearestTopic{}, errors.New("pipeline: representation backend returned nil representation")
	}

	results, err := s.store.NearestRepresentations(ctx, *representation, embed.DirectedMode, 1)
	if err != nil {
		return library.NearestTopic{}, err
	}
	if len(results) == 0 {
		return library.NearestTopic{}, nil
	}
	return library.NearestTopic{Slug: results[0].ID, Score: results[0].Score}, nil
}
