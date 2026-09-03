package vectors

import (
	"context"
	"errors"
	"fmt"

	"github.com/fernando143/patro/internal/adapter/embed"
	"github.com/fernando143/patro/internal/platform/layout"
)

// identityDocument is embedded once at startup purely to learn the backend's
// representation identity — dimensionality, model version, chunking — which
// the store stamps into its snapshot so a later run can detect that the
// snapshot was written by a different model and refuse to query it.
//
// Its content is arbitrary but must stay stable: changing the text changes
// the identity, which invalidates every existing snapshot on disk.
var identityDocument = embed.Document{ID: "identity", Text: "# Identity\n\nidentity"}

// OpenRepresentationStore constructs the multi-vector representation store
// for stateDir, backed by the named embedding backend.
//
// It returns the embedder alongside the store because callers need both and
// they must be the same instance: Sync takes the embedder as a separate
// argument, and the web viewer assigns it to its own Representer field. A
// caller handed only the store would have to call embed.New a second time,
// producing an embedder whose identity is not guaranteed to match the one
// stamped into the store — the exact mismatch this constructor exists to
// make impossible.
//
// Failure is always reported as an error and never as a nil store. Callers
// deliberately differ in what they do with it: video processing logs and
// degrades so a missing store can never block a recording, while maintenance
// and migration abort. That policy belongs to the call site, not here.
func OpenRepresentationStore(ctx context.Context, stateDir, backend string) (*V2Store, embed.Embedder, error) {
	embedder, err := embed.New(backend)
	if err != nil {
		return nil, nil, fmt.Errorf("embedding backend unavailable: %w", err)
	}
	sample, err := embedder.Represent(ctx, identityDocument)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot initialize representation identity: %w", err)
	}
	if sample == nil {
		return nil, nil, errors.New("representation backend returned nil identity")
	}
	return NewV2Store(layout.State(stateDir).VectorStore(), sample.Identity(), OSCommitFS{}), embedder, nil
}
