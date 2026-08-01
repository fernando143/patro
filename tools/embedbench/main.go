// Command embedbench is a developer-only tool for comparing patro's
// embedding backends (dim, wall time, cross-backend agreement). It lives in
// its own Go module so it never ships in a patro release and is excluded
// from the root module's `go build/vet/test ./...` and from
// .goreleaser.yaml (design D11).
//
// This file is currently the Unit 1a compile-verify spike only: it proves
// that a nested module rooted here, with its own go.mod and a
// `replace github.com/fernando143/patro => ../..` directive, can import
// internal/embed across the module boundary. Go's internal-package rule is
// enforced by import-path prefix, not by module boundary: the importer
// github.com/fernando143/patro/tools/embedbench sits under the
// github.com/fernando143/patro prefix that owns internal/embed, so the
// import is legal despite tools/embedbench being a separate module.
//
// The actual comparison server (Server, one inlined template.Must page,
// loopback bind, graceful shutdown, mirroring internal/web) is built in
// Unit 2 — out of scope here.
package main

import (
	"fmt"

	"github.com/fernando143/patro/internal/embed"
)

func main() {
	backends := embed.Available()
	fmt.Printf("embedbench: %d compiled-in embedding backend(s): %v\n", len(backends), backends)
}
