package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/library"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/types"
)

// newTmpConfig writes a config.yaml resolving inbox/library/state under a
// fresh temp dir, so a full run() invocation never touches the real
// project's directories.
func newTmpConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := "inbox: " + filepath.Join(dir, "inbox") + "\n" +
		"library: " + filepath.Join(dir, "library") + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	return path
}

func TestRunHelpAndVersion(t *testing.T) {
	if got := run([]string{"--help"}); got != 0 {
		t.Errorf("run(--help) = %d, want 0", got)
	}
	if got := run([]string{"-h"}); got != 0 {
		t.Errorf("run(-h) = %d, want 0", got)
	}
	if got := run([]string{"--version"}); got != 0 {
		t.Errorf("run(--version) = %d, want 0", got)
	}
}

func TestRunEmptyAndUnknownCommand(t *testing.T) {
	if got := run(nil); got != 2 {
		t.Errorf("run(nil) = %d, want 2", got)
	}
	if got := run([]string{"frobnicate"}); got != 2 {
		t.Errorf("run(frobnicate) = %d, want 2", got)
	}
}

func TestRunParseError(t *testing.T) {
	if got := run([]string{"--nope"}); got != 2 {
		t.Errorf("run(--nope) = %d, want 2", got)
	}
}

func TestRunInitRejectsMock(t *testing.T) {
	// init --mock must be rejected before ever touching the interactive
	// wizard (real or line-based), which would otherwise try to read
	// os.Stdin.
	if got := run([]string{"init", "--mock"}); got != 2 {
		t.Errorf("run(init --mock) = %d, want 2", got)
	}
}

func TestRunProcessRequiresFile(t *testing.T) {
	if got := run([]string{"process", "--mock"}); got != 2 {
		t.Errorf("run(process --mock) = %d, want 2", got)
	}
}

func TestRunProcessMissingFile(t *testing.T) {
	cfgPath := newTmpConfig(t)
	missing := filepath.Join(filepath.Dir(cfgPath), "no-such-video.mkv")
	if got := run([]string{"process", "--mock", "--config", cfgPath, missing}); got != 1 {
		t.Errorf("run(process, missing file) = %d, want 1", got)
	}
}

func TestRunProcessMockHappyPath(t *testing.T) {
	cfgPath := newTmpConfig(t)
	video := filepath.Join(filepath.Dir(cfgPath), "meeting.mkv")
	if err := os.WriteFile(video, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if got := run([]string{"process", "--mock", "--config", cfgPath, video}); got != 0 {
		t.Errorf("run(process --mock) = %d, want 0", got)
	}

	notes, _ := filepath.Glob(filepath.Join(filepath.Dir(cfgPath), "library", "meetings", "*.md"))
	if len(notes) != 1 {
		t.Errorf("meeting notes = %v, want exactly one written note", notes)
	}
}

func TestRunProcessConfigLoadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("analyzer_backend: not-a-real-backend\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	video := filepath.Join(dir, "meeting.mkv")
	if err := os.WriteFile(video, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if got := run([]string{"process", "--mock", "--config", cfgPath, video}); got != 1 {
		t.Errorf("run(process, bad config) = %d, want 1", got)
	}
}

func TestRunProcessPropagatesPipelineFailure(t *testing.T) {
	dir := t.TempDir()
	// A plain file where the library root should be forces
	// pipeline.ProcessVideo to fail even with the deterministic mock
	// backends, exercising runPipeline's failure path.
	libBlocker := filepath.Join(dir, "library")
	if err := os.WriteFile(libBlocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("inbox: "+filepath.Join(dir, "inbox")+"\nlibrary: "+libBlocker+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	video := filepath.Join(dir, "meeting.mkv")
	if err := os.WriteFile(video, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if got := run([]string{"process", "--mock", "--config", cfgPath, video}); got != 1 {
		t.Errorf("run(process, blocked library) = %d, want 1", got)
	}
}

func TestRunProcessWithoutMockRequiresAPIKey(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "")
	cfgPath := newTmpConfig(t)
	video := filepath.Join(filepath.Dir(cfgPath), "meeting.mkv")
	if err := os.WriteFile(video, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if got := run([]string{"process", "--config", cfgPath, video}); got != 2 {
		t.Errorf("run(process, no API key) = %d, want 2", got)
	}
}

func TestRunReconcileConfigLoadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("analyzer_backend: not-a-real-backend\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if got := run([]string{"reconcile", "--config", cfgPath}); got != 1 {
		t.Errorf("run(reconcile, bad config) = %d, want 1", got)
	}
}

func TestRunReconcileLoggingInitError(t *testing.T) {
	cfgPath := newTmpConfig(t)
	dir := filepath.Dir(cfgPath)
	// patro.log already exists as a directory, so logging.Init's OpenFile
	// fails.
	if err := os.MkdirAll(filepath.Join(dir, "patro.log"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if got := run([]string{"reconcile", "--config", cfgPath}); got != 1 {
		t.Errorf("run(reconcile, log path is a dir) = %d, want 1", got)
	}
}

func TestRunReconcileEmptyLibraryBuildsIndexesAndExitsClean(t *testing.T) {
	cfgPath := newTmpConfig(t)
	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(filepath.Join(dir, "library", "topics"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	if got := run([]string{"reconcile", "--mock", "--config", cfgPath}); got != 0 {
		t.Fatalf("run(reconcile, empty library) = %d, want 0", got)
	}

	if _, err := os.Stat(filepath.Join(dir, ".state", "vectors", "topics.json")); err != nil {
		t.Errorf("vector store not built: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".state", "search-index")); err != nil {
		t.Errorf("search index not built: %v", err)
	}
}

func TestRunReconcileFlaggedTopicNotYetAMatchStaysUntouched(t *testing.T) {
	cfgPath := newTmpConfig(t)
	dir := filepath.Dir(cfgPath)
	topicsDir := filepath.Join(dir, "library", "topics")
	if err := os.MkdirAll(topicsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(topicsDir, "target.md"), []byte("# Target\n\nCompletely unrelated content about gardening.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(topicsDir, "flagged-topic.md"), []byte("# Flagged Topic\n\nA totally different subject: rocket propulsion engineering.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	ledgerPath := filepath.Join(dir, ".state", "reconciliation.json")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	ledger := `{"entries":[{"slug":"flagged-topic","name":"Flagged Topic","proposed_slug":"flagged-topic","score":0,"merged":false,"flagged":true,"timestamp":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(ledgerPath, []byte(ledger), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if got := run([]string{"reconcile", "--mock", "--config", cfgPath}); got != 0 {
		t.Fatalf("run(reconcile, flagged topic) = %d, want 0", got)
	}

	// Real cybertron embeddings for two unrelated pieces of text are never
	// close to the 0.90 merge threshold, so the flagged topic must still
	// exist as its own file (not merged away).
	if _, err := os.Stat(filepath.Join(topicsDir, "flagged-topic.md")); err != nil {
		t.Errorf("flagged-topic.md missing: %v (should stay untouched, unrelated content never merges)", err)
	}
}

func TestRunTUIRequiresInteractiveTerminal(t *testing.T) {
	// go test's stdout is not a TTY, so runTUI must bail out before ever
	// launching the real Bubble Tea program.
	cfgPath := newTmpConfig(t)
	if got := run([]string{"run", "tui", "--config", cfgPath}); got != 1 {
		t.Errorf("run(run tui) = %d, want 1 (non-interactive terminal)", got)
	}
}

func TestRunTUIConfigLoadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("analyzer_backend: not-a-real-backend\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if got := run([]string{"run", "tui", "--config", cfgPath}); got != 1 {
		t.Errorf("run(run tui, bad config) = %d, want 1", got)
	}
}

func TestRunWebConfigLoadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("analyzer_backend: not-a-real-backend\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if got := run([]string{"run", "web", "--config", cfgPath}); got != 1 {
		t.Errorf("run(run web, bad config) = %d, want 1", got)
	}
}

func TestRunWebLoggingInitError(t *testing.T) {
	cfgPath := newTmpConfig(t)
	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(filepath.Join(dir, "library"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	// patro.log already exists as a directory, so logging.Init's OpenFile
	// fails.
	if err := os.MkdirAll(filepath.Join(dir, "patro.log"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	if got := run([]string{"run", "web", "--config", cfgPath}); got != 1 {
		t.Errorf("run(run web, log path is a dir) = %d, want 1", got)
	}
}

func TestRunProcessLoggingInitError(t *testing.T) {
	cfgPath := newTmpConfig(t)
	dir := filepath.Dir(cfgPath)
	video := filepath.Join(dir, "meeting.mkv")
	if err := os.WriteFile(video, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "patro.log"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	if got := run([]string{"process", "--mock", "--config", cfgPath, video}); got != 1 {
		t.Errorf("run(process, log path is a dir) = %d, want 1", got)
	}
}

func TestRunWebLibraryNotFound(t *testing.T) {
	cfgPath := newTmpConfig(t)
	// newTmpConfig points library at a directory that is never created.
	if got := run([]string{"run", "web", "--config", cfgPath}); got != 1 {
		t.Errorf("run(run web, missing library) = %d, want 1", got)
	}
}

func TestRunWebPortAlreadyInUse(t *testing.T) {
	cfgPath := newTmpConfig(t)
	if err := os.MkdirAll(filepath.Join(filepath.Dir(cfgPath), "library"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}

	// Bind the port ourselves first so ListenAndServe inside runWeb fails
	// immediately with "address already in use", exercising its error path
	// without needing to signal a graceful shutdown.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error = %v", err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	got := run([]string{"run", "web", "--config", cfgPath, "--port", strconv.Itoa(port)})
	if got != 1 {
		t.Errorf("run(run web, port in use) = %d, want 1", got)
	}
}

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantCommand string
		wantFile    string
		wantPort    int
		wantConfig  string
		wantMock    bool
		wantAll     bool
		wantDryRun  bool
		wantDate    string
		wantErr     bool
	}{
		{
			name:        "reconcile",
			args:        []string{"reconcile", "--config", "/etc/patro.yaml"},
			wantCommand: "reconcile", wantPort: defaultWebPort,
			wantConfig: "/etc/patro.yaml",
		},
		{
			name: "historical dry run flags after command", args: []string{"reconcile", "--all", "--dry-run"},
			wantCommand: "reconcile", wantPort: defaultWebPort, wantAll: true, wantDryRun: true,
		},
		{
			name: "historical dry run flags before command", args: []string{"--dry-run", "--all", "reconcile"},
			wantCommand: "reconcile", wantPort: defaultWebPort, wantAll: true, wantDryRun: true,
		},
		{
			name:        "reconcile mock",
			args:        []string{"reconcile", "--mock"},
			wantCommand: "reconcile", wantPort: defaultWebPort,
			wantMock: true,
		},
		{
			name:        "run tui",
			args:        []string{"run", "tui"},
			wantCommand: "run", wantFile: "tui", wantPort: defaultWebPort,
		},
		{
			name:        "run tui with config",
			args:        []string{"run", "tui", "--config", "/etc/patro.yaml"},
			wantCommand: "run", wantFile: "tui", wantPort: defaultWebPort,
			wantConfig: "/etc/patro.yaml",
		},
		{
			name:        "flags before the subcommand",
			args:        []string{"--config=/tmp/c.yaml", "run", "tui"},
			wantCommand: "run", wantFile: "tui", wantPort: defaultWebPort,
			wantConfig: "/tmp/c.yaml",
		},
		{
			name:        "run web with port",
			args:        []string{"run", "web", "--port", "9"},
			wantCommand: "run", wantFile: "web", wantPort: 9,
		},
		{
			name:        "process with mock",
			args:        []string{"process", "--mock", "/tmp/a.mkv"},
			wantCommand: "process", wantFile: "/tmp/a.mkv", wantPort: defaultWebPort,
			wantMock: true,
		},
		{name: "port out of range", args: []string{"--port", "0"}, wantErr: true},
		{name: "port not a number", args: []string{"--port", "abc"}, wantErr: true},
		{name: "config without a value", args: []string{"--config"}, wantErr: true},
		{name: "port without a value", args: []string{"--port"}, wantErr: true},
		{name: "unknown flag", args: []string{"--nope"}, wantErr: true},
		{name: "too many positionals", args: []string{"run", "web", "extra"}, wantErr: true},
		{
			name:        "regenerate with date and mock",
			args:        []string{"regenerate", "notes.txt", "--date", "2026-01-01", "--mock"},
			wantCommand: "regenerate", wantFile: "notes.txt", wantPort: defaultWebPort,
			wantDate: "2026-01-01", wantMock: true,
		},
		{
			name:        "regenerate with --date=",
			args:        []string{"regenerate", "notes.txt", "--date=2026-01-01"},
			wantCommand: "regenerate", wantFile: "notes.txt", wantPort: defaultWebPort,
			wantDate: "2026-01-01",
		},
		{name: "date without a value", args: []string{"regenerate", "notes.txt", "--date"}, wantErr: true},
		{name: "invalid date, single-digit month/day", args: []string{"regenerate", "notes.txt", "--date", "2026-8-4"}, wantErr: true},
		{name: "invalid date, day out of range", args: []string{"regenerate", "notes.txt", "--date", "2026-02-31"}, wantErr: true},
		{name: "invalid date, wrong layout", args: []string{"regenerate", "notes.txt", "--date", "04/08/2026"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseArgs(%q) = %+v, want an error", tc.args, opts)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs(%q): %v", tc.args, err)
			}
			if opts.command != tc.wantCommand {
				t.Errorf("command = %q, want %q", opts.command, tc.wantCommand)
			}
			if opts.file != tc.wantFile {
				t.Errorf("file = %q, want %q", opts.file, tc.wantFile)
			}
			if opts.port != tc.wantPort {
				t.Errorf("port = %d, want %d", opts.port, tc.wantPort)
			}
			if opts.configPath != tc.wantConfig {
				t.Errorf("configPath = %q, want %q", opts.configPath, tc.wantConfig)
			}
			if opts.mock != tc.wantMock {
				t.Errorf("mock = %v, want %v", opts.mock, tc.wantMock)
			}
			if opts.all != tc.wantAll || opts.dryRun != tc.wantDryRun {
				t.Errorf("all/dryRun = %v/%v, want %v/%v", opts.all, opts.dryRun, tc.wantAll, tc.wantDryRun)
			}
			if opts.date != tc.wantDate {
				t.Errorf("date = %q, want %q", opts.date, tc.wantDate)
			}
		})
	}
}

func TestRunRejectsDateOnNonRegenerateCommands(t *testing.T) {
	// Mirrors the --all/--dry-run-only-valid-with-reconcile guard (D6): the
	// rejection must happen before any config load or file I/O.
	if got := run([]string{"process", "file.mkv", "--date", "2026-01-01"}); got != 2 {
		t.Errorf("run(process --date ...) = %d, want 2", got)
	}
	if got := run([]string{"serve", "--date", "2026-01-01"}); got != 2 {
		t.Errorf("run(serve --date ...) = %d, want 2", got)
	}
}

func TestRunRegenerateRequiresFile(t *testing.T) {
	if got := run([]string{"regenerate", "--mock"}); got != 2 {
		t.Errorf("run(regenerate --mock, no file) = %d, want 2", got)
	}
}

func TestRunRegenerateMockOverwritesPriorNote(t *testing.T) {
	cfgPath := newTmpConfig(t)
	libDir := filepath.Join(filepath.Dir(cfgPath), "library")
	lib, err := library.NewLibrary(libDir)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}

	transcriptPath := filepath.Join(lib.TranscriptsDir, "abc123.txt")
	if err := os.WriteFile(transcriptPath, []byte("Speaker A: hello.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	priorPath, err := lib.WriteMeetingNote(
		&types.TranscriptResult{ID: "abc123"},
		&types.AnalysisResult{Title: "Old Title"},
		"orig.mkv", "2026-01-05",
	)
	if err != nil {
		t.Fatalf("WriteMeetingNote setup: %v", err)
	}

	if got := run([]string{"regenerate", "--mock", "--config", cfgPath, transcriptPath}); got != 0 {
		t.Errorf("run(regenerate --mock, overwrite) = %d, want 0", got)
	}

	notes, _ := filepath.Glob(filepath.Join(lib.MeetingsDir, "*.md"))
	if len(notes) != 1 {
		t.Errorf("meeting notes = %v, want exactly one (overwrite, not a new file)", notes)
	}
	content, err := os.ReadFile(priorPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "- **Date:** 2026-01-05") {
		t.Errorf("content = %q, want prior date preserved", content)
	}
	if !strings.Contains(string(content), "- **Source video:** `orig.mkv`") {
		t.Errorf("content = %q, want prior source video preserved", content)
	}
}

func TestRunRegenerateMockNewNoteRequiresDate(t *testing.T) {
	cfgPath := newTmpConfig(t)
	extPath := filepath.Join(filepath.Dir(cfgPath), "external-recording.txt")
	if err := os.WriteFile(extPath, []byte("Speaker A: brand new recording.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := run([]string{"regenerate", "--mock", "--date", "2026-05-01", "--config", cfgPath, extPath}); got != 0 {
		t.Errorf("run(regenerate --mock --date, new note) = %d, want 0", got)
	}

	libDir := filepath.Join(filepath.Dir(cfgPath), "library")
	notes, _ := filepath.Glob(filepath.Join(libDir, "meetings", "2026-05-01-*.md"))
	if len(notes) != 1 {
		t.Errorf("meeting notes = %v, want exactly one 2026-05-01-*.md", notes)
	}
	copies, _ := filepath.Glob(filepath.Join(libDir, "transcripts", "ext-*.txt"))
	if len(copies) != 1 {
		t.Errorf("transcript copies = %v, want exactly one ext-*.txt", copies)
	}
}

func TestRunRegenerateMissingFileExitsOne(t *testing.T) {
	cfgPath := newTmpConfig(t)
	missing := filepath.Join(filepath.Dir(cfgPath), "no-such-transcript.txt")
	if got := run([]string{"regenerate", "--mock", "--config", cfgPath, missing}); got != 1 {
		t.Errorf("run(regenerate, missing file) = %d, want 1", got)
	}
}

func TestRunRegenerateMissingDateNoPriorNoteFailsWithNoFile(t *testing.T) {
	cfgPath := newTmpConfig(t)
	extPath := filepath.Join(filepath.Dir(cfgPath), "no-date-recording.txt")
	if err := os.WriteFile(extPath, []byte("Speaker A: needs a date.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := run([]string{"regenerate", "--mock", "--config", cfgPath, extPath}); got != 1 {
		t.Errorf("run(regenerate, no --date, no prior note) = %d, want 1", got)
	}

	libDir := filepath.Join(filepath.Dir(cfgPath), "library")
	notes, _ := filepath.Glob(filepath.Join(libDir, "meetings", "*"))
	if len(notes) != 0 {
		t.Errorf("meetings dir = %v, want empty (no note written on failure)", notes)
	}
	transcripts, _ := filepath.Glob(filepath.Join(libDir, "transcripts", "*"))
	if len(transcripts) != 0 {
		t.Errorf("transcripts dir = %v, want empty (no copy on date-resolution failure)", transcripts)
	}
}

func TestRunRegenerateLemurRequiresAPIKey(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	contents := "inbox: " + filepath.Join(dir, "inbox") + "\n" +
		"library: " + filepath.Join(dir, "library") + "\n" +
		"analyzer_backend: lemur\n"
	if err := os.WriteFile(cfgPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	transcriptPath := filepath.Join(dir, "transcript.txt")
	if err := os.WriteFile(transcriptPath, []byte("Speaker A: needs lemur.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := run([]string{"regenerate", "--date", "2026-01-01", "--config", cfgPath, transcriptPath}); got != 2 {
		t.Errorf("run(regenerate, lemur, no API key) = %d, want 2", got)
	}
}

func TestRunRegenerateTopicsIndexAndProcessedStateUntouched(t *testing.T) {
	cfgPath := newTmpConfig(t)
	libDir := filepath.Join(filepath.Dir(cfgPath), "library")
	lib, err := library.NewLibrary(libDir)
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}

	topicPath := filepath.Join(lib.TopicsDir, "existing-topic.md")
	topicContent := "# Existing Topic\n\n## 2026-01-01 — Old Meeting\n\nOld content.\n"
	if err := os.WriteFile(topicPath, []byte(topicContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	indexPath := filepath.Join(lib.Root, "index.md")
	indexContent := "# Knowledge library\n\nstale, never rebuilt by regenerate\n"
	if err := os.WriteFile(indexPath, []byte(indexContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stateDir := filepath.Join(filepath.Dir(cfgPath), ".state")
	otherVideo := filepath.Join(filepath.Dir(cfgPath), "other-video.mkv")
	if err := os.WriteFile(otherVideo, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st := state.New(stateDir)
	if err := st.MarkProcessed(otherVideo, "unrelated-id"); err != nil {
		t.Fatalf("MarkProcessed setup: %v", err)
	}
	processedPath := filepath.Join(stateDir, "processed.json")
	processedBefore, err := os.ReadFile(processedPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	extPath := filepath.Join(filepath.Dir(cfgPath), "isolation-check.txt")
	if err := os.WriteFile(extPath, []byte("Speaker A: isolation check.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := run([]string{"regenerate", "--mock", "--date", "2026-04-01", "--config", cfgPath, extPath}); got != 0 {
		t.Fatalf("run(regenerate --mock --date) = %d, want 0", got)
	}

	if got, err := os.ReadFile(topicPath); err != nil || string(got) != topicContent {
		t.Errorf("topic file changed or unreadable: err=%v got=%q want=%q", err, got, topicContent)
	}
	if got, err := os.ReadFile(indexPath); err != nil || string(got) != indexContent {
		t.Errorf("index.md changed or unreadable: err=%v got=%q want=%q", err, got, indexContent)
	}
	if got, err := os.ReadFile(processedPath); err != nil || string(got) != string(processedBefore) {
		t.Errorf("processed.json changed or unreadable: err=%v got=%q want=%q", err, got, processedBefore)
	}
}

func TestValidateReconcileOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    cliOptions
		wantErr string
	}{
		{name: "legacy maintenance", opts: cliOptions{}},
		{name: "preview", opts: cliOptions{all: true, dryRun: true}},
		{name: "dry run alone", opts: cliOptions{dryRun: true}, wantErr: "requires --all"},
		{name: "noninteractive apply", opts: cliOptions{all: true}, wantErr: "use 'patro run tui'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReconcileOptions(&tt.opts)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunSubcommandRejectsUnknownTarget(t *testing.T) {
	// "dashboard" was replaced by "tui"; it must not silently resolve.
	for _, target := range []string{"", "dashboard", "bogus"} {
		if got := runSubcommand(&cliOptions{command: "run", file: target}); got != 2 {
			t.Errorf("runSubcommand(%q) = %d, want 2", target, got)
		}
	}
}

func TestUsageMentionsRunTUI(t *testing.T) {
	if !strings.Contains(usage, "patro run tui") {
		t.Error("usage does not document 'patro run tui'")
	}
	if strings.Contains(usage, "run dashboard") {
		t.Error("usage still documents the removed 'run dashboard'")
	}
}
