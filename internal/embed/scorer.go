package embed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
)

const ScorerVersion = "coverage-title-v2"

type ScoreMode string

const (
	DirectedMode  ScoreMode = "directed-reconciliation"
	SymmetricMode ScoreMode = "symmetric-migration"
)

var ErrMissingContent = errors.New("embed: content vectors are required for scoring")

type RankedResult struct {
	ID    string
	Score float64
}

// DirectedScore scores every source chunk against its best target chunk.
func DirectedScore(ctx context.Context, source, target Representation) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	contentSource := chunksOfKind(source, "content")
	contentTarget := chunksOfKind(target, "content")
	if len(contentSource) == 0 || len(contentTarget) == 0 {
		return 0, ErrMissingContent
	}
	content, err := coverage(ctx, contentSource, contentTarget)
	if err != nil {
		return 0, err
	}
	titleSource := chunksOfKind(source, "title")
	titleTarget := chunksOfKind(target, "title")
	if len(titleSource) == 0 || len(titleTarget) == 0 {
		return content, nil
	}
	titleForward, err := coverage(ctx, titleSource, titleTarget)
	if err != nil {
		return 0, err
	}
	titleReverse, err := coverage(ctx, titleTarget, titleSource)
	if err != nil {
		return 0, err
	}
	return .9*content + .1*math.Min(titleForward, titleReverse), nil
}

// SymmetricScore requires reciprocal coverage, preventing partial duplicate
// topics from authorizing historical migration.
func SymmetricScore(ctx context.Context, left, right Representation) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	forward, err := DirectedScore(ctx, left, right)
	if err != nil {
		return 0, err
	}
	reverse, err := DirectedScore(ctx, right, left)
	if err != nil {
		return 0, err
	}
	return math.Min(forward, reverse), nil
}

func CanAuthorizeMerge(source, target Representation) bool {
	return len(chunksOfKind(source, "content")) > 0 && len(chunksOfKind(target, "content")) > 0
}

func Rank(ctx context.Context, query Representation, documents []Representation, mode ScoreMode) ([]RankedResult, error) {
	results := make([]RankedResult, 0, len(documents))
	for _, document := range documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var score float64
		var err error
		switch mode {
		case DirectedMode:
			score, err = DirectedScore(ctx, query, document)
		case SymmetricMode:
			score, err = SymmetricScore(ctx, query, document)
		default:
			return nil, fmt.Errorf("embed: unknown score mode %q", mode)
		}
		if err != nil {
			return nil, err
		}
		results = append(results, RankedResult{ID: document.DocumentID, Score: score})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	return results, nil
}

func chunksOfKind(representation Representation, kind string) []Chunk {
	chunks := make([]Chunk, 0)
	for _, chunk := range representation.Chunks {
		if chunk.Kind == kind {
			chunks = append(chunks, chunk)
		}
	}
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Ordinal < chunks[j].Ordinal })
	return chunks
}

func coverage(ctx context.Context, source, target []Chunk) (float64, error) {
	if len(source) == 0 || len(target) == 0 {
		return 0, ErrMissingContent
	}
	var total float64
	for _, sourceChunk := range source {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		best := -math.MaxFloat64
		for _, targetChunk := range target {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			score, err := dotProduct(sourceChunk.Vector, targetChunk.Vector)
			if err != nil {
				return 0, err
			}
			if score > best {
				best = score
			}
		}
		total += best
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return total / float64(len(source)), nil
}

func dotProduct(left, right []float32) (float64, error) {
	if len(left) == 0 || len(left) != len(right) {
		return 0, fmt.Errorf("embed: incompatible vector dimensions %d and %d", len(left), len(right))
	}
	var result float64
	for i := range left {
		if math.IsNaN(float64(left[i])) || math.IsInf(float64(left[i]), 0) || math.IsNaN(float64(right[i])) || math.IsInf(float64(right[i]), 0) {
			return 0, errors.New("embed: scoring encountered a non-finite vector")
		}
		result += float64(left[i]) * float64(right[i])
	}
	return result, nil
}
