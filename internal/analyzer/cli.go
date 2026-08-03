// CLI analyzer backends: kimi, claude, and codex.
//
// Instead of calling a hosted LLM, these backends shell out to their local
// CLIs in non-interactive mode. Kimi and Claude use `<binary> -p` with
// `--output-format stream-json`; Codex uses `codex exec --json` in a
// read-only sandbox. The prompt tells the CLI to READ the transcript from a
// file on disk — embedding a long transcript in the argument would risk
// ARG_MAX — and to answer with the same strict JSON schema as the LeMUR
// backend (see BuildPrompt).
//
// Stdout is JSONL: assistant messages (text and/or tool calls), tool
// results and meta lines. The text of all assistant messages is
// concatenated and fed to the defensive parser every backend shares
// (ParseAnalysis).
//
// Errors are explicit: missing binary, non-zero exit (with truncated
// stderr) and timeout each produce a clear error.
//
// This is a port of scribe/kimi_analyzer.py and scribe/claude_analyzer.py.
package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/types"
)

// cliTimeoutSeconds bounds a single CLI invocation, mirroring
// KIMI_TIMEOUT_SECONDS / CLAUDE_TIMEOUT_SECONDS in the Python backends.
const cliTimeoutSeconds = 600

// AnalyzeCLI runs the local CLI backend selected by cfg.AnalyzerBackend
// ("kimi" -> cfg.KimiPath, "claude" -> cfg.ClaudePath,
// "codex" -> cfg.CodexPath) over the transcript
// and parses its answer.
func AnalyzeCLI(ctx context.Context, t *types.TranscriptResult, existing []types.TopicRef, cfg *config.Config) (*types.AnalysisResult, error) {
	var binaryPath, lowerName, parseName, notFoundMsg string
	var args func(prompt string) []string
	switch strings.ToLower(strings.TrimSpace(cfg.AnalyzerBackend)) {
	case "kimi":
		binaryPath, lowerName, parseName = cfg.KimiPath, "kimi", "Kimi"
		args = func(prompt string) []string {
			return []string{"-p", prompt, "--output-format", "stream-json"}
		}
		notFoundMsg = fmt.Sprintf(
			"'%s' executable not found. Install Kimi Code CLI "+
				"(https://www.kimi.com/code), adjust kimi_path in config.yaml, "+
				"or set analyzer_backend: lemur in config.yaml.",
			binaryPath,
		)
	case "claude":
		binaryPath, lowerName, parseName = cfg.ClaudePath, "claude", "Claude"
		// Claude Code CLI rejects `--print --output-format=stream-json`
		// unless --verbose is also passed.
		args = func(prompt string) []string {
			return []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
		}
		notFoundMsg = fmt.Sprintf(
			"'%s' executable not found. Install Claude Code CLI, "+
				"adjust claude_path in config.yaml, or switch analyzer_backend to kimi/lemur.",
			binaryPath,
		)
	case "codex":
		binaryPath, lowerName, parseName = cfg.CodexPath, "codex", "Codex"
		// Codex uses `exec` for non-interactive runs and emits JSONL events
		// with the final assistant message in an item.completed event.
		// The read-only sandbox and skipped repository check keep this
		// provider safe and usable when cfg.Dir is not a Git checkout.
		args = func(prompt string) []string {
			return []string{
				"exec", "--json", "--ephemeral", "--skip-git-repo-check",
				"--sandbox", "read-only", prompt,
			}
		}
		notFoundMsg = fmt.Sprintf(
			"'%s' executable not found. Install Codex CLI, "+
				"adjust codex_path in config.yaml, or switch analyzer_backend to kimi/claude/lemur.",
			binaryPath,
		)
	default:
		return nil, fmt.Errorf("unknown CLI analyzer backend %q", cfg.AnalyzerBackend)
	}

	transcriptFile, err := writeTranscriptFile(t, cfg.StateDir())
	if err != nil {
		return nil, err
	}
	defer os.Remove(transcriptFile)

	prompt := BuildPrompt(existing, t.Language, transcriptFile)
	logging.Infof("Running %s analysis over transcript %s (%s) ...", parseName, t.ID, transcriptFile)

	stdout, err := runCLI(ctx, binaryPath, args(prompt), cfg.Dir, lowerName, notFoundMsg)
	if err != nil {
		return nil, err
	}
	raw := assistantText(stdout)
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s produced no assistant text in its stream-json output", lowerName)
	}
	return ParseAnalysis(raw, parseName), nil
}

// writeTranscriptFile writes the transcript (with speaker labels when
// diarized) to <stateDir>/tmp/transcript-<id>.txt and returns its path,
// creating the directory if needed. Mirrors
// kimi_analyzer._write_transcript_file.
func writeTranscriptFile(t *types.TranscriptResult, stateDir string) (string, error) {
	tmpDir := filepath.Join(stateDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(tmpDir, fmt.Sprintf("transcript-%s.txt", t.ID))
	var body string
	if len(t.Utterances) > 0 {
		lines := make([]string, len(t.Utterances))
		for i, u := range t.Utterances {
			lines[i] = fmt.Sprintf("Speaker %s: %s", u.Speaker, u.Text)
		}
		body = strings.Join(lines, "\n\n")
	} else {
		body = t.Text
	}
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// runCLI executes the provider-specific argv in workDir and returns stdout. Mirrors
// kimi_analyzer._run_kimi and claude_analyzer._run_claude: missing binary,
// timeout and non-zero exit (with stderr truncated to 1000 characters) each
// raise a clear error.
func runCLI(ctx context.Context, binaryPath string, args []string, workDir, lowerName, notFoundMsg string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, cliTimeoutSeconds*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, binaryPath, args...)
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s did not finish within %ds; aborting analysis.", lowerName, cliTimeoutSeconds)
	}
	if runCtx.Err() != nil {
		// The parent context was canceled.
		return "", runCtx.Err()
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return "", errors.New(notFoundMsg)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		trimmed := strings.TrimSpace(stderr.String())
		if runes := []rune(trimmed); len(runes) > 1000 {
			trimmed = string(runes[:1000])
		}
		return "", fmt.Errorf("%s exited with code %d: %s", lowerName, exitErr.ExitCode(), trimmed)
	}
	return "", err
}

// assistantText concatenates the text of every assistant message in the
// JSONL stream. Mirrors kimi_analyzer._assistant_text. Handles both flat
// messages ({"role":"assistant","content":...}) and the Claude Code envelope
// ({"type":"assistant","message":{"role":"assistant","content":...}}).
func assistantText(streamJSONStdout string) string {
	var chunks []string
	for _, line := range strings.Split(streamJSONStdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if msg, ok := obj["message"].(map[string]any); ok {
			obj = msg
		}
		if item, ok := obj["item"].(map[string]any); ok {
			if typ, _ := item["type"].(string); typ == "agent_message" {
				if text, _ := item["text"].(string); text != "" {
					chunks = append(chunks, text)
				}
			}
			continue
		}
		if role, _ := obj["role"].(string); role != "assistant" {
			continue
		}
		switch content := obj["content"].(type) {
		case string:
			chunks = append(chunks, content)
		case []any: // content blocks, e.g. [{"type": "text", ...}]
			for _, block := range content {
				b, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if typ, _ := b["type"].(string); typ != "text" {
					continue
				}
				if text, _ := b["text"].(string); text != "" {
					chunks = append(chunks, text)
				}
			}
		}
	}
	return strings.Join(chunks, "\n")
}
