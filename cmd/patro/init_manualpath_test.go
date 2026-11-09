package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestPromptBinaryAcceptsManualPath covers the fallback wizard, which had its
// own copy of the executable-bit rule and therefore rejected every path on
// Windows — where files never carry that bit — leaving the prompt loop
// unbounded. The interactive wizard always accepted the path, so only this
// half was broken.
func TestPromptBinaryAcceptsManualPath(t *testing.T) {
	name := "probe-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An empty PATH keeps ResolveBinary from offering a different binary
	// first, so the manual answer is the one under test.
	t.Setenv("PATH", t.TempDir())

	p := newPrompter(path + "\n")
	done := make(chan string, 1)
	go func() { done <- p.promptBinary("kimi") }()

	select {
	case got := <-done:
		if got != path {
			t.Errorf("promptBinary() = %q, want %q", got, path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("promptBinary() never accepted the manual path")
	}
}
