package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
		wantErr     bool
	}{
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
