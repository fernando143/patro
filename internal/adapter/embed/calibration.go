package embed

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const CalibrationSchema = "calibration-v1"

type CalibrationSample struct {
	ID        string
	Score     float64
	Duplicate bool
}

type CalibrationProfile struct {
	ProfileSchema             string  `json:"profile_schema"`
	ScorerMode                string  `json:"scorer_mode"`
	ScorerVersion             string  `json:"scorer_version"`
	Backend                   string  `json:"backend"`
	ModelID                   string  `json:"model_id"`
	ModelVersion              string  `json:"model_version"`
	ModelWeightsSHA256        string  `json:"model_weights_sha256"`
	RepresentationFingerprint string  `json:"representation_fingerprint"`
	NormalizationVersion      string  `json:"normalization_version"`
	CorpusID                  string  `json:"corpus_id"`
	CorpusSHA256              string  `json:"corpus_sha256"`
	SampleCount               int     `json:"sample_count"`
	NegativeSupport           int     `json:"negative_support"`
	PositiveSupport           int     `json:"positive_support"`
	N                         float64 `json:"n"`
	M                         float64 `json:"m"`
}

// Calibrate computes conservative low/high bands from one labeled corpus.
func Calibrate(mode ScoreMode, identity RepresentationIdentity, corpusID, corpusSHA256 string, samples []CalibrationSample) (CalibrationProfile, error) {
	if mode != DirectedMode && mode != SymmetricMode {
		return CalibrationProfile{}, fmt.Errorf("embed: unknown calibration mode %q", mode)
	}
	if len(samples) == 0 {
		return CalibrationProfile{}, errors.New("embed: calibration corpus is empty")
	}
	ordered := append([]CalibrationSample(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score < ordered[j].Score
		}
		return ordered[i].ID < ordered[j].ID
	})
	seen := make(map[string]struct{}, len(ordered))
	for _, sample := range ordered {
		if sample.ID == "" {
			return CalibrationProfile{}, errors.New("embed: calibration sample ID is empty")
		}
		if _, exists := seen[sample.ID]; exists {
			return CalibrationProfile{}, fmt.Errorf("embed: duplicate calibration sample ID %q", sample.ID)
		}
		seen[sample.ID] = struct{}{}
	}

	var n, m float64
	var negativeSupport, positiveSupport int
	for _, sample := range ordered {
		if !sample.Duplicate {
			candidate := sample.Score
			count := 0
			valid := true
			for _, lower := range ordered {
				if lower.Score <= candidate {
					if lower.Duplicate {
						valid = false
						break
					}
					count++
				}
			}
			if valid && count >= 20 && count >= negativeSupport {
				n, negativeSupport = candidate, count
			}
		}
		if sample.Duplicate {
			candidate := sample.Score
			count := 0
			valid := true
			for _, higher := range ordered {
				if higher.Score >= candidate {
					if !higher.Duplicate {
						valid = false
						break
					}
					count++
				}
			}
			if valid && count >= 20 && (positiveSupport == 0 || candidate < m) {
				m, positiveSupport = candidate, count
			}
		}
	}
	if negativeSupport < 20 || positiveSupport < 20 {
		return CalibrationProfile{}, fmt.Errorf("embed: insufficient calibration support: negative=%d positive=%d", negativeSupport, positiveSupport)
	}
	if !(n < m) {
		return CalibrationProfile{}, fmt.Errorf("embed: calibration bands overlap: n=%v m=%v", n, m)
	}
	return CalibrationProfile{
		ProfileSchema:             CalibrationSchema,
		ScorerMode:                string(mode),
		ScorerVersion:             ScorerVersion,
		Backend:                   identity.Backend,
		ModelID:                   identity.ModelID,
		ModelVersion:              identity.ModelVersion,
		ModelWeightsSHA256:        identity.ModelWeightsSHA256,
		RepresentationFingerprint: representationFingerprint(identity),
		NormalizationVersion:      identity.NormalizationVersion,
		CorpusID:                  corpusID,
		CorpusSHA256:              corpusSHA256,
		SampleCount:               len(ordered),
		NegativeSupport:           negativeSupport,
		PositiveSupport:           positiveSupport,
		N:                         n,
		M:                         m,
	}, nil
}

func CanonicalCalibrationJSON(profile CalibrationProfile) ([]byte, error) {
	return json.Marshal(map[string]any{
		"backend":                    profile.Backend,
		"corpus_id":                  profile.CorpusID,
		"corpus_sha256":              profile.CorpusSHA256,
		"m":                          profile.M,
		"model_id":                   profile.ModelID,
		"model_version":              profile.ModelVersion,
		"model_weights_sha256":       profile.ModelWeightsSHA256,
		"n":                          profile.N,
		"negative_support":           profile.NegativeSupport,
		"normalization_version":      profile.NormalizationVersion,
		"positive_support":           profile.PositiveSupport,
		"profile_schema":             profile.ProfileSchema,
		"representation_fingerprint": profile.RepresentationFingerprint,
		"sample_count":               profile.SampleCount,
		"scorer_mode":                profile.ScorerMode,
		"scorer_version":             profile.ScorerVersion,
	})
}

func ProfileMatches(profile CalibrationProfile, identity RepresentationIdentity, mode ScoreMode) bool {
	return profile.ProfileSchema == CalibrationSchema && profile.ScorerMode == string(mode) &&
		profile.ScorerVersion == ScorerVersion && profile.Backend == identity.Backend &&
		profile.ModelID == identity.ModelID && profile.ModelVersion == identity.ModelVersion &&
		profile.ModelWeightsSHA256 == identity.ModelWeightsSHA256 &&
		profile.RepresentationFingerprint == representationFingerprint(identity) &&
		profile.NormalizationVersion == identity.NormalizationVersion && profile.N < profile.M
}

func representationFingerprint(identity RepresentationIdentity) string {
	return Fingerprint(identity)
}
