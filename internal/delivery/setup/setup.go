// Package setup writes patro's configuration and installs or updates its
// user-level background service.
//
// Every function here is silent: results come back as errors or as log lines
// for the caller to print. That makes the package usable both from the
// line-based init wizard and from inside a Bubble Tea alt-screen program,
// where stray writes to stdout would corrupt the display.
package setup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fernando143/patro/internal/platform/config"
)

// ExpandPath expands a leading "~" and returns the absolute path. The tilde
// rule itself lives in internal/platform/config so the wizard, the service
// installer and config loading cannot disagree about what "~" means.
func ExpandPath(path string) string {
	path = config.ExpandUser(path)
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// ConfigPath returns the same canonical or explicitly requested path used by
// config.Load. Keeping this resolver shared prevents the TUI and service from
// editing and reading different files.
func ConfigPath(flagConfig string) string {
	return config.ResolvePath(flagConfig)
}

// ResolveBinary locates the named CLI on PATH and returns its absolute path.
func ResolveBinary(name string) (string, error) {
	found, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if abs, absErr := filepath.Abs(found); absErr == nil {
		return abs, nil
	}
	return found, nil
}

// ValidateExecutable requires an existing file the current OS can launch.
// On Unix that means the executable bit. Windows has no executable bit —
// every regular file reports the same mode — so there the test is the
// extension: .exe for binaries and .cmd/.bat for the npm-installed CLIs the
// analyzer backends shell out to. Without this branch the wizard could never
// accept a path on Windows.
func ValidateExecutable(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("this field is required")
	}
	path := ExpandPath(s)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || !launchableFile(path, info) {
		return errors.New("not an executable file")
	}
	return nil
}

// launchableFile reports whether a regular file is something the OS will run.
// It is separate from ValidateExecutable so the Windows extension rule can be
// exercised on any platform.
func launchableFile(path string, info os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return hasLaunchableExtension(path)
	}
	return info.Mode()&0o111 != 0
}

// hasLaunchableExtension reports whether path ends in an extension Windows can
// start: either a real binary (.exe, .com) or one of the .cmd/.bat shims that
// npm and pnpm generate for global CLI installs.
func hasLaunchableExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".com", ".cmd", ".bat":
		return true
	default:
		return false
	}
}
