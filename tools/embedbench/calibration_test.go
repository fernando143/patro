package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/embed"
)

func TestCalibrationProfilesAreSeparateAndCanonical(t *testing.T) {
	identity := embed.RepresentationIdentity{
		SchemaVersion: embed.RepresentationSchema, Backend: "test", ModelID: "model", ModelVersion: "v1",
		ModelWeightsSHA256: strings.Repeat("a", 64), TokenizerSHA256: strings.Repeat("b", 64),
		ChunkerVersion: embed.ChunkerVersion, NormalizationVersion: embed.NormalizationVersion, Dimension: 2,
	}
	cases := make([]calibrationCase, 0, 80)
	for _, mode := range []embed.ScoreMode{embed.DirectedMode, embed.SymmetricMode} {
		for i := 0; i < 20; i++ {
			cases = append(cases, calibrationCase{ID: string(mode) + "-negative-" + string(rune('a'+i)), Mode: string(mode), Score: .1 + float64(i)/1000})
			cases = append(cases, calibrationCase{ID: string(mode) + "-positive-" + string(rune('a'+i)), Mode: string(mode), Score: .9 + float64(i)/1000, Duplicate: true})
		}
	}
	profiles, err := calibrationProfiles(identity, "fixture", strings.Repeat("c", 64), cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || string(profiles[string(embed.DirectedMode)]) == string(profiles[string(embed.SymmetricMode)]) {
		t.Fatal("calibration did not emit distinct mode profiles")
	}
	var fields map[string]any
	if err := json.Unmarshal(profiles[string(embed.DirectedMode)], &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 16 || fields["profile_schema"] != embed.CalibrationSchema {
		t.Fatalf("profile fields = %v", fields)
	}
}
