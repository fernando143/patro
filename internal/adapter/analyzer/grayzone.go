package analyzer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/fernando143/patro/internal/adapter/analyzer/backend"
	"github.com/fernando143/patro/internal/domain/meeting"
)

// GrayZoneDecision answers the one question reconciliation cannot settle
// from cosine similarity alone: when a candidate topic scores between the
// merge and new-topic thresholds, is it the same topic as its nearest
// neighbour?
//
// This is a subprocess adapter, so it belongs here beside the analyzer's
// other CLI plumbing rather than inside internal/library, where two
// near-identical copies of it used to live — one for kimi and claude, one
// for codex, differing only in how the command was invoked and how its
// answer was read back. Both facts are in the registry, so one function
// covers every backend.
//
// The returned function matches knowledge.GrayZoneDecider and is assignable to
// it without this package importing the domain.
func GrayZoneDecision(b backend.Backend, binaryPath string, timeout time.Duration) func(context.Context, meeting.Topic, meeting.TopicRef) (bool, error) {
	return func(ctx context.Context, candidate meeting.Topic, nearest meeting.TopicRef) (bool, error) {
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		prompt := grayZonePrompt(candidate, nearest)

		// Only Codex lacks a plain-text mode; the others answer a bare
		// prompt directly, which keeps this one-word question cheap.
		args := []string{"-p", prompt}
		if b.CodexStyleStream {
			args = b.Argv(prompt)
		}

		cmd := exec.CommandContext(runCtx, binaryPath, args...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out

		err := cmd.Run()
		if runCtx.Err() == context.DeadlineExceeded {
			return false, fmt.Errorf("library: reconciliation LLM call timed out after %s", timeout)
		}
		if err != nil {
			return false, fmt.Errorf("library: reconciliation LLM call failed: %w", err)
		}

		raw := out.String()
		if b.CodexStyleStream {
			raw = assistantText(raw)
		}
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "yes"), nil
	}
}

// grayZonePrompt asks for exactly one word so the answer can be read without
// parsing prose.
func grayZonePrompt(candidate meeting.Topic, nearest meeting.TopicRef) string {
	return fmt.Sprintf(
		"Existing topic: %q (slug %q).\n"+
			"Candidate topic: %q.\n%s\n"+
			"Is the candidate the SAME topic as the existing one? "+
			"Answer with exactly one word: yes or no.",
		nearest.Name, nearest.Slug, candidate.Name, strings.TrimSpace(candidate.Content),
	)
}
