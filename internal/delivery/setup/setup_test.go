package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExpandPathTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	if got := ExpandPath("~"); got != home {
		t.Errorf("ExpandPath(~) = %q, want %q", got, home)
	}
}

func TestExpandPathTildeSlash(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	want := filepath.Join(home, "recordings")
	if got := ExpandPath("~/recordings"); got != want {
		t.Errorf("ExpandPath(~/recordings) = %q, want %q", got, want)
	}
}

// A Windows user types ~\Videos\obs, so the tilde must expand before either
// separator, on every platform: the expansion is the same rule, only the
// spelling of the path differs.
func TestExpandPathWindowsStyleTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	want := filepath.Join(home, `Videos\obs`)
	if got := ExpandPath(`~\Videos\obs`); got != want {
		t.Errorf(`ExpandPath(~\Videos\obs) = %q, want %q`, got, want)
	}
}

func TestExpandPathRelativeBecomesAbsolute(t *testing.T) {
	got := ExpandPath("relative/dir")
	if !filepath.IsAbs(got) {
		t.Errorf("ExpandPath(relative/dir) = %q, want an absolute path", got)
	}
}

func TestExpandPathAlreadyAbsolute(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "foo")
	if got := ExpandPath(abs); got != abs {
		t.Errorf("ExpandPath(%q) = %q, want unchanged", abs, got)
	}
}

func TestConfigPathFlagTakesPriority(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "custom.yaml")
	if got := ConfigPath(flag); got != flag {
		t.Errorf("ConfigPath(flag) = %q, want the flag value expanded", got)
	}
}

func TestConfigPathUsesCanonicalUserConfigWhenLocalConfigIsPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("inbox: x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	// UserHomeDir reads HOME on Unix and USERPROFILE on Windows, so both are
	// pointed at the same directory to keep the expectation platform-neutral.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := ConfigPath("")
	want := filepath.Join(home, ".config", "patro", "config.yaml")
	if got != want {
		t.Errorf("ConfigPath(\"\") = %q, want %q (canonical user config)", got, want)
	}
}

func TestConfigPathFallsBackToUserConfigDir(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	// A cwd with no config.yaml, so ConfigPath falls through to
	// config.UserConfigPath() under $HOME.
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	got := ConfigPath("")
	want := filepath.Join(dir, ".config", "patro", "config.yaml")
	if got != want {
		t.Errorf("ConfigPath(\"\") = %q, want %q", got, want)
	}
}

func TestResolveBinaryFound(t *testing.T) {
	// "sh" is guaranteed to be on PATH on any POSIX system running these
	// tests.
	got, err := ResolveBinary("sh")
	if err != nil {
		t.Fatalf("ResolveBinary(sh) error = %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("ResolveBinary(sh) = %q, want an absolute path", got)
	}
}

func TestResolveBinaryNotFound(t *testing.T) {
	_, err := ResolveBinary("patro-definitely-not-a-real-binary-xyz")
	if err == nil {
		t.Fatal("ResolveBinary error = nil, want error for a missing binary")
	}
}

func TestValidateExecutableEmpty(t *testing.T) {
	if err := ValidateExecutable("   "); err == nil {
		t.Error("ValidateExecutable(blank) error = nil, want error")
	}
}

func TestValidateExecutableMissingFile(t *testing.T) {
	if err := ValidateExecutable(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("ValidateExecutable(missing) error = nil, want error")
	}
}

func TestValidateExecutableDirectory(t *testing.T) {
	if err := ValidateExecutable(t.TempDir()); err == nil {
		t.Error("ValidateExecutable(dir) error = nil, want error for a directory")
	}
}

func TestValidateExecutableNotExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit to withhold; see TestValidateExecutableWindows*")
	}
	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := ValidateExecutable(path); err == nil {
		t.Error("ValidateExecutable(non-exec file) error = nil, want error")
	}
}

func TestValidateExecutableValid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit to grant; see TestValidateExecutableWindows*")
	}
	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := ValidateExecutable(path); err != nil {
		t.Errorf("ValidateExecutable(executable file) error = %v, want nil", err)
	}
}

// On Windows a launchable file is identified by its extension, not by mode
// bits: .exe for binaries and .cmd/.bat for the npm-installed CLIs the
// analyzer backends shell out to.
func TestValidateExecutableWindowsAcceptsLaunchableExtension(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only rule")
	}
	for _, name := range []string{"tool.exe", "tool.cmd", "tool.bat", "TOOL.EXE"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o644); err != nil {
			t.Fatalf("WriteFile error = %v", err)
		}
		if err := ValidateExecutable(path); err != nil {
			t.Errorf("ValidateExecutable(%s) error = %v, want nil", name, err)
		}
	}
}

func TestValidateExecutableWindowsRejectsExtensionlessFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only rule")
	}
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := ValidateExecutable(path); err == nil {
		t.Error("ValidateExecutable(extensionless file) error = nil, want error")
	}
}
