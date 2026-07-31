package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newPrompter(input string) *prompter {
	return &prompter{reader: bufio.NewReader(strings.NewReader(input))}
}

func TestPrompterReadLine(t *testing.T) {
	p := newPrompter("hello world\nsecond line\n")
	if got := p.readLine(); got != "hello world" {
		t.Errorf("readLine() = %q, want %q", got, "hello world")
	}
	if got := p.readLine(); got != "second line" {
		t.Errorf("readLine() = %q, want %q", got, "second line")
	}
}

func TestPrompterAsk(t *testing.T) {
	if got := newPrompter("\n").ask("q", "default-value"); got != "default-value" {
		t.Errorf("ask(blank input) = %q, want the default", got)
	}
	if got := newPrompter("typed-answer\n").ask("q", "default-value"); got != "typed-answer" {
		t.Errorf("ask(typed input) = %q, want %q", got, "typed-answer")
	}
}

func TestPrompterRequiredRetriesUntilNonEmpty(t *testing.T) {
	p := newPrompter("\n\nfinally\n")
	if got := p.required("q", ""); got != "finally" {
		t.Errorf("required() = %q, want %q after two blank retries", got, "finally")
	}
}

func TestPrompterConfirm(t *testing.T) {
	tests := []struct {
		name  string
		input string
		def   bool
		want  bool
	}{
		{"blank uses default true", "\n", true, true},
		{"blank uses default false", "\n", false, false},
		{"yes", "y\n", false, true},
		{"YES uppercase", "YES\n", false, true},
		{"no", "n\n", true, false},
		{"invalid then yes", "maybe\ny\n", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newPrompter(tt.input).confirm("install?", tt.def); got != tt.want {
				t.Errorf("confirm(%q, def=%v) = %v, want %v", tt.input, tt.def, got, tt.want)
			}
		})
	}
}

func TestPrompterPromptPathCreatesMissingFolder(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "new-inbox")
	p := newPrompter(target + "\ny\n")

	got := p.promptPath("inbox", "")
	if got != target {
		t.Errorf("promptPath() = %q, want %q", got, target)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Errorf("promptPath() did not create %s: %v", target, err)
	}
}

func TestPrompterPromptPathAcceptsExistingFolder(t *testing.T) {
	dir := t.TempDir()
	p := newPrompter(dir + "\n")

	if got := p.promptPath("inbox", ""); got != dir {
		t.Errorf("promptPath() = %q, want %q", got, dir)
	}
}

func TestPrompterPromptPathRejectsFileThenAcceptsFolder(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	dir := t.TempDir()
	p := newPrompter(file + "\n" + dir + "\n")

	if got := p.promptPath("inbox", ""); got != dir {
		t.Errorf("promptPath() = %q, want %q after rejecting the file path", got, dir)
	}
}

func TestPrompterPromptPathDeclinesCreateThenAcceptsFolder(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "missing")
	dir := t.TempDir()
	// "n" declines creating `missing`, then the wizard re-asks for a path.
	p := newPrompter(missing + "\nn\n" + dir + "\n")

	if got := p.promptPath("inbox", ""); got != dir {
		t.Errorf("promptPath() = %q, want %q", got, dir)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Errorf("%s was created despite declining", missing)
	}
}

func TestPrompterPromptPathMkdirAllFails(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sub")
	if err := os.Chmod(base, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o755) })

	dir := t.TempDir()
	// First attempt: confirm creation, but the parent is read-only so
	// MkdirAll fails and the wizard re-asks; second attempt succeeds.
	p := newPrompter(target + "\ny\n" + dir + "\n")

	if got := p.promptPath("inbox", ""); got != dir {
		t.Errorf("promptPath() = %q, want %q after the failed MkdirAll", got, dir)
	}
}

func TestPrompterPromptPathStatOtherErrorFallsThrough(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A path with a regular file as an intermediate component: os.Stat
	// returns ENOTDIR, not ENOENT, hitting the "Cannot access" branch.
	invalid := filepath.Join(file, "sub")
	dir := t.TempDir()
	p := newPrompter(invalid + "\n" + dir + "\n")

	if got := p.promptPath("inbox", ""); got != dir {
		t.Errorf("promptPath() = %q, want %q after the stat error", got, dir)
	}
}

func TestPrompterPromptBinaryAcceptsFoundBinary(t *testing.T) {
	dir := t.TempDir()
	kimi := filepath.Join(dir, "kimi")
	if err := os.WriteFile(kimi, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	p := newPrompter("y\n")
	if got := p.promptBinary("kimi"); got != kimi {
		t.Errorf("promptBinary() = %q, want %q (the found binary, accepted)", got, kimi)
	}
}

func TestPrompterPromptBinaryDeclinesFoundThenManual(t *testing.T) {
	dir := t.TempDir()
	kimi := filepath.Join(dir, "kimi")
	if err := os.WriteFile(kimi, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	manual := filepath.Join(dir, "custom-kimi")
	if err := os.WriteFile(manual, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := newPrompter("n\n" + manual + "\n")
	if got := p.promptBinary("kimi"); got != manual {
		t.Errorf("promptBinary() = %q, want %q (declined found, entered manually)", got, manual)
	}
}

func TestPrompterPromptBackend(t *testing.T) {
	if got := newPrompter("kimi\n").promptBackend(); got != "kimi" {
		t.Errorf("promptBackend() = %q, want kimi", got)
	}
	if got := newPrompter("CLAUDE\n").promptBackend(); got != "claude" {
		t.Errorf("promptBackend() = %q, want claude (lowercased)", got)
	}
	if got := newPrompter("gpt\nclaude\n").promptBackend(); got != "claude" {
		t.Errorf("promptBackend() = %q, want claude after rejecting an invalid answer", got)
	}
}

func TestPrompterPromptBinaryFallsBackToManualPath(t *testing.T) {
	// Force setup.ResolveBinary to fail regardless of what's actually
	// installed on this machine.
	t.Setenv("PATH", t.TempDir())

	script := filepath.Join(t.TempDir(), "fake-kimi")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	p := newPrompter(script + "\n")

	if got := p.promptBinary("kimi"); got != script {
		t.Errorf("promptBinary() = %q, want %q", got, script)
	}
}

func TestPrompterPromptBinaryRejectsNonExecutableThenAccepts(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	dir := t.TempDir()
	notExec := filepath.Join(dir, "plain-file")
	if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	script := filepath.Join(dir, "real-script")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	p := newPrompter(notExec + "\n" + script + "\n")

	if got := p.promptBinary("kimi"); got != script {
		t.Errorf("promptBinary() = %q, want %q after rejecting a non-executable file", got, script)
	}
}

func TestPrintCompletion(t *testing.T) {
	// Smoke test: must not panic regardless of serviceInstalled, and the
	// GOOS-specific hint only appears for that OS.
	printCompletion("/tmp/config.yaml", true)
	printCompletion("/tmp/config.yaml", false)
}

// TestRunInitPromptWriteConfigError exercises runInitPrompt's error path
// when the config file cannot be written, without ever reaching the
// service-install confirm (WriteConfig fails first).
func TestRunInitPromptWriteConfigError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	base := t.TempDir()
	inbox := filepath.Join(base, "inbox")
	library := filepath.Join(base, "library")
	fakeKimi := filepath.Join(base, "fake-kimi")
	if err := os.WriteFile(fakeKimi, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	// A plain file where the config's parent directory should be: WriteConfig's
	// os.MkdirAll fails.
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	cfgPath := filepath.Join(blocker, "nested", "config.yaml")

	input := strings.Join([]string{
		"test-api-key",
		inbox,
		"y",
		library,
		"y",
		"kimi",
		fakeKimi,
	}, "\n") + "\n"

	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe error = %v", err)
	}
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })
	go func() {
		defer w.Close()
		fmt.Fprint(w, input)
	}()

	if got := runInitPrompt(cfgPath); got != 1 {
		t.Errorf("runInitPrompt() = %d, want 1 when the config cannot be written", got)
	}
}

// TestRunInitPromptFullFlow drives runInitPrompt end to end via a scripted
// stdin, declining the background-service install (the only way to keep
// the test from touching this machine's real, already-running
// patro.service).
func TestRunInitPromptFullFlow(t *testing.T) {
	// Force ResolveBinary("kimi") to fail so the wizard takes the
	// deterministic "enter an absolute path" branch instead of depending on
	// whether kimi happens to be installed on the host.
	t.Setenv("PATH", t.TempDir())

	base := t.TempDir()
	inbox := filepath.Join(base, "inbox")
	library := filepath.Join(base, "library")
	fakeKimi := filepath.Join(base, "fake-kimi")
	if err := os.WriteFile(fakeKimi, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	cfgPath := filepath.Join(base, "config.yaml")

	input := strings.Join([]string{
		"test-api-key", // API key
		inbox,          // recordings folder (missing)
		"y",            // confirm create it
		library,        // library folder (missing)
		"y",            // confirm create it
		"kimi",         // backend
		fakeKimi,       // manual binary path (ResolveBinary fails, PATH is empty)
		"n",            // decline installing the background service
	}, "\n") + "\n"

	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe error = %v", err)
	}
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })
	go func() {
		defer w.Close()
		fmt.Fprint(w, input)
	}()

	got := runInitPrompt(cfgPath)
	if got != 0 {
		t.Fatalf("runInitPrompt() = %d, want 0", got)
	}

	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("config file was not written: %v", err)
	}
	if info, err := os.Stat(inbox); err != nil || !info.IsDir() {
		t.Errorf("inbox folder was not created: %v", err)
	}
	if info, err := os.Stat(library); err != nil || !info.IsDir() {
		t.Errorf("library folder was not created: %v", err)
	}
}
