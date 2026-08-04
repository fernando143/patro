package embed

import (
	"context"
	"errors"
	"math"
	"testing"
)

func vectorChunk(kind string, ordinal int, vector []float32) Chunk {
	return Chunk{Kind: kind, Ordinal: ordinal, TokenCount: 1, Vector: vector}
}

func representationWithChunks(id string, chunks ...Chunk) Representation {
	return Representation{DocumentID: id, Dimension: 2, Chunks: chunks}
}

func TestDirectedScoreUsesLateContentMatchWithoutDilution(t *testing.T) {
	query := representationWithChunks("query",
		vectorChunk("content", 0, []float32{1, 0}),
		vectorChunk("content", 1, []float32{0, 1}),
	)
	document := representationWithChunks("document",
		vectorChunk("content", 0, []float32{-1, 0}),
		vectorChunk("content", 1, []float32{0, 1}),
	)
	score, err := DirectedScore(context.Background(), query, document)
	if err != nil {
		t.Fatalf("DirectedScore() error: %v", err)
	}
	if score != 0.5 {
		t.Fatalf("DirectedScore() = %v, want 0.5", score)
	}
}

func TestSymmetricScoreRejectsPartialDuplicateCoverage(t *testing.T) {
	left := representationWithChunks("left",
		vectorChunk("content", 0, []float32{1, 0}),
		vectorChunk("content", 1, []float32{0, 1}),
	)
	right := representationWithChunks("right", vectorChunk("content", 0, []float32{1, 0}))
	score, err := SymmetricScore(context.Background(), left, right)
	if err != nil {
		t.Fatalf("SymmetricScore() error: %v", err)
	}
	if score != 0.5 {
		t.Fatalf("SymmetricScore() = %v, want 0.5", score)
	}
}

func TestTitleContributionIsCappedAndTitleOnlyCannotAuthorizeMerge(t *testing.T) {
	left := representationWithChunks("left",
		vectorChunk("content", 0, []float32{1, 0}),
		vectorChunk("title", 0, []float32{1, 0}),
	)
	right := representationWithChunks("right",
		vectorChunk("content", 0, []float32{0, 1}),
		vectorChunk("title", 0, []float32{1, 0}),
	)
	score, err := DirectedScore(context.Background(), left, right)
	if err != nil {
		t.Fatalf("DirectedScore() error: %v", err)
	}
	if math.Abs(score-0.1) > 1e-6 {
		t.Fatalf("DirectedScore() = %v, want title contribution 0.1", score)
	}
	titleOnlyLeft := representationWithChunks("left", vectorChunk("title", 0, []float32{1, 0}))
	titleOnlyRight := representationWithChunks("right", vectorChunk("title", 0, []float32{1, 0}))
	if CanAuthorizeMerge(titleOnlyLeft, titleOnlyRight) {
		t.Fatal("CanAuthorizeMerge() accepted title-only evidence")
	}
}

func TestRankBreaksEqualScoresByAscendingDocumentID(t *testing.T) {
	query := representationWithChunks("query", vectorChunk("content", 0, []float32{1, 0}))
	docs := []Representation{
		representationWithChunks("zeta", vectorChunk("content", 0, []float32{1, 0})),
		representationWithChunks("alpha", vectorChunk("content", 0, []float32{1, 0})),
	}
	results, err := Rank(context.Background(), query, docs, DirectedMode)
	if err != nil {
		t.Fatalf("Rank() error: %v", err)
	}
	if len(results) != 2 || results[0].ID != "alpha" || results[1].ID != "zeta" {
		t.Fatalf("Rank() = %#v, want alpha then zeta", results)
	}
}

func TestScoringStopsBeforeNextDocumentWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	query := representationWithChunks("query", vectorChunk("content", 0, []float32{1, 0}))
	_, err := Rank(ctx, query, []Representation{representationWithChunks("doc", vectorChunk("content", 0, []float32{1, 0}))}, DirectedMode)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Rank() error = %v, want context.Canceled", err)
	}
}
