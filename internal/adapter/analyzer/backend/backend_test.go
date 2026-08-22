package backend

import (
	"slices"
	"strings"
	"testing"
)

// TestNamesPinsTheSupportedSet is the characterization test for the set that
// used to live in six places. If a backend is added or removed, this fails
// first and every consumer follows from the registry rather than from a
// hand-maintained copy.
func TestNamesPinsTheSupportedSet(t *testing.T) {
	want := []string{"kimi", "claude", "codex", "lemur"}
	if got := Names(); !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

// TestOnlyLemurIsHosted pins which backend needs no local binary. Every
// config writer branches on this, and getting it wrong would either write a
// meaningless lemur_path key or skip a real CLI's path.
func TestOnlyLemurIsHosted(t *testing.T) {
	for _, b := range All() {
		wantHosted := b.Name == "lemur"
		if b.Hosted != wantHosted {
			t.Errorf("%s: Hosted = %v, want %v", b.Name, b.Hosted, wantHosted)
		}
		if b.Hosted {
			if b.ConfigKey != "" {
				t.Errorf("%s is hosted but has ConfigKey %q, want empty", b.Name, b.ConfigKey)
			}
			if b.DefaultBinary != "" {
				t.Errorf("%s is hosted but has DefaultBinary %q, want empty", b.Name, b.DefaultBinary)
			}
		}
	}
}

// TestConfigKeysMatchTheOnDiskFormat pins the config.yaml keys. These live in
// every installed user's config file, so they are a compatibility contract,
// not an implementation detail.
func TestConfigKeysMatchTheOnDiskFormat(t *testing.T) {
	want := map[string]string{
		"kimi":   "kimi_path",
		"claude": "claude_path",
		"codex":  "codex_path",
		"lemur":  "",
	}
	for _, b := range All() {
		if b.ConfigKey != want[b.Name] {
			t.Errorf("%s: ConfigKey = %q, want %q", b.Name, b.ConfigKey, want[b.Name])
		}
	}
}

// TestDefaultBinariesAreTheLookPathNames pins the names resolved on PATH
// when config.yaml carries no explicit path.
func TestDefaultBinariesAreTheLookPathNames(t *testing.T) {
	want := map[string]string{"kimi": "kimi", "claude": "claude", "codex": "codex", "lemur": ""}
	for _, b := range All() {
		if b.DefaultBinary != want[b.Name] {
			t.Errorf("%s: DefaultBinary = %q, want %q", b.Name, b.DefaultBinary, want[b.Name])
		}
	}
}

// TestArgvPinsEachCLIInvocation is the behavior most likely to break a
// backend silently: a wrong flag makes the CLI emit a format the parser
// cannot read, which surfaces as "produced no assistant text" rather than as
// a bad command line.
func TestArgvPinsEachCLIInvocation(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{"kimi", []string{"-p", "PROMPT", "--output-format", "stream-json"}},
		{"claude", []string{"-p", "PROMPT", "--output-format", "stream-json", "--verbose"}},
		{"codex", []string{"exec", "--json", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "PROMPT"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, ok := Get(tt.name)
			if !ok {
				t.Fatalf("Get(%q) not found", tt.name)
			}
			if got := b.Argv("PROMPT"); !slices.Equal(got, tt.want) {
				t.Errorf("Argv = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHostedBackendHasNoInvocation guards the nil-func case every caller
// must avoid dereferencing.
func TestHostedBackendHasNoInvocation(t *testing.T) {
	b, ok := Get("lemur")
	if !ok {
		t.Fatal("Get(\"lemur\") not found")
	}
	if b.Argv != nil {
		t.Error("Argv != nil for a hosted backend")
	}
	if b.NotFoundHelp != nil {
		t.Error("NotFoundHelp != nil for a hosted backend")
	}
}

// TestOnlyCodexUsesItsOwnStreamShape pins the one parsing difference the
// gray-zone decider branches on.
func TestOnlyCodexUsesItsOwnStreamShape(t *testing.T) {
	for _, b := range All() {
		want := b.Name == "codex"
		if b.CodexStyleStream != want {
			t.Errorf("%s: CodexStyleStream = %v, want %v", b.Name, b.CodexStyleStream, want)
		}
	}
}

// TestNotFoundHelpNamesTheBinaryAndItsConfigKey keeps the guidance
// actionable: a user who hits it needs to know which file to edit.
func TestNotFoundHelpNamesTheBinaryAndItsConfigKey(t *testing.T) {
	for _, b := range All() {
		if b.Hosted {
			continue
		}
		help := b.NotFoundHelp("/opt/custom/bin")
		if !strings.Contains(help, "/opt/custom/bin") {
			t.Errorf("%s: help does not name the binary path: %q", b.Name, help)
		}
		if !strings.Contains(help, b.ConfigKey) {
			t.Errorf("%s: help does not name config key %q: %q", b.Name, b.ConfigKey, help)
		}
	}
}

// TestGetIsForgivingAboutCaseAndSpace covers hand-edited config files.
func TestGetIsForgivingAboutCaseAndSpace(t *testing.T) {
	for _, name := range []string{"KIMI", "  claude  ", "Codex"} {
		if _, ok := Get(name); !ok {
			t.Errorf("Get(%q) not found, want a match", name)
		}
	}
	if _, ok := Get("no-such-backend"); ok {
		t.Error("Get(\"no-such-backend\") found, want miss")
	}
}

// TestDefaultIsRegistered guards the fallback used when config.yaml names no
// backend.
func TestDefaultIsRegistered(t *testing.T) {
	if !IsValid(Default) {
		t.Errorf("Default = %q is not a registered backend", Default)
	}
}

// TestAllReturnsACopy makes sure a caller cannot corrupt the registry.
func TestAllReturnsACopy(t *testing.T) {
	all := All()
	all[0].Name = "mutated"
	if Names()[0] == "mutated" {
		t.Error("All() exposed the registry's backing array")
	}
}
