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
// This file ships the multi-vector Embedder contract and the registry. Real
// backend adapters register themselves in the registry table below (see
// cybertron.go).
package embed

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Embedder produces a complete multi-vector representation for a Markdown
// document. Implementations must preserve the document identity and return
// normalized chunk vectors through Representation.
type Embedder interface {
	// Represent returns all title/content chunk vectors for a document.
	Represent(ctx context.Context, document Document) (*Representation, error)
	// Dim returns the dimensionality of chunk vectors produced by Represent.
	Dim() int
	// Name returns the backend's registry name.
	Name() string
}

// factory constructs an Embedder on demand, so New never pays the cost of
// loading weights for backends it does not select.
type factory func() (Embedder, error)

// registry is the explicit table of compiled-in backends. Keeping it a plain
// map literal — not init()-based self-registration — means every compiled-in
// backend is visible by reading this file, matching the
// validAnalyzerBackends/backendChoices precedent elsewhere in the codebase.
//
// go-sentex was dropped (network-dependent weight loading, D9 amendment
// revision 4) and zerfoo was blocked (no contextual-embedding path in its
// public API, see tasks Unit 1c finding) before either was registered here.
// cybertron (Unit 1d) is the only backend that passed verification.
var registry = map[string]factory{
	cybertronName: newCybertronEmbedder,
}

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
