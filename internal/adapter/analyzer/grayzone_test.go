package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fernando143/patro/internal/adapter/analyzer/backend"
	"github.com/fernando143/patro/internal/domain/meeting"
)

// These tests moved here with the code. They previously lived in
// internal/domain/knowledge, which is the knowledge domain and had no business
// shelling out to a CLI.

// writeGrayZoneFake materializes a fake reconciliation CLI (see fakeCLI in
// cli_test.go), portably across Unix shell scripts and the Windows helper
// process.
func writeGrayZoneFake(t *testing.T, dir string, ctrl fakeControl) string {
	t.Helper()
	return fakeCLI(t, dir, "fake-cli", ctrl)
}

func mustBackend(t *testing.T, name string) backend.Backend {
	t.Helper()
	b, ok := backend.Get(name)
	if !ok {
		t.Fatalf("backend %q is not registered", name)
	}
	return b
}

func decideWith(t *testing.T, name, script string, timeout time.Duration) (bool, error) {
	t.Helper()
	decide := GrayZoneDecision(mustBackend(t, name), script, timeout)
	return decide(context.Background(), meeting.Topic{Name: "X"}, meeting.TopicRef{Name: "Y", Slug: "y"})
}

func TestGrayZoneDecisionYes(t *testing.T) {
	same, err := decideWith(t, "kimi", writeGrayZoneFake(t, t.TempDir(), fakeControl{Stdout: "yes"}), time.Second)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !same {
		t.Error("same = false, want true for a \"yes\" answer")
	}
}

func TestGrayZoneDecisionNo(t *testing.T) {
	same, err := decideWith(t, "kimi", writeGrayZoneFake(t, t.TempDir(), fakeControl{Stdout: "no"}), time.Second)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if same {
		t.Error("same = true, want false for a \"no\" answer")
	}
}

// TestGrayZoneDecisionCodexReadsItsOwnStreamShape covers the one backend
// with no plain-text mode: its answer arrives as a JSONL agent_message and
// has to be read back through the same parser the analyzer uses.
func TestGrayZoneDecisionCodexReadsItsOwnStreamShape(t *testing.T) {
	script := writeGrayZoneFake(t, t.TempDir(), fakeControl{
		Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"yes"}}`,
	})
	same, err := decideWith(t, "codex", script, time.Second)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !same {
		t.Error("same = false, want true for a Codex \"yes\" answer")
	}
}

// TestGrayZoneDecisionCodexRawJSONIsNotAYes guards the reason the Codex
// branch exists at all: its raw stdout starts with '{', so reading it
// without parsing would never match "yes" — and a decider that always says
// no silently disables merging.
func TestGrayZoneDecisionCodexRawJSONIsNotAYes(t *testing.T) {
	script := writeGrayZoneFake(t, t.TempDir(), fakeControl{
		Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"no"}}`,
	})
	same, err := decideWith(t, "codex", script, time.Second)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if same {
		t.Error("same = true, want false for a Codex \"no\" answer")
	}
}

func TestGrayZoneDecisionNonZeroExit(t *testing.T) {
	if _, err := decideWith(t, "kimi", writeGrayZoneFake(t, t.TempDir(), fakeControl{Exit: 1}), time.Second); err == nil {
		t.Fatal("decide() error = nil, want an error on non-zero exit")
	}
}

func TestGrayZoneDecisionTimeout(t *testing.T) {
	// The fake blocks instead of answering (exec sleep on Unix, a plain
	// sleep in the helper process on Windows), so the kill on context
	// timeout reaches the process actually holding stdout/stderr open and
	// the test returns promptly instead of blocking for the full sleep
	// duration.
	script := writeGrayZoneFake(t, t.TempDir(), fakeControl{SleepSeconds: 5})
	_, err := decideWith(t, "kimi", script, 50*time.Millisecond)
	if err == nil {
		t.Fatal("decide() error = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to mention a timeout", err)
	}
}

// TestGrayZoneDecisionArgvNotShellInterpreted: the prompt embeds a topic
// name that came from an LLM, so it must reach the CLI as one argv element
// and never through a shell.
func TestGrayZoneDecisionArgvNotShellInterpreted(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.txt")
	script := writeGrayZoneFake(t, dir, fakeControl{EchoArgs: promptFile})

	dangerous := "candidate `touch " + filepath.Join(dir, "pwned") + "` $(touch " + filepath.Join(dir, "pwned2") + ")"
	decide := GrayZoneDecision(mustBackend(t, "kimi"), script, time.Second)
	if _, err := decide(context.Background(),
		meeting.Topic{Name: dangerous, Content: "c"},
		meeting.TopicRef{Name: "Y", Slug: "y"}); err != nil {
		t.Fatalf("decide: %v", err)
	}

	data, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("reading captured prompt: %v", err)
	}
	// The prompt embeds the topic name with %q, which escapes backslashes —
	// Windows paths make that visible. Comparing against the quoted form
	// keeps the assertion (verbatim pass-through, no shell reinterpretation)
	// the same on every platform.
	if !strings.Contains(string(data), strconv.Quote(dangerous)) {
		t.Errorf("prompt argument was not passed through verbatim:\ngot:  %q\nwant substring: %q", data, strconv.Quote(dangerous))
	}
	for _, pwned := range []string{filepath.Join(dir, "pwned"), filepath.Join(dir, "pwned2")} {
		if _, err := os.Stat(pwned); err == nil {
			t.Errorf("shell metacharacters in the prompt were interpreted; %s was created", pwned)
		}
	}
}
