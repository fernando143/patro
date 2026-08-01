// Package embed provides in-binary, pure-Go local embedding generation for
// topic reconciliation and search.
//
// Weights for every compiled-in backend are embedded at build time
// (go:embed), so producing an embedding never requires a network call or a
// runtime secret. Backends are wired into a small explicit registry rather
// than self-registering through init(), mirroring
// internal/config.validAnalyzerBackends: every compiled-in backend is
// visible by reading this one table.
//
// This file ships only the Embedder contract, the registry skeleton, and a
// deterministic no-op double for tests/--mock. Real backend adapters
// (go-sentex, zerfoo, cybertron) are added in later units and register
// themselves in the registry table below; the shape of Embedder, Available
// and New is final as of this unit so adapters can plug in without changing
// callers.
package embed

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
)

// Embedder produces a vector embedding for a piece of text. Implementations
// must return an L2-normalized (unit-length) vector so callers can compute
// cosine similarity as a plain dot product.
type Embedder interface {
	// Embed returns the vector embedding of text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// Dim returns the dimensionality of vectors produced by Embed.
	Dim() int
	// Name returns the backend's registry name.
	Name() string
}

// factory constructs an Embedder on demand, so New never pays the cost of
// loading weights for backends it does not select.
type factory func() (Embedder, error)

// registry is the explicit table of compiled-in backends. It intentionally
// starts empty: adapters register their own entry here in units 1b/1c/1d
// (go-sentex, zerfoo, cybertron respectively). Keeping it a plain map
// literal — not init()-based self-registration — means every compiled-in
// backend is visible by reading this file, matching the
// validAnalyzerBackends/backendChoices precedent elsewhere in the codebase.
var registry = map[string]factory{}

// Available returns the names of every compiled-in backend, sorted for a
// deterministic order. The result is a copy: mutating it cannot corrupt the
// registry.
func Available() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// New constructs the named backend. It returns an error naming the unknown
// backend and listing the available ones when name is not registered, or
// propagates the backend's own construction error otherwise (e.g. failure
// to decode embedded weights).
func New(name string) (Embedder, error) {
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf(
			"unknown embedding backend %q; available: %s",
			name, strings.Join(Available(), ", "),
		)
	}
	e, err := f()
	if err != nil {
		return nil, fmt.Errorf("embedding backend %q: %w", name, err)
	}
	return e, nil
}

// nopEmbedder is a deterministic, dependency-free Embedder used by tests and
// --mock runs. It performs no real inference and is deliberately not part of
// the registry: it is a test double, not a config-selectable
// embedding_backend value.
type nopEmbedder struct {
	dim int
}

// NewNop returns an Embedder that derives a deterministic, unit-length
// pseudo-vector from the FNV hash of its input text. It has no external
// dependency and needs no embedded weights, making it safe to use in tests
// and in --mock pipeline runs.
func NewNop(dim int) Embedder {
	return nopEmbedder{dim: dim}
}

func (n nopEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, n.dim)
	var sumSquares float64
	for i := range vec {
		h := fnv.New64a()
		fmt.Fprintf(h, "%d:%s", i, text)
		// Map the hash into [-1, 1) for a mix of signs.
		v := float64(h.Sum64()%2000001)/1000000.0 - 1.0
		vec[i] = float32(v)
		sumSquares += v * v
	}
	norm := math.Sqrt(sumSquares)
	if norm > 0 {
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec, nil
}

func (n nopEmbedder) Dim() int { return n.dim }

func (n nopEmbedder) Name() string { return "nop" }
