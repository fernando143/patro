package embed

import (
	"reflect"
	"strings"
	"testing"
)

func TestCalibrationProducesSeparateStableProfiles(t *testing.T) {
	_, model := fixtureIdentity()
	identity := RepresentationIdentityFrom(TokenizerIdentity{ConfigSHA256: strings.Repeat("a", 64), VocabSHA256: strings.Repeat("b", 64)}, model)
	corpus := make([]CalibrationSample, 0, 40)
	for i := 0; i < 20; i++ {
		corpus = append(corpus, CalibrationSample{ID: "positive-" + string(rune('a'+i)), Score: 0.90 + float64(i)/1000, Duplicate: true})
	}
	for i := 0; i < 20; i++ {
		corpus = append(corpus, CalibrationSample{ID: "negative-" + string(rune('a'+i)), Score: 0.10 + float64(i)/1000, Duplicate: false})
	}
	first, err := Calibrate(DirectedMode, identity, "fixture-corpus", strings.Repeat("d", 64), corpus)
	if err != nil {
		t.Fatalf("Calibrate(directed) error: %v", err)
	}
	second, err := Calibrate(SymmetricMode, identity, "fixture-corpus", strings.Repeat("d", 64), reverseSamples(corpus))
	if err != nil {
		t.Fatalf("Calibrate(symmetric) error: %v", err)
	}
	if first.ScorerMode != string(DirectedMode) || second.ScorerMode != string(SymmetricMode) {
		t.Fatalf("profiles modes = %q/%q, want separate modes", first.ScorerMode, second.ScorerMode)
	}
	if !(first.N < first.M) || first.PositiveSupport < 20 || first.NegativeSupport < 20 {
		t.Fatalf("profile bands/support = n=%v m=%v negative=%d positive=%d", first.N, first.M, first.NegativeSupport, first.PositiveSupport)
	}
	canonical, err := CanonicalCalibrationJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	canonicalAgain, err := CanonicalCalibrationJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canonical, canonicalAgain) {
		t.Fatal("calibration canonical bytes are not stable")
	}
	if !ProfileMatches(first, identity, DirectedMode) || ProfileMatches(first, identity, SymmetricMode) {
		t.Fatal("ProfileMatches() accepted an incorrect mode")
	}
}

func TestCalibrationRejectsInsufficientOrOverlappingSupport(t *testing.T) {
	_, model := fixtureIdentity()
	identity := RepresentationIdentityFrom(TokenizerIdentity{ConfigSHA256: strings.Repeat("a", 64), VocabSHA256: strings.Repeat("b", 64)}, model)
	tooSmall := []CalibrationSample{{ID: "one", Score: .9, Duplicate: true}}
	if _, err := Calibrate(DirectedMode, identity, "small", strings.Repeat("d", 64), tooSmall); err == nil {
		t.Fatal("Calibrate() accepted insufficient support")
	}
	corpus := make([]CalibrationSample, 0, 40)
	for i := 0; i < 20; i++ {
		corpus = append(corpus, CalibrationSample{ID: "duplicate-" + string(rune('a'+i)), Score: .5, Duplicate: true})
		corpus = append(corpus, CalibrationSample{ID: "negative-" + string(rune('a'+i)), Score: .5, Duplicate: false})
	}
	if _, err := Calibrate(DirectedMode, identity, "overlap", strings.Repeat("d", 64), corpus); err == nil {
		t.Fatal("Calibrate() accepted overlapping score support")
	}
}

func reverseSamples(samples []CalibrationSample) []CalibrationSample {
	result := make([]CalibrationSample, len(samples))
	for i := range samples {
		result[len(samples)-1-i] = samples[i]
	}
	return result
}
