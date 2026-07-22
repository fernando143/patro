// Package config loads patro's YAML configuration.
//
// Configuration is read from config.yaml; all relative paths inside it are
// resolved against the directory containing the loaded file (Config.Dir).
// The AssemblyAI API key is read exclusively from the ASSEMBLYAI_API_KEY
// environment variable.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// APIKeyEnvVar is the environment variable holding the AssemblyAI API key.
const APIKeyEnvVar = "ASSEMBLYAI_API_KEY"

const (
	defaultInbox                    = "~/Videos/obs"
	defaultLibrary                  = "./knowledge"
	defaultStabilityChecks          = 2
	defaultStabilityIntervalSeconds = 5
	defaultAnalyzerBackend          = "kimi"
	defaultKimiPath                 = "kimi"
	defaultClaudePath               = "claude"
)

var (
	defaultVideoExtensions = []string{".mkv", ".mp4", ".mov", ".webm"}
	validAnalyzerBackends  = []string{"kimi", "lemur", "claude"}
)

// Config is the runtime configuration with all paths resolved.
type Config struct {
	Inbox                    string   // absolute path
	Library                  string   // absolute path
	VideoExtensions          []string // lowercase, leading dot
	StabilityChecks          int
	StabilityIntervalSeconds int
	AnalyzerBackend          string
	KimiPath                 string
	ClaudePath               string
	Dir                      string // directory of the config file; base for relative paths
	Path                     string // resolved config file, "" when none was found
}

// ValidAnalyzerBackends returns the accepted analyzer_backend values. The
// result is a copy so callers cannot corrupt validation.
func ValidAnalyzerBackends() []string {
	return append([]string(nil), validAnalyzerBackends...)
}

// yamlConfig mirrors config.yaml. Pointer fields distinguish an absent key
// (fall back to the default) from an explicitly set zero value.
type yamlConfig struct {
	Inbox                    *string  `yaml:"inbox"`
	Library                  *string  `yaml:"library"`
	VideoExtensions          []string `yaml:"video_extensions"`
	StabilityChecks          *int     `yaml:"stability_checks"`
	StabilityIntervalSeconds *int     `yaml:"stability_interval_seconds"`
	AnalyzerBackend          *string  `yaml:"analyzer_backend"`
	KimiPath                 *string  `yaml:"kimi_path"`
	ClaudePath               *string  `yaml:"claude_path"`
}

// UserConfigPath returns ~/.config/patro/config.yaml, or "" when the home
// directory cannot be determined.
func UserConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "patro", "config.yaml")
}

// Load reads the config. Search order when flagPath == "":
//  1. ./config.yaml (if it exists)
//  2. UserConfigPath() (if it exists)
//  3. built-in defaults with Dir = current working directory.
//
// When flagPath != "" that file is used (missing file = defaults for that
// path). Missing keys in the YAML fall back to the built-in defaults.
func Load(flagPath string) (*Config, error) {
	configPath := ""
	if flagPath != "" {
		abs, err := filepath.Abs(flagPath)
		if err != nil {
			return nil, err
		}
		configPath = abs
	} else {
		if _, err := os.Stat("config.yaml"); err == nil {
			abs, err := filepath.Abs("config.yaml")
			if err != nil {
				return nil, err
			}
			configPath = abs
		} else if userPath := UserConfigPath(); userPath != "" {
			if _, err := os.Stat(userPath); err == nil {
				configPath = userPath
			}
		}
	}

	dir := ""
	if configPath != "" {
		dir = filepath.Dir(configPath)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = cwd
	}

	raw := yamlConfig{}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, &raw); err != nil {
				return nil, fmt.Errorf("patro: cannot parse %s: %w", configPath, err)
			}
		case flagPath != "" && os.IsNotExist(err):
			// Explicitly requested but missing: use the defaults.
		default:
			return nil, err
		}
	}

	backend := strings.ToLower(strings.TrimSpace(stringOr(raw.AnalyzerBackend, defaultAnalyzerBackend)))
	if !validBackend(backend) {
		return nil, fmt.Errorf(
			"invalid analyzer_backend %q in %s; valid values: %s",
			backend, filepath.Base(configPath), strings.Join(validAnalyzerBackends, ", "),
		)
	}

	return &Config{
		Inbox:                    resolvePath(stringOr(raw.Inbox, defaultInbox), dir),
		Library:                  resolvePath(stringOr(raw.Library, defaultLibrary), dir),
		VideoExtensions:          normalizeExtensions(extensionsOr(raw.VideoExtensions)),
		StabilityChecks:          intOr(raw.StabilityChecks, defaultStabilityChecks),
		StabilityIntervalSeconds: intOr(raw.StabilityIntervalSeconds, defaultStabilityIntervalSeconds),
		AnalyzerBackend:          backend,
		KimiPath:                 binaryPathOr(raw.KimiPath, defaultKimiPath),
		ClaudePath:               binaryPathOr(raw.ClaudePath, defaultClaudePath),
		Dir:                      dir,
		Path:                     configPath,
	}, nil
}

// APIKey returns the AssemblyAI key from the environment or an error whose
// message matches the Python one.
func (c *Config) APIKey() (string, error) {
	key := strings.TrimSpace(os.Getenv(APIKeyEnvVar))
	if key == "" {
		return "", fmt.Errorf(
			"%s is not set. Export your AssemblyAI API key, "+
				"e.g.: export %s=<your-key> "+
				"(or use --mock to run without the API).",
			APIKeyEnvVar, APIKeyEnvVar,
		)
	}
	return key, nil
}

// IsVideo reports whether path has a configured video extension
// (case-insensitive).
func (c *Config) IsVideo(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range c.VideoExtensions {
		if e == ext {
			return true
		}
	}
	return false
}

// StateDir returns the directory holding patro's persistent state.
func (c *Config) StateDir() string {
	return filepath.Join(c.Dir, ".state")
}

// LogFile returns the path of patro's log file.
func (c *Config) LogFile() string {
	return filepath.Join(c.Dir, "patro.log")
}

func stringOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func intOr(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func extensionsOr(value []string) []string {
	if value == nil {
		return defaultVideoExtensions
	}
	return value
}

// binaryPathOr trims the configured binary path, falling back when empty.
func binaryPathOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	if trimmed := strings.TrimSpace(*value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func validBackend(backend string) bool {
	for _, b := range validAnalyzerBackends {
		if b == backend {
			return true
		}
	}
	return false
}

// normalizeExtensions lowercases every extension and ensures a leading dot.
func normalizeExtensions(extensions []string) []string {
	normalized := make([]string, len(extensions))
	for i, ext := range extensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		normalized[i] = ext
	}
	return normalized
}

// resolvePath expands a leading "~" and resolves value against base,
// returning a cleaned absolute path.
func resolvePath(value, base string) string {
	p := expandUser(value)
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// expandUser replaces a leading "~" with the user's home directory.
func expandUser(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	default:
		return path
	}
}
