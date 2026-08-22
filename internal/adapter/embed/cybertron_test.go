package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// testCybertronEmbedder loads the real cybertron backend once and shares it
// across sub-tests: loading decodes a ~90MB gob-serialized BERT checkpoint,
// so paying that cost once keeps the suite fast while still exercising the
// real production factory (embed.New("cybertron"), not a shortcut).
var (
	testCybertronOnce     sync.Once
	testCybertronEmbedder Embedder
	testCybertronErr      error
)

func newTestCybertronEmbedder(t *testing.T) Embedder {
	t.Helper()
	testCybertronOnce.Do(func() {
		testCybertronEmbedder, testCybertronErr = New("cybertron")
	})
	if testCybertronErr != nil {
		t.Fatalf("New(\"cybertron\") returned error: %v", testCybertronErr)
	}
	return testCybertronEmbedder
}

func TestCybertronIsRegisteredInProductionRegistry(t *testing.T) {
	found := false
	for _, name := range Available() {
		if name == "cybertron" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Available() = %v, want it to contain %q", Available(), "cybertron")
	}
}

func TestCybertronName(t *testing.T) {
	e := newTestCybertronEmbedder(t)
	if got := e.Name(); got != "cybertron" {
		t.Errorf("Name() = %q, want %q", got, "cybertron")
	}
}

func TestCybertronEmbeddedModelLoads(t *testing.T) {
	e := newTestCybertronEmbedder(t)
	// all-MiniLM-L6-v2 (the embedded checkpoint) has hidden_size = 384.
	if got := e.Dim(); got != 384 {
		t.Errorf("Dim() = %d, want 384", got)
	}
}

func TestCybertronEmbeddedWeightsMatchManifest(t *testing.T) {
	if err := verifyCybertronWeights(cybertronWeights); err != nil {
		t.Fatalf("verifyCybertronWeights() error: %v", err)
	}
}

func TestVerifyCybertronWeightsRejectsLFSPointer(t *testing.T) {
	weights := map[string][]byte{
		"vocab.txt":             []byte("vocabulary"),
		"tokenizer_config.json": []byte(`{"model_max_length": 256}`),
		"spago_model.bin":       []byte("version https://git-lfs.github.com/spec/v1\noid sha256:123\nsize 456\n"),
	}
	manifest := cybertronManifest{
		ModelID:      cybertronModelID,
		ModelVersion: cybertronModelVersion,
		Weights: map[string]string{
			"vocab.txt":             sha256Hex(weights["vocab.txt"]),
			"tokenizer_config.json": sha256Hex(weights["tokenizer_config.json"]),
			"spago_model.bin":       sha256Hex([]byte("hydrated model bytes")),
		},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal(manifest): %v", err)
	}
	files := fstest.MapFS{
		cybertronManifestPath: {Data: manifestJSON},
	}
	for name, data := range weights {
		files["weights/cybertron/"+name] = &fstest.MapFile{Data: data}
	}

	if err := verifyCybertronWeights(files); err == nil {
		t.Fatal("verifyCybertronWeights() accepted an unhydrated Git LFS pointer")
	}
}

func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func TestCybertronRepresentProducesCompleteDocument(t *testing.T) {
	e := newTestCybertronEmbedder(t)
	representation, err := e.Represent(context.Background(), Document{
		ID:   "meeting",
		Text: "# Product roadmap\n\nThe team discussed the next release.",
	})
	if err != nil {
		t.Fatalf("Represent() error: %v", err)
	}
	if representation.DocumentID != "meeting" {
		t.Fatalf("DocumentID = %q, want meeting", representation.DocumentID)
	}
	if len(representation.Chunks) < 2 {
		t.Fatalf("chunks = %d, want title and content vectors", len(representation.Chunks))
	}
	if representation.Dimension != e.Dim() {
		t.Fatalf("Dimension = %d, want %d", representation.Dimension, e.Dim())
	}
}

func TestCybertron609Positions(t *testing.T) {
	e := newTestCybertronEmbedder(t)
	backend, ok := e.(*cybertronEmbedder)
	if !ok {
		t.Fatalf("production cybertron embedder has type %T, want *cybertronEmbedder", e)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "cybertron-609.md"))
	if err != nil {
		t.Fatal(err)
	}
	representation, err := backend.Represent(context.Background(), Document{ID: "cybertron-609", Text: string(data)})
	if err != nil {
		t.Fatalf("Represent(609-position fixture) error: %v", err)
	}
	if len(representation.Chunks) < 2 {
		t.Fatalf("chunks = %d, want multiple chunks", len(representation.Chunks))
	}
	runes := []rune(string(data))
	var joined string
	contentIndex := 0
	for _, chunk := range representation.Chunks {
		if chunk.Kind != "content" {
			continue
		}
		if chunk.TokenCount > 510 {
			t.Errorf("content chunk %d token_count = %d, want <= 510", contentIndex, chunk.TokenCount)
		}
		if contentIndex > 0 && chunk.Overlap != 32 {
			t.Errorf("content chunk %d overlap = %d, want 32", contentIndex, chunk.Overlap)
		}
		joined += string(runes[chunk.SourceStartRune:chunk.SourceEndRune]) + " "
		contentIndex++
	}
	for _, marker := range []string{"BEGIN", "MIDDLE", "END"} {
		if !strings.Contains(joined, marker) {
			t.Errorf("chunk spans do not contain marker %q", marker)
		}
	}
}
