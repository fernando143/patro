package vectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fernando143/patro/internal/embed"
)

var ErrV2NeedsRebuild = errors.New("vectors: v2 representation needs rebuild")

type v2File struct {
	SchemaVersion             int       `json:"schema_version"`
	Backend                   string    `json:"backend"`
	ModelID                   string    `json:"model_id"`
	ModelVersion              string    `json:"model_version"`
	ModelWeightsSHA256        string    `json:"model_weights_sha256"`
	TokenizerSHA256           string    `json:"tokenizer_sha256"`
	ChunkerVersion            string    `json:"chunker_version"`
	NormalizationVersion      string    `json:"normalization_version"`
	RepresentationFingerprint string    `json:"representation_fingerprint"`
	ScorerVersion             string    `json:"scorer_version"`
	Dimension                 int       `json:"dimension"`
	Entries                   []v2Entry `json:"entries"`
}

type v2Entry struct {
	ID         string        `json:"id"`
	SourceHash string        `json:"source_hash"`
	Chunks     []embed.Chunk `json:"chunks"`
}

// WriteV2 writes a deterministic, canonical v2 representation snapshot.
func WriteV2(path string, identity embed.RepresentationIdentity, representations []embed.Representation) error {
	data, err := marshalV2(identity, representations)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vectors-v2-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func marshalV2(identity embed.RepresentationIdentity, representations []embed.Representation) ([]byte, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	entries := make([]v2Entry, 0, len(representations))
	seen := make(map[string]struct{}, len(representations))
	for _, representation := range representations {
		if _, exists := seen[representation.DocumentID]; exists {
			return nil, fmt.Errorf("vectors: duplicate representation ID %q", representation.DocumentID)
		}
		seen[representation.DocumentID] = struct{}{}
		if err := validateRepresentation(representation, identity); err != nil {
			return nil, err
		}
		chunks := append([]embed.Chunk(nil), representation.Chunks...)
		sort.Slice(chunks, func(i, j int) bool {
			if chunks[i].Kind != chunks[j].Kind {
				return chunks[i].Kind < chunks[j].Kind
			}
			return chunks[i].Ordinal < chunks[j].Ordinal
		})
		entries = append(entries, v2Entry{ID: representation.DocumentID, SourceHash: representation.SourceHash, Chunks: chunks})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	file := v2File{
		SchemaVersion:             embed.RepresentationSchema,
		Backend:                   identity.Backend,
		ModelID:                   identity.ModelID,
		ModelVersion:              identity.ModelVersion,
		ModelWeightsSHA256:        identity.ModelWeightsSHA256,
		TokenizerSHA256:           identity.TokenizerSHA256,
		ChunkerVersion:            identity.ChunkerVersion,
		NormalizationVersion:      identity.NormalizationVersion,
		RepresentationFingerprint: embed.Fingerprint(identity),
		ScorerVersion:             embed.ScorerVersion,
		Dimension:                 identity.Dimension,
		Entries:                   entries,
	}
	data, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// LoadV2 loads a v2 snapshot. A legacy, stale, or incompatible snapshot is
// reported through needsRebuild so callers can rebuild from Markdown without
// ever scoring vectors from the wrong identity.
func LoadV2(path string, identity embed.RepresentationIdentity) ([]embed.Representation, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, false, err
	}
	var schema int
	if data, ok := fields["schema_version"]; ok {
		if err := json.Unmarshal(data, &schema); err != nil {
			return nil, false, err
		}
	}
	if schema != embed.RepresentationSchema {
		return nil, true, nil
	}
	for key := range fields {
		if !allowedV2Field(key) {
			return nil, false, fmt.Errorf("vectors: unsupported or alias v2 field %q", key)
		}
	}
	var file v2File
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, false, err
	}
	stored := embed.RepresentationIdentity{
		SchemaVersion:        file.SchemaVersion,
		Backend:              file.Backend,
		ModelID:              file.ModelID,
		ModelVersion:         file.ModelVersion,
		ModelWeightsSHA256:   file.ModelWeightsSHA256,
		TokenizerSHA256:      file.TokenizerSHA256,
		ChunkerVersion:       file.ChunkerVersion,
		NormalizationVersion: file.NormalizationVersion,
		Dimension:            file.Dimension,
	}
	if !sameIdentity(stored, identity) || file.RepresentationFingerprint != embed.Fingerprint(identity) || file.ScorerVersion != embed.ScorerVersion {
		return nil, true, nil
	}
	if err := validateIdentity(stored); err != nil {
		return nil, false, err
	}
	result := make([]embed.Representation, 0, len(file.Entries))
	for _, entry := range file.Entries {
		representation := embed.Representation{
			SchemaVersion:             identity.SchemaVersion,
			DocumentID:                entry.ID,
			SourceHash:                entry.SourceHash,
			Backend:                   identity.Backend,
			ModelID:                   identity.ModelID,
			ModelVersion:              identity.ModelVersion,
			ModelWeightsSHA256:        identity.ModelWeightsSHA256,
			TokenizerSHA256:           identity.TokenizerSHA256,
			ChunkerVersion:            identity.ChunkerVersion,
			NormalizationVersion:      identity.NormalizationVersion,
			RepresentationFingerprint: embed.Fingerprint(identity),
			Dimension:                 identity.Dimension,
			Chunks:                    entry.Chunks,
		}
		if err := validateRepresentation(representation, identity); err != nil {
			return nil, false, err
		}
		result = append(result, representation)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DocumentID < result[j].DocumentID })
	return result, false, nil
}

func allowedV2Field(key string) bool {
	switch key {
	case "schema_version", "backend", "model_id", "model_version", "model_weights_sha256", "tokenizer_sha256", "chunker_version", "normalization_version", "representation_fingerprint", "scorer_version", "dimension", "entries":
		return true
	default:
		return false
	}
}

func validateIdentity(identity embed.RepresentationIdentity) error {
	if identity.SchemaVersion != embed.RepresentationSchema || identity.Dimension <= 0 || identity.ChunkerVersion != embed.ChunkerVersion || identity.NormalizationVersion != embed.NormalizationVersion {
		return ErrV2NeedsRebuild
	}
	for name, value := range map[string]string{"model_weights_sha256": identity.ModelWeightsSHA256, "tokenizer_sha256": identity.TokenizerSHA256} {
		if !isLowerHex64(value) {
			return fmt.Errorf("vectors: invalid %s", name)
		}
	}
	return nil
}

func validateRepresentation(representation embed.Representation, identity embed.RepresentationIdentity) error {
	if representation.DocumentID == "" || !isLowerHex64(representation.SourceHash) {
		return fmt.Errorf("vectors: invalid representation identity for %q", representation.DocumentID)
	}
	if !sameIdentity(embed.RepresentationIdentity{
		SchemaVersion: representation.SchemaVersion, Backend: representation.Backend, ModelID: representation.ModelID,
		ModelVersion: representation.ModelVersion, ModelWeightsSHA256: representation.ModelWeightsSHA256,
		TokenizerSHA256: representation.TokenizerSHA256, ChunkerVersion: representation.ChunkerVersion,
		NormalizationVersion: representation.NormalizationVersion, Dimension: representation.Dimension,
	}, identity) || representation.RepresentationFingerprint != embed.Fingerprint(identity) {
		return fmt.Errorf("vectors: representation %q identity mismatch", representation.DocumentID)
	}
	seen := make(map[string]struct{}, len(representation.Chunks))
	for _, chunk := range representation.Chunks {
		key := fmt.Sprintf("%s:%d", chunk.Kind, chunk.Ordinal)
		if _, exists := seen[key]; exists || (chunk.Kind != "title" && chunk.Kind != "content") || chunk.Ordinal < 0 || chunk.TokenCount <= 0 || chunk.SourceStartRune < 0 || chunk.SourceEndRune <= chunk.SourceStartRune {
			return fmt.Errorf("vectors: invalid chunk %q in %q", key, representation.DocumentID)
		}
		seen[key] = struct{}{}
		if len(chunk.Vector) != identity.Dimension {
			return fmt.Errorf("vectors: chunk %q has dimension %d, want %d", key, len(chunk.Vector), identity.Dimension)
		}
		var norm float64
		for _, value := range chunk.Vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return fmt.Errorf("vectors: chunk %q is non-finite", key)
			}
			norm += float64(value) * float64(value)
		}
		if math.Abs(math.Sqrt(norm)-1) > 1e-5 {
			return fmt.Errorf("vectors: chunk %q is not unit norm", key)
		}
	}
	return nil
}

func sameIdentity(left, right embed.RepresentationIdentity) bool {
	return left.SchemaVersion == right.SchemaVersion && left.Backend == right.Backend && left.ModelID == right.ModelID &&
		left.ModelVersion == right.ModelVersion && left.ModelWeightsSHA256 == right.ModelWeightsSHA256 &&
		left.TokenizerSHA256 == right.TokenizerSHA256 && left.ChunkerVersion == right.ChunkerVersion &&
		left.NormalizationVersion == right.NormalizationVersion && left.Dimension == right.Dimension
}

func isLowerHex64(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
