package search

import (
	"math"
	"testing"
)

func ids(results []Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestReciprocalRankMatchesTheEstablishedFormula pins the exact scores the
// web viewer produced before fusion moved out of the handler.
func TestReciprocalRankMatchesTheEstablishedFormula(t *testing.T) {
	for rank, want := range map[int]float64{0: 1.0 / 61, 1: 1.0 / 62, 9: 1.0 / 70} {
		if got := ReciprocalRank(rank); math.Abs(got-want) > 1e-12 {
			t.Errorf("ReciprocalRank(%d) = %v, want %v", rank, got, want)
		}
	}
}

// TestFuseRewardsAgreementBetweenLegs is the property that justifies RRF at
// all: a document both legs rank second beats one that only a single leg
// ranked first.
func TestFuseRewardsAgreementBetweenLegs(t *testing.T) {
	lexical := []Candidate{{ID: "topic:solo"}, {ID: "topic:agreed"}}
	semantic := []Candidate{{ID: "topic:other"}, {ID: "topic:agreed"}}

	got := ids(Fuse(lexical, semantic))
	if got[0] != "topic:agreed" {
		t.Errorf("Fuse ordering = %v, want the agreed document first", got)
	}
}

// TestFuseAccumulatesAcrossLegs checks the arithmetic rather than just the
// ordering.
func TestFuseAccumulatesAcrossLegs(t *testing.T) {
	results := Fuse(
		[]Candidate{{ID: "a"}, {ID: "b"}},
		[]Candidate{{ID: "b"}},
	)

	want := map[string]float64{
		"a": ReciprocalRank(0),
		"b": ReciprocalRank(1) + ReciprocalRank(0),
	}
	for _, r := range results {
		if math.Abs(r.Score-want[r.ID]) > 1e-12 {
			t.Errorf("score(%s) = %v, want %v", r.ID, r.Score, want[r.ID])
		}
	}
}

// TestFuseDegradesToASingleLeg covers the case that actually happens in
// production: one retrieval path is unavailable, and search must still work
// rather than return nothing.
func TestFuseDegradesToASingleLeg(t *testing.T) {
	only := []Candidate{{ID: "topic:a"}, {ID: "topic:b"}}

	for name, legs := range map[string][][]Candidate{
		"nil second leg":   {only, nil},
		"empty second leg": {only, {}},
		"nil first leg":    {nil, only},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ids(Fuse(legs...)); !equal(got, []string{"topic:a", "topic:b"}) {
				t.Errorf("Fuse = %v, want the surviving leg's order", got)
			}
		})
	}
}

// TestFuseKeepsFirstNonEmptyMetadata: the lexical leg knows Kind and Title
// from the index, the semantic leg usually does not. Metadata must survive
// the merge regardless of which leg saw the document first.
func TestFuseKeepsFirstNonEmptyMetadata(t *testing.T) {
	results := Fuse(
		[]Candidate{{ID: "topic:x"}},
		[]Candidate{{ID: "topic:x", Kind: "topic", Title: "Onboarding"}},
	)

	if len(results) != 1 {
		t.Fatalf("Fuse returned %d results, want 1", len(results))
	}
	if results[0].Kind != "topic" || results[0].Title != "Onboarding" {
		t.Errorf("metadata = %+v, want kind and title filled from the later leg", results[0].Candidate)
	}
}

// TestFuseIsDeterministicOnTies keeps the rendered order stable between
// identical queries.
func TestFuseIsDeterministicOnTies(t *testing.T) {
	legs := [][]Candidate{{{ID: "topic:b"}}, {{ID: "topic:a"}}}
	for range 20 {
		if got := ids(Fuse(legs...)); !equal(got, []string{"topic:a", "topic:b"}) {
			t.Fatalf("Fuse = %v, want ties broken by ID", got)
		}
	}
}

// TestFuseSkipsEmptyIDs guards against a malformed hit creating a phantom
// result with no document behind it.
func TestFuseSkipsEmptyIDs(t *testing.T) {
	if got := Fuse([]Candidate{{ID: ""}, {ID: "topic:real"}}); len(got) != 1 || got[0].ID != "topic:real" {
		t.Errorf("Fuse = %+v, want only the real document", got)
	}
}

// TestFuseWithNoLegsReturnsNothing covers the empty query path.
func TestFuseWithNoLegsReturnsNothing(t *testing.T) {
	if got := Fuse(); len(got) != 0 {
		t.Errorf("Fuse() = %+v, want no results", got)
	}
}
