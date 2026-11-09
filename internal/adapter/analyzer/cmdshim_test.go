package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsCmdShim(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{`C:\Users\me\AppData\Roaming\npm\pi.cmd`, true},
		{`C:\Users\me\AppData\Roaming\npm\PI.CMD`, true},
		{`C:\tools\claude.bat`, true},
		{`C:\tools\claude.exe`, false},
		{"/usr/local/bin/pi", false},
		{"/usr/local/bin/claude.js", false},
		{"pi", false},
	}
	for _, tc := range tests {
		if got := isCmdShim(tc.path); got != tc.want {
			t.Errorf("isCmdShim(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestExecCLIRunsACmdShim is the end-to-end half of the shim rule: rendering a
// command line is unit-tested below, but only actually starting the batch file
// proves the cmd.exe invocation works and that a multi-line prompt survives as
// a single line.
func TestExecCLIRunsACmdShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch-file shims only exist on Windows")
	}
	shim := filepath.Join(t.TempDir(), "probe.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\necho %*\r\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	cmd := execCLI(context.Background(), shim, []string{"-p", "line one\nline two"})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("execCLI(cmd shim) error = %v, output = %q", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "-p") {
		t.Errorf("shim output = %q, want the arguments to reach it", got)
	}
	if !strings.Contains(got, "line one line two") {
		t.Errorf("shim output = %q, want the multi-line prompt collapsed to one line", got)
	}
}

// TestExecCLIRunsAnExecutableDirectly keeps the non-shim path honest: anything
// that is not a batch file must be started directly, with its arguments
// untouched.
func TestExecCLIRunsAnExecutableDirectly(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only shim routing")
	}
	cmd := execCLI(context.Background(), os.Getenv("COMSPEC"), []string{"/c", "echo direct"})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("execCLI(exe) error = %v", err)
	}
	if !strings.Contains(string(out), "direct") {
		t.Errorf("execCLI(exe) output = %q, want %q", out, "direct")
	}
}

func TestWindowsCommandLine(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		args   []string
		want   string
	}{
		{
			name:   "quotes every token",
			binary: `C:\npm\pi.cmd`,
			args:   []string{"-p", "hello", "--no-session"},
			want:   `"C:\npm\pi.cmd" "-p" "hello" "--no-session"`,
		},
		{
			name:   "binary only",
			binary: `C:\npm\pi.cmd`,
			args:   nil,
			want:   `"C:\npm\pi.cmd"`,
		},
		{
			// A raw newline would end the cmd.exe command; the rest of the
			// prompt would vanish.
			name:   "newlines collapse to spaces",
			binary: "shim.cmd",
			args:   []string{"line one\nline two\r\nline three"},
			want:   `"shim.cmd" "line one line two line three"`,
		},
		{
			// The prompt carries a JSON schema, so embedded quotes are normal.
			name:   "embedded quotes are escaped for the C runtime",
			binary: "shim.cmd",
			args:   []string{`{"topics": []}`},
			want:   `"shim.cmd" "{\"topics\": []}"`,
		},
		{
			name:   "shell metacharacters stay inside the quotes",
			binary: "shim.cmd",
			args:   []string{"a & b | c > d < e ^ f"},
			want:   `"shim.cmd" "a & b | c > d < e ^ f"`,
		},
		{
			name:   "empty argument is preserved",
			binary: "shim.cmd",
			args:   []string{""},
			want:   `"shim.cmd" ""`,
		},
		{
			name:   "tabs are kept",
			binary: "shim.cmd",
			args:   []string{"a\tb"},
			want:   `"shim.cmd" "a	b"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowsCommandLine(tc.binary, tc.args); got != tc.want {
				t.Errorf("windowsCommandLine(%q, %v) = %q, want %q", tc.binary, tc.args, got, tc.want)
			}
		})
	}
}
