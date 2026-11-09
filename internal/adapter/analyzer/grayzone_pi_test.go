package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fernando143/patro/internal/domain/meeting"
)

// TestGrayZonePiUsesTheSafetyFlags guards the second place a backend is
// launched. The gray-zone question historically used a bare `-p <prompt>` for
// every backend that had one, which for Pi would have run with its default
// tools — write and edit included — and persisted a session, breaking the
// read-only guarantee the analyzer path keeps.
func TestGrayZonePiUsesTheSafetyFlags(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")

	var script string
	if runtime.GOOS == "windows" {
		script = filepath.Join(dir, "rec.cmd")
		body := "@echo off\r\n>" + out + " echo %*\r\nexit /b 1\r\n"
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		script = filepath.Join(dir, "rec.sh")
		body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + out + "\nexit 1\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	decide := GrayZoneDecision(mustBackend(t, "pi"), script, time.Second)
	if _, err := decide(context.Background(), meeting.Topic{Name: "A", Content: "x"}, meeting.TopicRef{Name: "B", Slug: "b"}); err == nil {
		t.Fatal("decide error = nil, want the recorder's failure")
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("recorder wrote nothing: %v", err)
	}
	for _, want := range []string{"--no-session", "--tools", "read", "--no-context-files"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("gray-zone argv missing %q; got %q", want, got)
		}
	}
}
