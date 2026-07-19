// Command patro watches an OBS recordings folder, transcribes new videos
// with AssemblyAI and builds a Markdown knowledge library.
//
// Usage:
//
//	patro init [--config PATH]
//	patro serve [--mock] [--config PATH]
//	patro process <file> [--mock] [--config PATH]
//	patro run web [--port N] [--config PATH]
//	patro run dashboard [--config PATH]
//	patro --version
//
// init runs the interactive setup wizard. serve watches the configured
// inbox forever; process handles a single file. run web starts a local,
// on-demand web viewer for the knowledge library; run dashboard opens the
// live status TUI (Ctrl+C / q to stop). --mock skips all AssemblyAI calls
// and uses deterministic fakes so the whole pipeline can be verified
// without an API key.
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
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/pipeline"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/status"
	"github.com/fernando143/patro/internal/tui"
	"github.com/fernando143/patro/internal/watcher"
	"github.com/fernando143/patro/internal/web"

	"golang.org/x/term"
)

// version is overridden by release builds via -X main.version=...
var version = "dev"

const usage = `patro — transcribe OBS recordings into a Markdown knowledge library

Usage:
  patro init [--config PATH]              Run the interactive setup wizard
  patro serve [--mock] [--config PATH]    Watch the inbox and process new recordings forever
  patro process <file> [--mock] [--config PATH]
                                          Process a single video file
  patro run web [--port N] [--config PATH]
                                          Serve the knowledge library locally (Ctrl+C to stop)
  patro run dashboard [--config PATH]     Live synthwave status dashboard (q to quit)
  patro --version                         Print the version and exit

Options:
  --config PATH   Path to config.yaml (default: ./config.yaml or ~/.config/patro/config.yaml)
  --mock          Do not call AssemblyAI; use deterministic fake transcripts/analysis
  --port N        Port for 'run web' (default: 8765)
`

// defaultWebPort is the localhost port used by 'patro run web'.
const defaultWebPort = 8765

// cliOptions holds the parsed command line.
type cliOptions struct {
	configPath  string
	mock        bool
	showVersion bool
	showHelp    bool
	command     string
	// file holds the second positional argument: the video file for
	// "process", or the subcommand name for "run".
	file string
	port int
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

	switch opts.command {
	case "":
		fmt.Fprint(os.Stderr, usage)
		return 2
	case "init":
		if opts.mock {
			fmt.Fprintln(os.Stderr, "patro: --mock is only valid with serve or process")
			return 2
		}
		return runInit(opts.configPath)
	case "serve", "process":
		return runPipeline(opts)
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
		case arg == "--mock":
			opts.mock = true
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
		video := expandPath(opts.file)
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
		logging.Warnf("Cannot write status file (dashboard will be unavailable): %v", err)
	}
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

// parsePort validates a --port value.
func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid --port %q: must be a number between 1 and 65535", value)
	}
	return port, nil
}

// runSubcommand dispatches the "run <target>" commands.
func runSubcommand(opts *cliOptions) int {
	switch opts.file {
	case "web":
		return runWeb(opts)
	case "dashboard":
		return runDashboard(opts)
	case "":
		fmt.Fprintln(os.Stderr, "patro: run requires a target ('web' or 'dashboard')")
		return 2
	default:
		fmt.Fprintf(os.Stderr, "patro: unknown run target %q (expected 'web' or 'dashboard')\n", opts.file)
		return 2
	}
}

// runDashboard launches the live synthwave status dashboard.
func runDashboard(opts *cliOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		return 1
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "patro: run dashboard requires an interactive terminal")
		return 1
	}
	if err := tui.Run(cfg, opts.configPath); err != nil {
		fmt.Fprintf(os.Stderr, "patro: dashboard error: %v\n", err)
		return 1
	}
	return 0
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

	addr := fmt.Sprintf("127.0.0.1:%d", opts.port)
	server := &http.Server{Addr: addr, Handler: web.NewServer(cfg.Library)}

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

// expandPath expands a leading "~" and returns the absolute path.
func expandPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		switch {
		case path == "~":
			path = home
		case strings.HasPrefix(path, "~/"):
			path = filepath.Join(home, path[2:])
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
