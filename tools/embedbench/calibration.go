package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/fernando143/patro/internal/embed"
)

type calibrationCase struct {
	ID        string  `json:"id"`
	Mode      string  `json:"mode"`
	Score     float64 `json:"score"`
	Duplicate bool    `json:"duplicate"`
}

// calibrationProfiles is independent from filesystem and model loading so
// tests can inject fixed labeled scores and verify byte stability.
func calibrationProfiles(identity embed.RepresentationIdentity, corpusID, corpusSHA string, cases []calibrationCase) (map[string][]byte, error) {
	byMode := map[embed.ScoreMode][]embed.CalibrationSample{embed.DirectedMode: nil, embed.SymmetricMode: nil}
	for _, sample := range cases {
		mode := embed.ScoreMode(sample.Mode)
		if _, ok := byMode[mode]; !ok {
			return nil, fmt.Errorf("calibration: unsupported mode %q", sample.Mode)
		}
		byMode[mode] = append(byMode[mode], embed.CalibrationSample{ID: sample.ID, Score: sample.Score, Duplicate: sample.Duplicate})
	}
	profiles := make(map[string][]byte, 2)
	for _, mode := range []embed.ScoreMode{embed.DirectedMode, embed.SymmetricMode} {
		profile, err := embed.Calibrate(mode, identity, corpusID, corpusSHA, byMode[mode])
		if err != nil {
			return nil, fmt.Errorf("calibration %s unavailable: %w", mode, err)
		}
		data, err := embed.CanonicalCalibrationJSON(profile)
		if err != nil {
			return nil, err
		}
		profiles[string(mode)] = data
	}
	return profiles, nil
}

func runCalibration(path string) int {
	manifest, err := loadManifest(path)
	if err != nil {
		fmt.Printf("calibration unavailable: %v\n", err)
		return 1
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("calibration unavailable: %v\n", err)
		return 1
	}
	if len(embed.Available()) == 0 {
		fmt.Println("calibration unavailable: no embedding backend")
		return 1
	}
	backend, err := embed.New(embed.Available()[0])
	if err != nil {
		fmt.Printf("calibration unavailable: %v\n", err)
		return 1
	}
	if len(manifest.Cases) == 0 {
		fmt.Println("calibration unavailable: corpus case missing")
		return 1
	}
	text, err := caseText(path, manifest.Cases[0].A, manifest.Cases[0].APath)
	if err != nil {
		fmt.Printf("calibration unavailable: %v\n", err)
		return 1
	}
	representation, err := backend.Represent(context.Background(), embed.Document{ID: "calibration", Text: text})
	if err != nil {
		fmt.Printf("calibration unavailable: %v\n", err)
		return 1
	}
	sum := sha256.Sum256(data)
	profiles, err := calibrationProfiles(representation.Identity(), manifest.ID, hex.EncodeToString(sum[:]), manifest.Calibration)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	for _, mode := range []embed.ScoreMode{embed.DirectedMode, embed.SymmetricMode} {
		fmt.Println(string(profiles[string(mode)]))
	}
	return 0
}
