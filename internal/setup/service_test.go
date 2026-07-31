package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests below intentionally never exercise InstallService, SetAPIKey,
// RestartService, installLinuxService, setLinuxAPIKey, installMacService,
// setMacAPIKey, or reloadLaunchAgent: those shell out to the real
// systemctl --user / launchctl on this machine, and this user already runs
// a real, active patro.service — restarting or re-enabling it from a test
// would disrupt a live service outside the test's control. Only the pure
// path/parsing helpers and file-only writers are covered.

func TestLinuxUnitAndOverridePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	unitPath, err := linuxUnitPath()
	if err != nil {
		t.Fatalf("linuxUnitPath error = %v", err)
	}
	want := filepath.Join(home, ".config", "systemd", "user", "patro.service")
	if unitPath != want {
		t.Errorf("linuxUnitPath = %q, want %q", unitPath, want)
	}

	overridePath, err := linuxOverridePath()
	if err != nil {
		t.Fatalf("linuxOverridePath error = %v", err)
	}
	wantOverride := filepath.Join(home, ".config", "systemd", "user", "patro.service.d", "override.conf")
	if overridePath != wantOverride {
		t.Errorf("linuxOverridePath = %q, want %q", overridePath, wantOverride)
	}
}

func TestMacPlistPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := macPlistPath()
	if err != nil {
		t.Fatalf("macPlistPath error = %v", err)
	}
	want := filepath.Join(home, "Library", "LaunchAgents", "com.patro.plist")
	if got != want {
		t.Errorf("macPlistPath = %q, want %q", got, want)
	}
}

func TestWriteLinuxOverrideContentAndPermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := writeLinuxOverride("my-secret-key"); err != nil {
		t.Fatalf("writeLinuxOverride error = %v", err)
	}

	path := filepath.Join(home, ".config", "systemd", "user", "patro.service.d", "override.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if !strings.Contains(string(data), "Environment=ASSEMBLYAI_API_KEY=my-secret-key") {
		t.Errorf("override content = %q, want the API key line", data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("override file mode = %o, want 0600 (owner-only, holds a secret)", perm)
	}
}

func TestWriteLinuxOverrideRewritesLooserExistingPermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Simulate a pre-existing override created with looser permissions:
	// os.WriteFile does not chmod a file that already exists.
	dir := filepath.Join(home, ".config", "systemd", "user", "patro.service.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	path := filepath.Join(dir, "override.conf")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if err := writeLinuxOverride("new-key"); err != nil {
		t.Fatalf("writeLinuxOverride error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("override file mode = %o, want 0600 even when the file pre-existed with looser permissions", perm)
	}
}

func TestServiceAPIKeyConfiguredLinuxNoOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if ServiceAPIKeyConfigured() {
		t.Error("ServiceAPIKeyConfigured() = true, want false when no override.conf exists")
	}
}

func TestServiceAPIKeyConfiguredLinuxEmptyKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := writeLinuxOverride(""); err != nil {
		t.Fatalf("writeLinuxOverride error = %v", err)
	}
	if ServiceAPIKeyConfigured() {
		t.Error("ServiceAPIKeyConfigured() = true, want false for an empty key")
	}
}

func TestServiceAPIKeyConfiguredLinuxSetKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := writeLinuxOverride("a-real-key"); err != nil {
		t.Fatalf("writeLinuxOverride error = %v", err)
	}
	if !ServiceAPIKeyConfigured() {
		t.Error("ServiceAPIKeyConfigured() = false, want true when the override carries a non-empty key")
	}
}

func TestServiceExecutablePath(t *testing.T) {
	got, err := serviceExecutablePath()
	if err != nil {
		t.Fatalf("serviceExecutablePath error = %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("serviceExecutablePath = %q, want an absolute path", got)
	}
}

func TestXMLEscape(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"ampersand", "a&b", "a&amp;b"},
		{"angle brackets", "<tag>", "&lt;tag&gt;"},
		{"quotes", `say "hi"`, "say &quot;hi&quot;"},
		{"apostrophe", "it's", "it&apos;s"},
		{"plain text unchanged", "plain-key-123", "plain-key-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := xmlEscape(tt.input); got != tt.want {
				t.Errorf("xmlEscape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRunQuietSuccess(t *testing.T) {
	if err := runQuiet("true"); err != nil {
		t.Errorf("runQuiet(true) error = %v, want nil", err)
	}
}

func TestRunQuietFailureIncludesOutput(t *testing.T) {
	err := runQuiet("sh", "-c", "echo boom to stderr >&2; exit 1")
	if err == nil {
		t.Fatal("runQuiet error = nil, want error for a failing command")
	}
	if !strings.Contains(err.Error(), "boom to stderr") {
		t.Errorf("runQuiet error = %q, want it to include the command's combined output", err)
	}
}

func TestRunQuietCommandNotFound(t *testing.T) {
	err := runQuiet("patro-definitely-not-a-real-binary-xyz")
	if err == nil {
		t.Fatal("runQuiet error = nil, want error for a missing binary")
	}
}

func TestCellarOptPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "macos cellar path",
			in:   "/opt/homebrew/Cellar/patro/0.2.0/bin/patro",
			want: "/opt/homebrew/opt/patro/bin/patro",
		},
		{
			name: "linuxbrew cellar path",
			in:   "/home/linuxbrew/.linuxbrew/Cellar/patro/0.1.1/bin/patro",
			want: "/home/linuxbrew/.linuxbrew/opt/patro/bin/patro",
		},
		{
			name: "non-cellar path",
			in:   "/usr/local/bin/patro",
			want: "",
		},
		{
			name: "cellar without version segment",
			in:   "/opt/homebrew/Cellar/patro",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellarOptPath(tc.in); got != tc.want {
				t.Errorf("cellarOptPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
