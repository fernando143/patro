// Command patro watches an OBS recordings folder, transcribes new videos
// with AssemblyAI and builds a Markdown knowledge library.
//
// Usage:
//
//	patro init [--config PATH]
//	patro serve [--mock] [--config PATH]
//	patro process <file> [--mock] [--config PATH]
//	patro --version
//
// init runs the interactive setup wizard. serve watches the configured
// inbox forever; process handles a single file. --mock skips all AssemblyAI
// calls and uses deterministic fakes so the whole pipeline can be verified
// without an API key.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/pipeline"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/watcher"
)

// version is overridden by release builds via -X main.version=...
var version = "dev"

const usage = `patro — transcribe OBS recordings into a Markdown knowledge library

Usage:
  patro init [--config PATH]              Run the interactive setup wizard
  patro serve [--mock] [--config PATH]    Watch the inbox and process new recordings forever
  patro process <file> [--mock] [--config PATH]
                                          Process a single video file
  patro --version                         Print the version and exit

Options:
  --config PATH   Path to config.yaml (default: ./config.yaml or ~/.config/patro/config.yaml)
  --mock          Do not call AssemblyAI; use deterministic fake transcripts/analysis
`

// cliOptions holds the parsed command line.
type cliOptions struct {
	configPath  string
	mock        bool
	showVersion bool
	showHelp    bool
	command     string
	file        string
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
	default:
		fmt.Fprintf(os.Stderr, "patro: unknown command %q\n", opts.command)
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

// parseArgs scans args by hand so flags are accepted before or after the
// subcommand (no external CLI library).
func parseArgs(args []string) (*cliOptions, error) {
	opts := &cliOptions{}
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
		if _, err := pipeline.ProcessVideo(ctx, video, cfg, st, transcribeFn, analyzeFn); err != nil {
			logging.Errorf("Failed to process %s: %v", video, err)
			return 1
		}
		return 0
	}

	// serve
	w := watcher.New(cfg, func(path string) {
		if _, err := pipeline.ProcessVideo(ctx, path, cfg, st, transcribeFn, analyzeFn); err != nil {
			logging.Errorf("Failed to process %s: %v", path, err)
		}
	})
	if err := w.Run(ctx); err != nil {
		logging.Errorf("%v", err)
		return 1
	}
	return 0
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
