// Package backend is the registry of analyzer backends: the local CLIs and
// the hosted service that can turn a transcript into structured knowledge.
//
// The set used to be re-declared, and switched on, in six places —
// config validation, the analyzer's own launcher, both config writers, the
// settings TUI and the gray-zone decider — with a comment in one of them
// asking future maintainers to keep it in sync with another by hand. Adding
// a backend meant finding all of them, and the setup wizard proved the point
// by silently falling two backends behind.
//
// This package holds no dependencies of its own so every one of those
// callers can read from it, including internal/platform/config, which the analyzer
// itself imports.
package backend

import (
	"fmt"
	"strings"
)

// Backend describes one analyzer backend end to end: how it is named in
// config, how it is shown to a human, where its binary path is stored, and
// how it is invoked.
type Backend struct {
	// Name is the analyzer_backend value in config.yaml.
	Name string
	// Label is the one-line description shown in menus.
	Label string
	// DisplayName is the capitalized name used when attributing parsed
	// output, e.g. in a parse error.
	DisplayName string
	// Hosted marks a backend that runs in the cloud and needs no local
	// binary. ConfigKey and DefaultBinary are empty for hosted backends and
	// every config writer must skip the path write for them.
	Hosted bool
	// ConfigKey is the config.yaml key holding this backend's binary path,
	// empty when Hosted.
	ConfigKey string
	// DefaultBinary is the binary name looked up on PATH when the config
	// key is absent, empty when Hosted.
	DefaultBinary string
	// Argv builds the non-interactive command line for one prompt.
	Argv func(prompt string) []string
	// NotFoundHelp explains what to install or change when the binary is
	// missing.
	NotFoundHelp func(binaryPath string) string
	// CodexStyleStream marks a CLI that emits JSONL item.completed events
	// rather than stream-json assistant deltas. The gray-zone decider needs
	// a different reader for those two shapes.
	CodexStyleStream bool
}

// registry is an explicit ordered table rather than init()-based
// self-registration, so the full set is readable in one place and menu order
// is the declaration order.
var registry = []Backend{
	{
		Name:          "kimi",
		Label:         "kimi   — local Kimi CLI",
		DisplayName:   "Kimi",
		ConfigKey:     "kimi_path",
		DefaultBinary: "kimi",
		Argv: func(prompt string) []string {
			return []string{"-p", prompt, "--output-format", "stream-json"}
		},
		NotFoundHelp: func(binaryPath string) string {
			return fmt.Sprintf(
				"'%s' executable not found. Install Kimi Code CLI "+
					"(https://www.kimi.com/code), adjust kimi_path in config.yaml, "+
					"or set analyzer_backend: lemur in config.yaml.",
				binaryPath,
			)
		},
	},
	{
		Name:          "claude",
		Label:         "claude — local Claude CLI",
		DisplayName:   "Claude",
		ConfigKey:     "claude_path",
		DefaultBinary: "claude",
		// Claude Code CLI rejects `--print --output-format=stream-json`
		// unless --verbose is also passed.
		Argv: func(prompt string) []string {
			return []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
		},
		NotFoundHelp: func(binaryPath string) string {
			return fmt.Sprintf(
				"'%s' executable not found. Install Claude Code CLI, "+
					"adjust claude_path in config.yaml, or switch analyzer_backend to kimi/lemur.",
				binaryPath,
			)
		},
	},
	{
		Name:          "codex",
		Label:         "codex  — local Codex CLI",
		DisplayName:   "Codex",
		ConfigKey:     "codex_path",
		DefaultBinary: "codex",
		// Codex uses `exec` for non-interactive runs and emits JSONL events
		// with the final assistant message in an item.completed event. The
		// read-only sandbox and skipped repository check keep this provider
		// safe and usable when the config directory is not a Git checkout.
		Argv: func(prompt string) []string {
			return []string{
				"exec", "--json", "--ephemeral", "--skip-git-repo-check",
				"--sandbox", "read-only", prompt,
			}
		},
		NotFoundHelp: func(binaryPath string) string {
			return fmt.Sprintf(
				"'%s' executable not found. Install Codex CLI, "+
					"adjust codex_path in config.yaml, or switch analyzer_backend to kimi/claude/lemur.",
				binaryPath,
			)
		},
		CodexStyleStream: true,
	},
	{
		Name:        "lemur",
		Label:       "lemur  — hosted by AssemblyAI, no local CLI",
		DisplayName: "LeMUR",
		Hosted:      true,
	},
}

// All returns every registered backend in display order. The slice is a copy
// so callers cannot corrupt the registry.
func All() []Backend {
	return append([]Backend(nil), registry...)
}

// Names returns every registered backend name in display order.
func Names() []string {
	names := make([]string, len(registry))
	for i, b := range registry {
		names[i] = b.Name
	}
	return names
}

// Get looks up a backend by its config name, trimming and lowercasing first
// so a hand-edited config.yaml is forgiving about case and stray spaces.
func Get(name string) (Backend, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, b := range registry {
		if b.Name == normalized {
			return b, true
		}
	}
	return Backend{}, false
}

// IsValid reports whether name is a registered backend.
func IsValid(name string) bool {
	_, ok := Get(name)
	return ok
}

// Default is the backend used when config.yaml does not name one.
const Default = "kimi"
