package embed

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

type fixtureTokenizer struct {
	identity TokenizerIdentity
}

func (f fixtureTokenizer) Tokenize(ctx context.Context, text string) ([]TokenSpan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var tokens []TokenSpan
	runes := []rune(text)
	start := -1
	for i, r := range runes {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			if start >= 0 {
				tokens = append(tokens, TokenSpan{String: string(runes[start:i]), Start: start, End: i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		tokens = append(tokens, TokenSpan{String: string(runes[start:]), Start: start, End: len(runes)})
	}
	return tokens, nil
}

func (f fixtureTokenizer) Identity() TokenizerIdentity { return f.identity }
func (f fixtureTokenizer) MaxPositions() uint32        { return 512 }

type fixtureEncoder struct {
	identity ModelIdentity
	calls    []string
	cancel   context.CancelFunc
}

func (f *fixtureEncoder) EncodeWindow(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.calls = append(f.calls, text)
	if f.cancel != nil && len(f.calls) == 1 {
		f.cancel()
	}
	return []float32{1, 0}, nil
}

func (f *fixtureEncoder) Identity() ModelIdentity { return f.identity }

func fixtureIdentity() (TokenizerIdentity, ModelIdentity) {
	return TokenizerIdentity{ConfigSHA256: strings.Repeat("a", 64), VocabSHA256: strings.Repeat("b", 64)}, ModelIdentity{
		Backend: "fixture", ModelID: "fixture-model", ModelVersion: "fixture-v1",
		ModelWeightsSHA256: strings.Repeat("c", 64), Dimension: 2,
	}
}

func TestRepresentObserved609PositionDocumentLosslessly(t *testing.T) {
	tokenizerIdentity, modelIdentity := fixtureIdentity()
	var words []string
	for i := 0; i < 607; i++ {
		switch i {
		case 0:
			words = append(words, "BEGIN")
		case 303:
			words = append(words, "MIDDLE")
		case 606:
			words = append(words, "END")
		default:
			words = append(words, "token")
		}
	}
	doc := Document{ID: "fixture-609", Text: "# Fixture\n\n" + strings.Join(words, " ")}
	encoder := &fixtureEncoder{identity: modelIdentity}
	representer := NewRepresenter(fixtureTokenizer{identity: tokenizerIdentity}, encoder)

	got, err := representer.Represent(context.Background(), doc)
	if err != nil {
		t.Fatalf("Represent() error: %v", err)
	}
	var content []Chunk
	for _, chunk := range got.Chunks {
		if chunk.Kind == "content" {
			content = append(content, chunk)
		}
	}
	if len(content) < 2 {
		t.Fatalf("content chunks = %d, want multiple chunks", len(content))
	}
	for i, chunk := range content {
		if chunk.TokenCount > 510 {
			t.Errorf("content chunk %d token_count = %d, want <= 510", i, chunk.TokenCount)
		}
		if i > 0 && chunk.Overlap != 32 {
			t.Errorf("content chunk %d overlap = %d, want 32", i, chunk.Overlap)
		}
	}
	if len(encoder.calls) != len(got.Chunks) {
		t.Fatalf("encoder calls = %d, chunks = %d", len(encoder.calls), len(got.Chunks))
	}
	for i, call := range encoder.calls {
		if got := len(strings.Fields(call)) + 2; got > 512 {
			t.Errorf("encoder call %d has %d positions, want <= 512", i, got)
		}
	}
	joined := strings.Join(encoder.calls, " ")
	for _, marker := range []string{"BEGIN", "MIDDLE", "END"} {
		if !strings.Contains(joined, marker) {
			t.Errorf("encoder calls do not contain marker %q", marker)
		}
	}
	if got.SourceHash == "" || got.RepresentationFingerprint == "" {
		t.Fatal("representation identity is incomplete")
	}
}

func TestChunkMarkdownIsDeterministicAndUTF8Safe(t *testing.T) {
	tokens := fixtureTokenizer{}
	text := "## Section\n\nparagraph with café and 日本語\n\n- list item\n\n```go\n日本語\n```\n"
	first, err := chunkMarkdown(context.Background(), text, mustTokens(t, tokens, text), 64)
	if err != nil {
		t.Fatalf("chunkMarkdown() error: %v", err)
	}
	second, err := chunkMarkdown(context.Background(), text, mustTokens(t, tokens, text), 64)
	if err != nil {
		t.Fatalf("second chunkMarkdown() error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("chunkMarkdown() is not deterministic")
	}
	for i, chunk := range first {
		if chunk.SourceStartRune < 0 || chunk.SourceEndRune > len([]rune(text)) || chunk.SourceStartRune >= chunk.SourceEndRune {
			t.Fatalf("chunk %d has invalid rune span: %#v", i, chunk)
		}
		if !utf8.ValidString(string([]rune(text)[chunk.SourceStartRune:chunk.SourceEndRune])) {
			t.Fatalf("chunk %d does not preserve valid UTF-8", i)
		}
		if chunk.TokenCount > 62 {
			t.Errorf("chunk %d token_count = %d, want <= 62 payload tokens", i, chunk.TokenCount)
		}
		if i > 0 && chunk.Overlap != 32 {
			t.Errorf("chunk %d overlap = %d, want 32", i, chunk.Overlap)
		}
	}
}

func mustTokens(t *testing.T, tokenizer ExactTokenizer, text string) []TokenSpan {
	t.Helper()
	tokens, err := tokenizer.Tokenize(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func TestIdentityCanonicalBytesAndFingerprintAreStableAndSensitive(t *testing.T) {
	tokenizerIdentity, modelIdentity := fixtureIdentity()
	one := RepresentationIdentityFrom(tokenizerIdentity, modelIdentity)
	two := RepresentationIdentityFrom(tokenizerIdentity, modelIdentity)
	firstJSON, err := CanonicalIdentityJSON(one)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := CanonicalIdentityJSON(two)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) || Fingerprint(one) != Fingerprint(two) {
		t.Fatal("equal identities are not byte-stable")
	}
	for name, mutate := range map[string]func(*ModelIdentity){
		"weights":       func(m *ModelIdentity) { m.ModelWeightsSHA256 = strings.Repeat("d", 64) },
		"dimension":     func(m *ModelIdentity) { m.Dimension++ },
		"model-version": func(m *ModelIdentity) { m.ModelVersion = "fixture-v2" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := modelIdentity
			mutate(&changed)
			if Fingerprint(RepresentationIdentityFrom(tokenizerIdentity, changed)) == Fingerprint(one) {
				t.Fatal("identity mutation did not change fingerprint")
			}
		})
	}
}

func TestRepresentCancellationReturnsNoPartialRepresentation(t *testing.T) {
	tokenizerIdentity, modelIdentity := fixtureIdentity()
	ctx, cancel := context.WithCancel(context.Background())
	encoder := &fixtureEncoder{identity: modelIdentity, cancel: cancel}
	representer := NewRepresenter(fixtureTokenizer{identity: tokenizerIdentity}, encoder)
	_, err := representer.Represent(ctx, Document{ID: "cancelled", Text: strings.Repeat("token ", 700)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Represent() error = %v, want context.Canceled", err)
	}
	if len(encoder.calls) != 1 {
		t.Fatalf("encoder calls = %d, want 1 after cancellation", len(encoder.calls))
	}
}

func TestRepresentRejectsInvalidVector(t *testing.T) {
	tokenizerIdentity, modelIdentity := fixtureIdentity()
	encoder := &invalidVectorEncoder{identity: modelIdentity}
	representer := NewRepresenter(fixtureTokenizer{identity: tokenizerIdentity}, encoder)
	if _, err := representer.Represent(context.Background(), Document{ID: "invalid", Text: "one two"}); err == nil {
		t.Fatal("Represent() accepted a non-unit vector")
	}
}

type invalidVectorEncoder struct{ identity ModelIdentity }

func (e *invalidVectorEncoder) EncodeWindow(context.Context, string) ([]float32, error) {
	return []float32{float32(math.Inf(1)), 0}, nil
}
func (e *invalidVectorEncoder) Identity() ModelIdentity { return e.identity }
