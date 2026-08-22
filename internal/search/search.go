// Package search fuses independently ranked retrieval legs into a single
// ordering.
//
// patro retrieves the same library two ways — BM25 over the Markdown index
// and cosine similarity over multi-vector representations — and neither
// score is comparable to the other. Reciprocal rank fusion sidesteps that by
// scoring a document on where it placed rather than on what it scored, so a
// document ranked well by both legs outranks one that only a single leg
// loved.
//
// The algorithm used to live inside the web viewer's HTTP handler, which
// meant it could only be exercised through httptest and could never back a
// command-line search. Ranking is domain logic, not a delivery concern.
package search

import "sort"

// K damps the contribution of top ranks so a single leg cannot dominate the
// fused ordering. 60 is the value from the original RRF paper and the one
// patro has always used.
const K = 60

// Retrieval depth per leg. BM25 is cheap and returns a long tail worth
// fusing; representation search is comparatively expensive, so it
// contributes a shorter, higher-confidence list.
const (
	LexicalLimit  = 50
	SemanticLimit = 8
)

// Candidate is one hit from a single retrieval leg. Kind and Title are
// carried through because the leg that found a document often knows them
// already, sparing the caller a lookup; either may be empty.
type Candidate struct {
	ID    string
	Kind  string
	Title string
}

// Result is a candidate with its fused score.
type Result struct {
	Candidate
	Score float64
}

// ReciprocalRank is the score one leg contributes to the candidate it placed
// at rank, counting from zero.
func ReciprocalRank(rank int) float64 {
	return 1 / float64(K+rank+1)
}

// Fuse merges ranked legs into one ordering by reciprocal rank fusion. Each
// leg must be ordered best-first; a nil or empty leg contributes nothing, so
// an unavailable retrieval path degrades to the remaining legs rather than
// emptying the result.
//
// A candidate appearing in several legs accumulates each leg's contribution
// and keeps the first non-empty Kind and Title seen for it. Results are
// sorted by descending score, ties broken by ID so the ordering is total and
// callers get a deterministic list to apply their own tiebreakers to.
func Fuse(legs ...[]Candidate) []Result {
	scores := make(map[string]float64)
	meta := make(map[string]Candidate)

	for _, leg := range legs {
		for rank, candidate := range leg {
			if candidate.ID == "" {
				continue
			}
			scores[candidate.ID] += ReciprocalRank(rank)

			known := meta[candidate.ID]
			if known.ID == "" {
				known.ID = candidate.ID
			}
			if known.Kind == "" {
				known.Kind = candidate.Kind
			}
			if known.Title == "" {
				known.Title = candidate.Title
			}
			meta[candidate.ID] = known
		}
	}

	results := make([]Result, 0, len(meta))
	for id, candidate := range meta {
		results = append(results, Result{Candidate: candidate, Score: scores[id]})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	return results
}
