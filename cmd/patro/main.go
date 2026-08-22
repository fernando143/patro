// Command patro watches an OBS recordings folder, transcribes new videos
// with AssemblyAI and builds a Markdown knowledge library.
//
// Usage:
//
//	patro init [--config PATH]
//	patro serve [--mock] [--config PATH]
//	patro process <file> [--mock] [--config PATH]
//	patro regenerate <transcript-file> [--date YYYY-MM-DD] [--mock] [--config PATH]
//	patro reconcile [--mock] [--config PATH]
//	patro reconcile --all --dry-run [--config PATH]
//	patro run web [--port N] [--config PATH]
//	patro run tui [--config PATH]
//	patro --version
//
// init runs the interactive setup wizard. serve watches the configured
// inbox forever; process handles a single file. regenerate re-runs only the
// analyzer over an already-transcribed file and rewrites its meeting note
// in place (or writes a new one, given --date, when no note exists yet for
// that transcript) — it never touches topics, the index, or dedup state.
// reconcile ensures the vector/search indexes are up to date (rebuilding
// them if missing or stale) and then re-attempts reconciliation for every
// topic flagged during ingestion. run web starts a local, on-demand web
// viewer for the knowledge library; run tui opens the menu: the live status
// dashboard and settings (q / Ctrl+C to quit). --mock skips all AssemblyAI
// calls and uses deterministic fakes so the whole pipeline can be verified
// without an API key; for reconcile it additionally guarantees no
// gray-zone-reconciliation subprocess is ever spawned.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/library"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/migration"
	"github.com/fernando143/patro/internal/pipeline"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/setup"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/status"
	"github.com/fernando143/patro/internal/tui"
	"github.com/fernando143/patro/internal/vectors"
	"github.com/fernando143/patro/internal/watcher"
	"github.com/fernando143/patro/internal/web"

	"golang.org/x/term"

	"github.com/fernando143/patro/internal/layout"

	"github.com/fernando143/patro/internal/analyzer/backend"
)

// version is overridden by release builds via -X main.version=...
var version = "dev"

const usage = `patro — transcribe OBS recordings into a Markdown knowledge library

Usage:
  patro init [--config PATH]              Run the interactive setup wizard
  patro serve [--mock] [--config PATH]    Watch the inbox and process new recordings forever
  patro process <file> [--mock] [--config PATH]
                                          Process a single video file
  patro regenerate <transcript-file> [--date YYYY-MM-DD] [--mock] [--config PATH]
                                          Re-analyze an existing transcript and rewrite
                                          its meeting note, without touching topics/index
  patro reconcile [--mock] [--config PATH]
                                           Rebuild the vector/search indexes if needed, then
                                           re-attempt reconciliation for flagged topics
  patro reconcile --all --dry-run [--config PATH]
                                           Preview historical topic merge proposals (read-only)
  patro run web [--port N] [--config PATH]
                                          Serve the knowledge library locally (Ctrl+C to stop)
  patro run tui [--config PATH]           Menu: live status dashboard and settings
  patro --version                         Print the version and exit

Options:
  --config PATH   Path to config.yaml (default: ~/.config/patro/config.yaml)
  --mock          Do not call AssemblyAI; use deterministic fake transcripts/analysis
  --all           Inspect every historical topic (reconcile only)
  --dry-run       Print the historical migration plan without changing files
  --port N        Port for 'run web' (default: 8765)
  --date YYYY-MM-DD
                  Date for a new meeting note (regenerate only; required unless a prior
                  note for the transcript already exists)
`

// defaultWebPort is the localhost port used by 'patro run web'.
const defaultWebPort = 8765

// cliOptions holds the parsed command line.
type cliOptions struct {
	configPath  string
	mock        bool
	showVersion bool
	showHelp    bool
	all         bool
	dryRun      bool
	command     string
	// file holds the second positional argument: the video file for
	// "process", the transcript file for "regenerate", or the subcommand
	// name for "run".
	file string
	port int
	// date is --date's validated YYYY-MM-DD value, only meaningful for
	// "regenerate" (D6: rejected on every other command).
	date string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if opts.showHelp {
		fmt.Print(usage)
		return 0
	}
	if opts.showVersion {
		fmt.Printf("patro %s\n", version)
		return 0
	}
	if (opts.all || opts.dryRun) && opts.command != "reconcile" {
		fmt.Fprintln(os.Stderr, "patro: --all and --dry-run are only valid with reconcile")
		return 2
	}
	if opts.date != "" && opts.command != "regenerate" {
		fmt.Fprintln(os.Stderr, "patro: --date is only valid with regenerate")
		return 2
	}

	switch opts.command {
	case "":
		fmt.Fprint(os.Stderr, usage)
		return 2
	case "init":
		if opts.mock {
			fmt.Fprintln(os.Stderr, "patro: --mock is only valid with serve, process or regenerate")
			return 2
		}
		return runInit(opts.configPath)
	case "serve", "process":
		return runPipeline(opts)
	case "regenerate":
		return runRegenerate(opts)
	case "reconcile":
		return runReconcile(opts)
	case "run":
		return runSubcommand(opts)
	default:
		fmt.Fprintf(os.Stderr, "patro: unknown command %q\n", opts.command)
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

// parseArgs scans args by hand so flags are accepted before or after the
// subcommand (no external CLI library).
func parseArgs(args []string) (*cliOptions, error) {
	opts := &cliOptions{port: defaultWebPort}
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config":
			i++
			if i >= len(args) {
				return nil, errors.New("--config requires a value")
			}
			opts.configPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			opts.configPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--port":
			i++
			if i >= len(args) {
				return nil, errors.New("--port requires a value")
			}
			port, err := parsePort(args[i])
			if err != nil {
				return nil, err
			}
			opts.port = port
		case strings.HasPrefix(arg, "--port="):
			port, err := parsePort(strings.TrimPrefix(arg, "--port="))
			if err != nil {
				return nil, err
			}
			opts.port = port
		case arg == "--date":
			i++
			if i >= len(args) {
				return nil, errors.New("--date requires a value")
			}
			date, err := parseDate(args[i])
			if err != nil {
				return nil, err
			}
			opts.date = date
		case strings.HasPrefix(arg, "--date="):
			date, err := parseDate(strings.TrimPrefix(arg, "--date="))
			if err != nil {
				return nil, err
			}
			opts.date = date
		case arg == "--mock":
			opts.mock = true
		case arg == "--all":
			opts.all = true
		case arg == "--dry-run":
			opts.dryRun = true
		case arg == "--version":
			opts.showVersion = true
		case arg == "-h" || arg == "--help":
			opts.showHelp = true
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag: %s", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) > 0 {
		opts.command = positional[0]
	}
	if len(positional) > 1 {
		opts.file = positional[1]
	}
	if len(positional) > 2 {
		return nil, fmt.Errorf("unexpected argument: %s", positional[2])
	}
	return opts, nil
}

// runPipeline implements the serve and process commands.
func runPipeline(opts *cliOptions) int {
	if opts.command == "process" && opts.file == "" {
		fmt.Fprintln(os.Stderr, "patro: process requires a video file")
		return 2
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		return 1
	}
	if err := logging.Init(cfg.LogFile()); err != nil {
		fmt.Fprintf(os.Stderr, "patro: cannot open log file %s: %v\n", cfg.LogFile(), err)
		return 1
	}
	logging.Infof("Loaded config %s (analyzer backend: %s)", cfg.Path, cfg.AnalyzerBackend)

	var transcribeFn pipeline.TranscribeFunc
	var analyzeFn pipeline.AnalyzeFunc
	if opts.mock {
		transcribeFn, analyzeFn = pipeline.MockTranscribe, pipeline.MockAnalyze
		logging.Infof("Mock mode: AssemblyAI will NOT be called")
	} else {
		// Fail fast with a clear message when the key is missing.
		if _, err := cfg.APIKey(); err != nil {
			logging.Errorf("%v", err)
			return 2
		}
		transcribeFn = pipeline.RealTranscribe
		analyzeFn = pipeline.MakeAnalyzeFunc(cfg)
	}

	st := state.New(cfg.StateDir())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if opts.command == "process" {
		video := setup.ExpandPath(opts.file)
		if info, err := os.Stat(video); err != nil || info.IsDir() {
			logging.Errorf("File not found: %s", video)
			return 1
		}
		if _, err := pipeline.ProcessVideo(ctx, video, cfg, st, nil, transcribeFn, analyzeFn); err != nil {
			logging.Errorf("Failed to process %s: %v", video, err)
			return 1
		}
		return 0
	}

	// serve
	tracker, err := status.NewTracker(cfg.StateDir())
	if err != nil {
		logging.Warnf("Cannot write status file (the tui dashboard will be unavailable): %v", err)
	}

	// Startup integrity check (design D10: "Triggers: serve startup + on
	// demand. Never mid-pipeline."). Runs in its own goroutine so a
	// first-run/large-library rebuild never blocks the watcher from coming
	// up and queueing new recordings; reconciliation itself safely degrades
	// (ErrV2NeedsRebuild -> new+flagged) for the rebuild's duration, which is
	// already-designed behavior (Unit 3/4), not new machinery here.
	go func() {
		if err := runMaintenance(ctx, cfg, tracker); err != nil {
			logging.Warnf("startup maintenance: %v", err)
		}
	}()

	w := watcher.New(cfg, func(path string) {
		if _, err := pipeline.ProcessVideo(ctx, path, cfg, st, tracker, transcribeFn, analyzeFn); err != nil {
			logging.Errorf("Failed to process %s: %v", path, err)
			tracker.Fail(path, err.Error())
		}
	})
	w.Tracker = tracker
	if err := w.Run(ctx); err != nil {
		logging.Errorf("%v", err)
		return 1
	}
	return 0
}

// runRegenerate implements "patro regenerate <transcript-file>": it
// re-analyzes an already-transcribed file and (re)writes exactly one
// meeting note, deliberately bypassing topics/index/state (design
// invariants — see pipeline.Regenerate). It mirrors runPipeline's
// structure and exit codes.
func runRegenerate(opts *cliOptions) int {
	if opts.file == "" {
		fmt.Fprintln(os.Stderr, "patro: regenerate requires a transcript file")
		return 2
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		return 1
	}
	if err := logging.Init(cfg.LogFile()); err != nil {
		fmt.Fprintf(os.Stderr, "patro: cannot open log file %s: %v\n", cfg.LogFile(), err)
		return 1
	}
	logging.Infof("Loaded config %s (analyzer backend: %s)", cfg.Path, cfg.AnalyzerBackend)

	var analyzeFn pipeline.AnalyzeFunc
	if opts.mock {
		analyzeFn = pipeline.MockAnalyze
		logging.Infof("Mock mode: AssemblyAI will NOT be called")
	} else {
		// regenerate never uploads to AssemblyAI, so the API key is only
		// required by a hosted analyzer backend, which calls AssemblyAI's
		// LLM (design D8) — unlike runPipeline's unconditional check.
		if b, ok := backend.Get(cfg.AnalyzerBackend); ok && b.Hosted {
			if _, err := cfg.APIKey(); err != nil {
				logging.Errorf("%v", err)
				return 2
			}
		}
		analyzeFn = pipeline.MakeAnalyzeFunc(cfg)
	}

	transcriptPath := setup.ExpandPath(opts.file)
	if info, err := os.Stat(transcriptPath); err != nil || info.IsDir() {
		logging.Errorf("File not found: %s", transcriptPath)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	notePath, err := pipeline.Regenerate(ctx, transcriptPath, opts.date, cfg, analyzeFn)
	if err != nil {
		logging.Errorf("Failed to regenerate %s: %v", transcriptPath, err)
		return 1
	}
	logging.Infof("Done: %s -> %s", transcriptPath, notePath)
	return 0
}

// parsePort validates a --port value.
func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid --port %q: must be a number between 1 and 65535", value)
	}
	return port, nil
}

// parseDate validates a --date value against YYYY-MM-DD (design D2). The
// round-trip re-format check rejects inputs time.Parse would otherwise
// silently accept in a non-canonical shape, e.g. "2026-8-4" or "2026-02-31"
// (which time.Parse normalizes forward into March).
func parseDate(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	t, err := time.Parse("2006-01-02", trimmed)
	if err != nil || t.Format("2006-01-02") != trimmed {
		return "", fmt.Errorf("invalid --date %q: must be in YYYY-MM-DD format", value)
	}
	return trimmed, nil
}

// runSubcommand dispatches the "run <target>" commands.
func runSubcommand(opts *cliOptions) int {
	switch opts.file {
	case "web":
		return runWeb(opts)
	case "tui":
		return runTUI(opts)
	case "":
		fmt.Fprintln(os.Stderr, "patro: run requires a target ('web' or 'tui')")
		return 2
	default:
		fmt.Fprintf(os.Stderr, "patro: unknown run target %q (expected 'web' or 'tui')\n", opts.file)
		return 2
	}
}

// runTUI launches the synthwave menu: status dashboard and settings.
func runTUI(opts *cliOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		return 1
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "patro: run tui requires an interactive terminal")
		return 1
	}
	if err := tui.Run(cfg, cfg.Path); err != nil {
		fmt.Fprintf(os.Stderr, "patro: tui error: %v\n", err)
		return 1
	}
	return 0
}

// runReconcile implements "patro reconcile" (design D8/D10, Unit 7): the
// on-demand maintenance trigger, needed for first-run migration on an
// existing library when no serve process is running, for corruption
// recovery, and for the TUI's future maintenance key (Unit 8, which spawns
// this exact subcommand detached — design D8's "one unified maintenance
// action").
func runReconcile(opts *cliOptions) int {
	if err := validateReconcileOptions(opts); err != nil {
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		return 2
	}
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		return 1
	}
	if opts.all {
		service, err := migration.ConfiguredService(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "patro: %v\n", err)
			return 1
		}
		plan, err := service.BuildPlan(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "patro: historical migration preview: %v\n", err)
			return 1
		}
		printMigrationPlan(plan)
		return 0
	}
	if err := logging.Init(cfg.LogFile()); err != nil {
		fmt.Fprintf(os.Stderr, "patro: cannot open log file %s: %v\n", cfg.LogFile(), err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tracker, err := status.NewTracker(cfg.StateDir())
	if err != nil {
		logging.Warnf("Cannot write status file (the tui dashboard will be unavailable): %v", err)
	}

	if err := runMaintenance(ctx, cfg, tracker); err != nil {
		logging.Errorf("reconcile: %v", err)
		return 1
	}
	return 0
}

func validateReconcileOptions(opts *cliOptions) error {
	if opts.dryRun && !opts.all {
		return errors.New("--dry-run requires --all; use 'patro reconcile --all --dry-run'")
	}
	if opts.all && !opts.dryRun {
		return errors.New("noninteractive historical migration is disabled; use 'patro run tui', open Migrate, and approve merges individually")
	}
	return nil
}

func printMigrationPlan(plan migration.Plan) {
	if len(plan.Proposals) == 0 {
		fmt.Println("Historical migration plan: no topic merges proposed.")
		return
	}
	fmt.Printf("Historical migration plan: %d proposal(s), review required for every merge.\n", len(plan.Proposals))
	for i, p := range plan.Proposals {
		fmt.Printf("\n%d. %s (%s) -> %s (%s)\n", i+1, p.SourceTitle, p.SourceSlug, p.TargetTitle, p.TargetSlug)
		fmt.Printf("   cosine: %.4f\n", p.Score)
		fmt.Printf("   source: %s [%d bytes, %d sections, sha256 %s]\n", p.SourcePath, p.SourceBytes, p.SourceSections, p.SourceHash)
		fmt.Printf("   target: %s [%d bytes, %d sections, sha256 %s]\n", p.TargetPath, p.TargetBytes, p.TargetSections, p.TargetHash)
	}
	fmt.Println("\nDry run only: no knowledge, state, or index files were changed. Use 'patro run tui' -> Migrate to approve or reject each proposal.")
}

// runMaintenance performs ensure-index (rebuild the multi-vector
// representation store and/or the BM25 search index when missing or
// model-version-mismatched — design D10) and then re-attempts reconciliation
// for every flagged topic against the now-current store. Both "patro
// reconcile" (on demand) and serve's own startup integrity check drive
// through this one function, reporting progress through
// tracker.Maintenance* — tracker may be nil (e.g. status.json could not be
// opened), which every Tracker method already tolerates.
//
// The representation store's self-invalidating tag (design D10) is exactly
// NeedsSync(); bleve's BM25 index has no equivalent backend/model concept
// to mismatch, so its ensure-index trigger is simply "the index directory
// did not exist yet" (first run / pre-Unit-7 library) — Rebuild is
// otherwise left alone rather than paying a full re-embed/reindex cost on
// every single serve startup or reconcile call.
func runMaintenance(ctx context.Context, cfg *config.Config, tracker *status.Tracker) error {
	libPaths := layout.Library(cfg.Library)
	topicsDir := libPaths.Topics()
	store, embedder, err := vectors.OpenRepresentationStore(ctx, cfg.StateDir(), cfg.EmbeddingBackend)
	if err != nil {
		return fmt.Errorf("maintenance: %w", err)
	}
	if store.NeedsSync() {
		files, _ := filepath.Glob(filepath.Join(topicsDir, "*.md"))
		tracker.MaintenanceStart(status.PhaseRebuildingIndex, len(files))
		rebuildErr := store.Sync(ctx, topicsDir, embedder)
		tracker.MaintenanceDone()
		if rebuildErr != nil {
			return fmt.Errorf("maintenance: rebuilding vector representations: %w", rebuildErr)
		}
	}

	searchIndexPath := cfg.SearchIndexDir()
	_, statErr := os.Stat(searchIndexPath)
	searchIndexExisted := statErr == nil

	searchIdx, err := searchindex.Open(searchIndexPath)
	if err != nil {
		return fmt.Errorf("maintenance: opening search index: %w", err)
	}
	defer func() {
		if err := searchIdx.Close(); err != nil {
			logging.Warnf("maintenance: closing search index: %v", err)
		}
	}()

	if !searchIndexExisted {
		if err := searchIdx.Rebuild(ctx, topicsDir, libPaths.Meetings()); err != nil {
			return fmt.Errorf("maintenance: rebuilding search index: %w", err)
		}
	}

	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		return fmt.Errorf("maintenance: opening library: %w", err)
	}
	lib.Reconciler = pipeline.NewReconciler(cfg)
	if lib.Reconciler == nil {
		return nil // reconciliation disabled (e.g. unknown embedding backend): nothing more to do
	}

	ledgerPath := layout.State(cfg.StateDir()).Ledger()
	entries, err := library.ReadLedger(ledgerPath)
	if err != nil {
		return fmt.Errorf("maintenance: reading reconciliation ledger: %w", err)
	}
	if flaggedTotal := library.CountFlagged(entries); flaggedTotal > 0 {
		tracker.MaintenanceStart(status.PhaseReconciling, flaggedTotal)
		_, reconcileErr := lib.ReconcileFlagged(ctx, ledgerPath, func(done, _ int) {
			tracker.MaintenanceProgress(done)
		})
		tracker.MaintenanceDone()
		if reconcileErr != nil {
			return fmt.Errorf("maintenance: reconciling flagged topics: %w", reconcileErr)
		}
	}
	return nil
}

// wireSearch configures the BM25 index path and semantic store that power the
// web viewer's read-only /search route. The web handler opens and closes the
// derived BM25 index per request, so a long-running viewer does not hold
// Bleve's lock while `patro process` publishes a replacement. Embedding setup
// is best effort: a failure disables only the semantic leg and leaves BM25
// available. It returns a cleanup func to keep the caller's lifecycle
// contract unchanged.
func wireSearch(srv *web.Server, cfg *config.Config) (closeFn func()) {
	srv.SearchIndexPath = cfg.SearchIndexDir()
	closeFn = func() {}

	store, embedder, err := vectors.OpenRepresentationStore(context.Background(), cfg.StateDir(), cfg.EmbeddingBackend)
	if err != nil {
		logging.Warnf("multi-vector search is disabled: %v", err)
		return closeFn
	}
	srv.MultiVectors = store
	srv.Representer = embedder
	return closeFn
}

// runWeb starts the local knowledge-library web viewer and blocks until
// SIGINT/SIGTERM, then shuts the server down gracefully.
func runWeb(opts *cliOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		return 1
	}
	if err := logging.Init(cfg.LogFile()); err != nil {
		fmt.Fprintf(os.Stderr, "patro: cannot open log file %s: %v\n", cfg.LogFile(), err)
		return 1
	}

	if info, err := os.Stat(cfg.Library); err != nil || !info.IsDir() {
		logging.Errorf("Library directory not found: %s (run patro process/serve first)", cfg.Library)
		return 1
	}

	srv := web.NewServer(cfg.Library)
	closeSearch := wireSearch(srv, cfg)
	defer closeSearch()

	addr := fmt.Sprintf("127.0.0.1:%d", opts.port)
	server := &http.Server{Addr: addr, Handler: srv}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	logging.Infof("Web viewer serving %s at http://%s (Ctrl+C to stop)", cfg.Library, addr)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logging.Errorf("web server: %v", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		logging.Infof("Shutting down web viewer")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logging.Errorf("web shutdown: %v", err)
			return 1
		}
		return 0
	}
}
