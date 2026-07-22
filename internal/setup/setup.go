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
	"strings"

	"github.com/fernando143/patro/internal/config"
)

// ExpandPath expands a leading "~" and returns the absolute path.
func ExpandPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		switch {
		case path == "~":
			path = home
		case strings.HasPrefix(path, "~/"):
			path = filepath.Join(home, path[2:])
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// ConfigPath picks the config file to write: the --config value when given,
// else ./config.yaml when it exists (update in place), else
// ~/.config/patro/config.yaml.
func ConfigPath(flagConfig string) string {
	path := flagConfig
	if path == "" {
		if _, err := os.Stat("config.yaml"); err == nil {
			path = "config.yaml"
		} else if userPath := config.UserConfigPath(); userPath != "" {
			path = userPath
		} else {
			path = "config.yaml"
		}
	}
	return ExpandPath(path)
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

// ValidateExecutable requires an existing executable file.
func ValidateExecutable(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("this field is required")
	}
	info, err := os.Stat(ExpandPath(s))
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return errors.New("not an executable file")
	}
	return nil
}
