package embed

import (
	"context"
	"embed"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/nlpodyssey/cybertron/pkg/models/bert"
	bertenc "github.com/nlpodyssey/cybertron/pkg/tasks/textencoding/bert"
)

// cybertronName is the registry name for the cybertron backend, and the
// value validated by internal/config.validEmbeddingBackends.
const cybertronName = "cybertron"

// cybertronWeights embeds the local, pre-converted spaGO/cybertron
// checkpoint for the all-MiniLM-L6-v2 sentence encoder (384-dim, BERT
// family): the wordpiece vocabulary, tokenizer configuration, and the
// gob-serialized spaGO model produced by cybertron's own PyTorch-to-spaGO
// converter (run once, offline, at development time — never at runtime).
// Because the weights are embedded at build time, loading this backend
// performs zero network calls, satisfying the project's offline-runtime
// invariant.
//
//go:embed weights/cybertron/vocab.txt weights/cybertron/tokenizer_config.json weights/cybertron/spago_model.bin
var cybertronWeights embed.FS

// cybertronWeightFiles are the files LoadTextEncoding expects to find
// together in one directory.
var cybertronWeightFiles = []string{"vocab.txt", "tokenizer_config.json", "spago_model.bin"}

// cybertronEmbedder wraps a loaded cybertron/spaGO BERT text-encoding model.
// Loading walks the model's real transformer stack (self-attention + feed
// forward per layer, see cybertron/pkg/models/bert), unlike a non-contextual
// token-embedding average — this is the property that disqualified the
// zerfoo candidate (Unit 1c).
type cybertronEmbedder struct {
	model *bertenc.TextEncoding
	dim   int
}

// newCybertronEmbedder constructs the cybertron backend by materializing the
// embedded weights into a temporary directory (cybertron's loader is
// file-path based), loading the model, and discarding the temporary files —
// the loaded model holds everything it needs in memory afterward.
func newCybertronEmbedder() (Embedder, error) {
	dir, err := os.MkdirTemp("", "patro-cybertron-weights-*")
	if err != nil {
		return nil, fmt.Errorf("cybertron: create temp weights dir: %w", err)
	}
	defer os.RemoveAll(dir)

	for _, name := range cybertronWeightFiles {
		data, err := cybertronWeights.ReadFile("weights/cybertron/" + name)
		if err != nil {
			return nil, fmt.Errorf("cybertron: read embedded %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, fmt.Errorf("cybertron: write %s: %w", name, err)
		}
	}

	m, err := bertenc.LoadTextEncoding(dir)
	if err != nil {
		return nil, fmt.Errorf("cybertron: load model: %w", err)
	}

	return &cybertronEmbedder{
		model: m,
		dim:   m.Model.Bert.Config.HiddenSize,
	}, nil
}

// Embed returns the mean-pooled, L2-normalized sentence embedding for text.
// Mean pooling (averaging the model's contextual last-hidden-states across
// tokens) is the pooling strategy sentence-embedding models of the
// MiniLM/BERT family are evaluated with; the model itself only guarantees a
// dense vector, so normalization to unit length happens here to satisfy the
// Embedder contract.
func (c *cybertronEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := c.model.Encode(ctx, text, int(bert.MeanPooling))
	if err != nil {
		return nil, fmt.Errorf("cybertron: encode: %w", err)
	}

	data := resp.Vector.Data().F32()
	vec := make([]float32, len(data))
	var sumSquares float64
	for i, v := range data {
		vec[i] = v
		sumSquares += float64(v) * float64(v)
	}
	norm := math.Sqrt(sumSquares)
	if norm > 0 {
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec, nil
}

// Dim returns the hidden size of the loaded model (384 for all-MiniLM-L6-v2).
func (c *cybertronEmbedder) Dim() int { return c.dim }

// Name returns the registry name of this backend.
func (c *cybertronEmbedder) Name() string { return cybertronName }
