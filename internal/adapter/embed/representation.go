package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	ChunkerVersion       = "md-wordpiece-510-478-32-v1"
	NormalizationVersion = "l2-f32-v1"
	RepresentationSchema = 2
	SpecialTokenCount    = 2
	ContinuationOverlap  = 32
)

// TokenSpan is one exact tokenizer output and its source range in runes.
type TokenSpan struct {
	String string
	Start  int
	End    int
}

// TokenizerIdentity identifies the raw tokenizer assets used to create spans.
type TokenizerIdentity struct {
	ConfigSHA256 string
	VocabSHA256  string
}

// ExactTokenizer is the tokenizer contract for lossless representation.
type ExactTokenizer interface {
	Tokenize(context.Context, string) ([]TokenSpan, error)
	Identity() TokenizerIdentity
	MaxPositions() uint32
}

// ModelIdentity identifies the model and representation-producing backend.
type ModelIdentity struct {
	Backend            string
	ModelID            string
	ModelVersion       string
	ModelWeightsSHA256 string
	Dimension          int
}

// WindowEncoder encodes one already-bounded text window.
type WindowEncoder interface {
	EncodeWindow(context.Context, string) ([]float32, error)
	Identity() ModelIdentity
}

// Document is Markdown source to represent.
type Document struct {
	ID   string
	Text string
}

// Chunk is one ordered title or content vector.
type Chunk struct {
	Kind            string    `json:"kind"`
	Ordinal         int       `json:"ordinal"`
	TokenCount      int       `json:"token_count"`
	SourceStartRune int       `json:"source_start_rune"`
	SourceEndRune   int       `json:"source_end_rune"`
	Overlap         int       `json:"overlap"`
	Vector          []float32 `json:"vector"`

	tokenStart int
	tokenEnd   int
}

// Representation is the lossless multi-vector representation of a document.
type Representation struct {
	SchemaVersion             int     `json:"schema_version"`
	DocumentID                string  `json:"document_id"`
	SourceHash                string  `json:"source_hash"`
	Backend                   string  `json:"backend"`
	ModelID                   string  `json:"model_id"`
	ModelVersion              string  `json:"model_version"`
	ModelWeightsSHA256        string  `json:"model_weights_sha256"`
	TokenizerSHA256           string  `json:"tokenizer_sha256"`
	ChunkerVersion            string  `json:"chunker_version"`
	NormalizationVersion      string  `json:"normalization_version"`
	RepresentationFingerprint string  `json:"representation_fingerprint"`
	Dimension                 int     `json:"dimension"`
	Chunks                    []Chunk `json:"chunks"`
}

func (r Representation) Identity() RepresentationIdentity {
	return RepresentationIdentity{
		SchemaVersion: r.SchemaVersion, Backend: r.Backend, ModelID: r.ModelID,
		ModelVersion: r.ModelVersion, ModelWeightsSHA256: r.ModelWeightsSHA256,
		TokenizerSHA256: r.TokenizerSHA256, ChunkerVersion: r.ChunkerVersion,
		NormalizationVersion: r.NormalizationVersion, Dimension: r.Dimension,
	}
}

// RepresentationIdentity is the exact identity payload fingerprinted by v2.
type RepresentationIdentity struct {
	SchemaVersion        int
	Backend              string
	ModelID              string
	ModelVersion         string
	ModelWeightsSHA256   string
	TokenizerSHA256      string
	ChunkerVersion       string
	NormalizationVersion string
	Dimension            int
}

// RepresentationIdentityFrom builds the canonical v2 identity.
func RepresentationIdentityFrom(tokenizer TokenizerIdentity, model ModelIdentity) RepresentationIdentity {
	return RepresentationIdentity{
		SchemaVersion:        RepresentationSchema,
		Backend:              model.Backend,
		ModelID:              model.ModelID,
		ModelVersion:         model.ModelVersion,
		ModelWeightsSHA256:   model.ModelWeightsSHA256,
		TokenizerSHA256:      tokenizer.Fingerprint(),
		ChunkerVersion:       ChunkerVersion,
		NormalizationVersion: NormalizationVersion,
		Dimension:            model.Dimension,
	}
}

// Fingerprint returns the lowercase SHA-256 of the canonical identity JSON.
func Fingerprint(identity RepresentationIdentity) string {
	data, err := CanonicalIdentityJSON(identity)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CanonicalIdentityJSON returns RFC-8785-compatible canonical JSON for the
// string/integer identity fields. encoding/json sorts map keys lexicographically
// and emits the exact primitive forms used by this schema.
func CanonicalIdentityJSON(identity RepresentationIdentity) ([]byte, error) {
	return json.Marshal(map[string]any{
		"backend":               identity.Backend,
		"chunker_version":       identity.ChunkerVersion,
		"dimension":             identity.Dimension,
		"model_id":              identity.ModelID,
		"model_version":         identity.ModelVersion,
		"model_weights_sha256":  identity.ModelWeightsSHA256,
		"normalization_version": identity.NormalizationVersion,
		"schema_version":        identity.SchemaVersion,
		"tokenizer_sha256":      identity.TokenizerSHA256,
	})
}

// Fingerprint returns the canonical tokenizer identity hash required by v2.
func (identity TokenizerIdentity) Fingerprint() string {
	configSum := identity.ConfigSHA256
	vocabSum := identity.VocabSHA256
	data, err := json.Marshal(map[string]string{
		"tokenizer_config_sha256": configSum,
		"vocab_sha256":            vocabSum,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// NewRepresenter creates a document representer using exact tokenizer/model
// boundaries. The production Cybertron adapter is wired in a later task.
func NewRepresenter(tokenizer ExactTokenizer, encoder WindowEncoder) *DocumentRepresenter {
	return &DocumentRepresenter{tokenizer: tokenizer, encoder: encoder}
}

// DocumentRepresenter creates a complete representation transactionally.
type DocumentRepresenter struct {
	tokenizer ExactTokenizer
	encoder   WindowEncoder
}

// Represent tokenizes and encodes a document without returning partial state.
func (r *DocumentRepresenter) Represent(ctx context.Context, document Document) (*Representation, error) {
	if r == nil || r.tokenizer == nil || r.encoder == nil {
		return nil, errors.New("embed: representer is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	title, titleOffset, content, contentOffset := splitRootTitle(document.Text)
	model := r.encoder.Identity()
	if model.Dimension <= 0 {
		return nil, fmt.Errorf("embed: invalid model dimension %d", model.Dimension)
	}
	identity := RepresentationIdentityFrom(r.tokenizer.Identity(), model)
	result := &Representation{
		SchemaVersion:             RepresentationSchema,
		DocumentID:                document.ID,
		SourceHash:                hashString(document.Text),
		Backend:                   model.Backend,
		ModelID:                   model.ModelID,
		ModelVersion:              model.ModelVersion,
		ModelWeightsSHA256:        model.ModelWeightsSHA256,
		TokenizerSHA256:           identity.TokenizerSHA256,
		ChunkerVersion:            identity.ChunkerVersion,
		NormalizationVersion:      identity.NormalizationVersion,
		RepresentationFingerprint: Fingerprint(identity),
		Dimension:                 model.Dimension,
	}

	sections := []struct {
		kind   string
		text   string
		offset int
	}{
		{kind: "title", text: title, offset: titleOffset},
		{kind: "content", text: content, offset: contentOffset},
	}
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(section.text) == "" {
			continue
		}
		tokens, err := r.tokenizer.Tokenize(ctx, section.text)
		if err != nil {
			return nil, fmt.Errorf("embed: tokenize %s: %w", section.kind, err)
		}
		for i := range tokens {
			tokens[i].Start += section.offset
			tokens[i].End += section.offset
		}
		localText := section.text
		localTokens := make([]TokenSpan, len(tokens))
		copy(localTokens, tokens)
		for i := range localTokens {
			localTokens[i].Start -= section.offset
			localTokens[i].End -= section.offset
		}
		chunks, err := chunkMarkdown(ctx, localText, localTokens, int(r.tokenizer.MaxPositions()))
		if err != nil {
			return nil, fmt.Errorf("embed: chunk %s: %w", section.kind, err)
		}
		for i := range chunks {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			chunk := &chunks[i]
			chunk.Kind = section.kind
			chunk.Ordinal = len(result.Chunks)
			chunk.SourceStartRune += section.offset
			chunk.SourceEndRune += section.offset
			windowRunes := []rune(section.text)[chunk.SourceStartRune-section.offset : chunk.SourceEndRune-section.offset]
			vector, err := r.encoder.EncodeWindow(ctx, string(windowRunes))
			if err != nil {
				return nil, fmt.Errorf("embed: encode %s chunk %d: %w", section.kind, i, err)
			}
			chunk.Vector, err = normalizedVector(vector, model.Dimension)
			if err != nil {
				return nil, fmt.Errorf("embed: %s chunk %d: %w", section.kind, i, err)
			}
			result.Chunks = append(result.Chunks, *chunk)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func splitRootTitle(text string) (title string, titleOffset int, content string, contentOffset int) {
	runes := []rune(text)
	lineEnd := 0
	for lineEnd < len(runes) && runes[lineEnd] != '\n' {
		lineEnd++
	}
	line := strings.TrimSuffix(string(runes[:lineEnd]), "\r")
	if strings.HasPrefix(line, "# ") && strings.TrimSpace(line[2:]) != "" {
		title = strings.TrimSpace(line[2:])
		leading := len([]rune(line[2:])) - len([]rune(strings.TrimLeft(line[2:], " \t")))
		titleOffset = 2 + leading
		contentStart := lineEnd
		if contentStart < len(runes) && runes[contentStart] == '\n' {
			contentStart++
		}
		return title, titleOffset, string(runes[contentStart:]), contentStart
	}
	return "", 0, text, 0
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizedVector(vector []float32, dimension int) ([]float32, error) {
	if len(vector) != dimension {
		return nil, fmt.Errorf("vector dimension %d, want %d", len(vector), dimension)
	}
	var norm float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, errors.New("vector contains non-finite value")
		}
		norm += float64(value) * float64(value)
	}
	if norm == 0 {
		return nil, errors.New("vector has zero norm")
	}
	norm = math.Sqrt(norm)
	result := make([]float32, len(vector))
	for i, value := range vector {
		result[i] = float32(float64(value) / norm)
	}
	return result, nil
}

func chunkMarkdown(ctx context.Context, text string, tokens []TokenSpan, maxPositions int) ([]Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxPositions <= SpecialTokenCount {
		return nil, fmt.Errorf("max positions %d leaves no payload", maxPositions)
	}
	if !utf8.ValidString(text) {
		return nil, errors.New("text is not valid UTF-8")
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	for i, token := range tokens {
		if token.Start < 0 || token.End <= token.Start || token.End > len([]rune(text)) || (i > 0 && token.Start < tokens[i-1].End) {
			return nil, fmt.Errorf("invalid token span at index %d", i)
		}
	}
	payload := maxPositions - SpecialTokenCount
	boundaries := preferredBoundaries(text, tokens)
	var chunks []Chunk
	cursor := 0
	for cursor < len(tokens) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		newLimit := payload
		overlap := 0
		if cursor > 0 {
			overlap = ContinuationOverlap
			if overlap > cursor {
				overlap = cursor
			}
			newLimit -= overlap
		}
		end := chooseBoundary(cursor, cursor+newLimit, boundaries)
		if end <= cursor {
			end = minInt(cursor+newLimit, len(tokens))
		}
		start := cursor - overlap
		chunks = append(chunks, Chunk{
			TokenCount:      end - start,
			SourceStartRune: tokens[start].Start,
			SourceEndRune:   tokens[end-1].End,
			Overlap:         overlap,
			tokenStart:      start,
			tokenEnd:        end,
		})
		cursor = end
	}
	return chunks, nil
}

type chunkBoundary struct {
	index    int
	priority int
}

func preferredBoundaries(text string, tokens []TokenSpan) []chunkBoundary {
	runes := []rune(text)
	boundaries := map[int]int{len(tokens): 3}
	lineStart := 0
	for lineStart < len(runes) {
		lineEnd := lineStart
		for lineEnd < len(runes) && runes[lineEnd] != '\n' {
			lineEnd++
		}
		line := strings.TrimSpace(string(runes[lineStart:lineEnd]))
		if lineStart > 0 && strings.HasPrefix(line, "#") {
			addTokenBoundary(boundaries, tokens, lineStart, 0)
		}
		if line != "" && (strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "+") ||
			strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") || strings.HasPrefix(line, "|") ||
			strings.Trim(line, "-_* \t") == "") {
			addTokenBoundary(boundaries, tokens, lineEnd, 1)
		}
		if lineEnd < len(runes) && lineEnd+1 < len(runes) && runes[lineEnd+1] == '\n' {
			addTokenBoundary(boundaries, tokens, lineEnd, 1)
		}
		lineStart = lineEnd + 1
	}
	for i := 1; i < len(tokens); i++ {
		between := string(runes[tokens[i-1].End:tokens[i].Start])
		if strings.HasSuffix(tokens[i-1].String, ".") || strings.HasSuffix(tokens[i-1].String, "!") ||
			strings.HasSuffix(tokens[i-1].String, "?") || strings.ContainsAny(strings.TrimSpace(between), ".?!") {
			if current, exists := boundaries[i]; !exists || current > 2 {
				boundaries[i] = 2
			}
		}
	}
	return sortedBoundaries(boundaries)
}

func addTokenBoundary(boundaries map[int]int, tokens []TokenSpan, runeOffset, priority int) {
	index := 0
	for index < len(tokens) && tokens[index].End <= runeOffset {
		index++
	}
	if index > 0 && (index == len(tokens) || tokens[index].Start >= runeOffset) {
		if current, exists := boundaries[index]; !exists || priority < current {
			boundaries[index] = priority
		}
	}
}

func sortedBoundaries(boundaries map[int]int) []chunkBoundary {
	result := make([]chunkBoundary, 0, len(boundaries))
	for index, priority := range boundaries {
		result = append(result, chunkBoundary{index: index, priority: priority})
	}
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && (result[j].priority < result[j-1].priority ||
			(result[j].priority == result[j-1].priority && result[j].index < result[j-1].index)); j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}

func chooseBoundary(cursor, limit int, boundaries []chunkBoundary) int {
	for priority := 0; priority <= 2; priority++ {
		chosen := 0
		for _, boundary := range boundaries {
			if boundary.priority == priority && boundary.index > cursor && boundary.index <= limit && boundary.index > chosen {
				chosen = boundary.index
			}
		}
		if chosen > 0 {
			return chosen
		}
	}
	return 0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
