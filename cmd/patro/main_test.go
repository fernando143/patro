package main

import (
	"strings"
	"testing"
)

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
