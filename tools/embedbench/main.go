// Command embedbench is a developer-only tool for evaluating patro's
// compiled-in embedding backend(s): dimensionality, embed wall time, and
// cos(A,B) for a submitted pair of texts. It lives in its own Go module so
// it never ships in a patro release and is excluded from the root module's
// `go build/vet/test ./...` and from .goreleaser.yaml (design D11).
//
// Scope note: originally designed as a cross-backend A/B comparison tool
// (3, later 2, embedding backends side by side). Both go-sentex and zerfoo
// were dropped before shipping and cybertron is the only backend that made
// it into the registry (see the topic-reconciliation design doc, D9
// amendment), so embedbench is now a single-backend quality/performance
// report rather than a comparison matrix — see server.go's package doc for
// the report itself. It still iterates purely through
// embed.Available()/embed.New(name), so it becomes a real A/B tool again
// automatically if a second backend is ever registered; nothing here
// special-cases "cybertron" beyond what the registry already provides.
//
// This file previously held only the Unit 1a compile-verify spike: proving
// that a nested module rooted here, with its own go.mod and a
// `replace github.com/fernando143/patro => ../..` directive, can import
// internal/embed across the module boundary. Go's internal-package rule is
// enforced by import-path prefix, not module boundary: the importer
// github.com/fernando143/patro/tools/embedbench sits under the
// github.com/fernando143/patro prefix that owns internal/embed, so the
// import is legal despite tools/embedbench being a separate module. Unit 2
// replaces that spike with the real form+server.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// defaultPort is the loopback-only port embedbench listens on by default.
const defaultPort = 8766

// listenAddr returns the address embedbench binds to for the given port.
// It is hardcoded to the 127.0.0.1 loopback host — never "0.0.0.0" or an
// empty host — because embedbench is a developer-only tool with no
// authentication and no write path into the knowledge library (threat
// matrix: dev-tool network exposure).
func listenAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func main() {
	os.Exit(run(os.Args[1:]))
}

// run starts the embedbench server and blocks until SIGINT/SIGTERM, then
// shuts it down gracefully. Mirrors cmd/patro/main.go's runWeb.
func run(args []string) int {
	fs := flag.NewFlagSet("embedbench", flag.ContinueOnError)
	port := fs.Int("port", defaultPort, "loopback port to serve the embedbench form on")
	acceptance := fs.Bool("acceptance", false, "run the deterministic 5 warmup + 30 run acceptance gate")
	manifest := fs.String("manifest", "testdata/corpus-manifest.json", "authoritative corpus manifest")
	calibration := fs.Bool("calibration", false, "emit separate directed and symmetric calibration profiles")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *acceptance {
		return runAcceptance(*manifest)
	}
	if *calibration {
		return runCalibration(*manifest)
	}

	addr := listenAddr(*port)
	server := &http.Server{Addr: addr, Handler: NewServer()}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	fmt.Printf("embedbench serving http://%s (Ctrl+C to stop)\n", addr)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "embedbench: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "embedbench: shutdown: %v\n", err)
			return 1
		}
		return 0
	}
}
