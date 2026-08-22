// Package config loads patro's YAML configuration.
//
// The runtime configuration is read from ~/.config/patro/config.yaml unless
// an explicit --config path is supplied; all relative paths inside it are
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

	"github.com/fernando143/patro/internal/adapter/analyzer/backend"
)

// APIKeyEnvVar is the environment variable holding the AssemblyAI API key.
const APIKeyEnvVar = "ASSEMBLYAI_API_KEY"

const (
	defaultInbox                    = "~/Videos/obs"
	defaultLibrary                  = "./knowledge"
	defaultStabilityChecks          = 2
	defaultStabilityIntervalSeconds = 5
	// defaultEmbeddingBackend is provisional: cybertron is the only
	// candidate with third-party adoption evidence as of design D9.
	// tools/embedbench settles the final default once both backends
	// land (units 1c-1d).
	defaultEmbeddingBackend = "cybertron"
	// defaultMergeThreshold / defaultNewTopicThreshold are the 3-band
	// reconciliation thresholds (design D7), provisional until calibrated
	// with tools/embedbench: cosine >= merge_threshold auto-merges, cosine
	// < new_topic_threshold creates a new topic, the gray zone between them
	// triggers one reconciliation LLM call.
	defaultMergeThreshold    = 0.90
	defaultNewTopicThreshold = 0.70
	// defaultTopicPromptLimit bounds how many existing topics are shown to
	// the analyzer LLM per request (design D6/D7): BuildPrompt stays a pure
	// formatter, the caller (pipeline) passes an already recency-capped
	// list via Library.ExistingTopicsRecent(n) instead of every topic.
	defaultTopicPromptLimit = 50
)

var (
	defaultVideoExtensions = []string{".mkv", ".mp4", ".mov", ".webm"}
	// validEmbeddingBackends mirrors internal/adapter/embed's compiled-in adapters
	// (design D9 amendment). It is a separate, explicit list here rather
	// than a call into internal/adapter/embed so config validation has no
	// dependency on the embed package, matching the validAnalyzerBackends
	// precedent. Kept in sync by hand as adapters land.
	// go-sentex was dropped (D9 revision 4): its LoadModel() downloads
	// ~87MB of weights from HuggingFace Hub on first call with no way to
	// inject local/embedded bytes, incompatible with the
	// zero-network-after-install requirement (decision 2). zerfoo was
	// investigated (Unit 1c) but never registered: its only exported
	// embedding API mean-pools the static token-embedding table and never
	// runs the transformer, so it cannot produce genuine contextual
	// sentence embeddings. cybertron (Unit 1d) is the sole verified,
	// registered backend.
	validEmbeddingBackends = []string{"cybertron"}
)

// Config is the runtime configuration with all paths resolved.
type Config struct {
	Inbox                    string   // absolute path
	Library                  string   // absolute path
	VideoExtensions          []string // lowercase, leading dot
	StabilityChecks          int
	StabilityIntervalSeconds int
	AnalyzerBackend          string
	EmbeddingBackend         string
	MergeThreshold           float64
	NewTopicThreshold        float64
	TopicPromptLimit         int
	// BinaryPaths maps an analyzer backend name to the path of its CLI.
	// Hosted backends have no entry. Use BinaryPath to read it.
	BinaryPaths map[string]string
	Dir         string // directory of the config file; base for relative paths
	Path        string // canonical or explicitly requested config file
}

// ValidAnalyzerBackends returns the accepted analyzer_backend values. The
// result is a copy so callers cannot corrupt validation.
func ValidAnalyzerBackends() []string {
	return backend.Names()
}

// ValidEmbeddingBackends returns the accepted embedding_backend values. The
// result is a copy so callers cannot corrupt validation.
func ValidEmbeddingBackends() []string {
	return append([]string(nil), validEmbeddingBackends...)
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
	EmbeddingBackend         *string  `yaml:"embedding_backend"`
	MergeThreshold           *float64 `yaml:"merge_threshold"`
	NewTopicThreshold        *float64 `yaml:"new_topic_threshold"`
	TopicPromptLimit         *int     `yaml:"topic_prompt_limit"`
	// Binary paths stay flat, top-level keys — kimi_path, claude_path,
	// codex_path — because they are already written that way in every
	// installed config.yaml. Capturing them inline instead of as named
	// fields means a new backend needs no new field here: the registry
	// alone decides which keys are meaningful.
	Extra map[string]any `yaml:",inline"`
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

// ResolvePath returns the single runtime config path. An explicit --config
// value wins; otherwise the per-user config is canonical. A relative path is
// resolved against the current working directory and a leading ~ is expanded.
func ResolvePath(flagPath string) string {
	path := strings.TrimSpace(flagPath)
	if path == "" {
		path = UserConfigPath()
		if path == "" {
			path = "config.yaml"
		}
	}

	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			path = home
		}
	} else if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// Load reads the canonical config, or the explicitly requested config when
// flagPath is non-empty. Missing keys in the YAML fall back to built-in
// defaults. When a legacy local ./config.yaml is found and the canonical file
// does not exist yet, it is copied to the canonical path once before loading.
func Load(flagPath string) (*Config, error) {
	configPath := ResolvePath(flagPath)
	if flagPath == "" {
		if err := migrateLocalConfig(configPath); err != nil {
			return nil, err
		}
	}

	dir := filepath.Dir(configPath)

	raw := yamlConfig{}
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("patro: cannot parse %s: %w", configPath, err)
		}
	case os.IsNotExist(err):
		// A missing canonical or explicitly requested config uses defaults.
	default:
		return nil, err
	}

	analyzerBackend := strings.ToLower(strings.TrimSpace(stringOr(raw.AnalyzerBackend, backend.Default)))
	if !backend.IsValid(analyzerBackend) {
		return nil, fmt.Errorf(
			"invalid analyzer_backend %q in %s; valid values: %s",
			analyzerBackend, filepath.Base(configPath), strings.Join(backend.Names(), ", "),
		)
	}

	embeddingBackend := strings.ToLower(strings.TrimSpace(stringOr(raw.EmbeddingBackend, defaultEmbeddingBackend)))
	if !validEmbeddingBackend(embeddingBackend) {
		return nil, fmt.Errorf(
			"invalid embedding_backend %q in %s; valid values: %s",
			embeddingBackend, filepath.Base(configPath), strings.Join(validEmbeddingBackends, ", "),
		)
	}

	return &Config{
		Inbox:                    resolvePath(stringOr(raw.Inbox, defaultInbox), dir),
		Library:                  resolvePath(stringOr(raw.Library, defaultLibrary), dir),
		VideoExtensions:          normalizeExtensions(extensionsOr(raw.VideoExtensions)),
		StabilityChecks:          intOr(raw.StabilityChecks, defaultStabilityChecks),
		StabilityIntervalSeconds: intOr(raw.StabilityIntervalSeconds, defaultStabilityIntervalSeconds),
		AnalyzerBackend:          analyzerBackend,
		EmbeddingBackend:         embeddingBackend,
		MergeThreshold:           floatOr(raw.MergeThreshold, defaultMergeThreshold),
		NewTopicThreshold:        floatOr(raw.NewTopicThreshold, defaultNewTopicThreshold),
		TopicPromptLimit:         intOr(raw.TopicPromptLimit, defaultTopicPromptLimit),
		BinaryPaths:              binaryPaths(raw.Extra),
		Dir:                      dir,
		Path:                     configPath,
	}, nil
}

// migrateLocalConfig adopts a legacy repository-local config only when the
// canonical user config has not been created yet. Existing canonical config
// always wins, so two divergent files cannot silently overwrite each other.
func migrateLocalConfig(canonicalPath string) error {
	if _, err := os.Stat(canonicalPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	localPath, err := filepath.Abs("config.yaml")
	if err != nil {
		return err
	}
	if localPath == canonicalPath {
		return nil
	}
	raw, err := os.ReadFile(localPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(canonicalPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(canonicalPath, raw, 0o644)
}

// APIKey returns the AssemblyAI key from the environment or an error whose
// message matches the Python one.
func (c *Config) APIKey() (string, error) {
	key := strings.TrimSpace(os.Getenv(APIKeyEnvVar))
	if key == "" {
		return "", fmt.Errorf(
			"%s is not set; export your AssemblyAI API key, "+
				"e.g.: export %s=<your-key> "+
				"(or use --mock to run without the API)",
			APIKeyEnvVar, APIKeyEnvVar,
		)
	}
	return key, nil
}

// BinaryPath returns the CLI path configured for the named analyzer
// backend, or "" for a hosted backend that has none.
func (c *Config) BinaryPath(name string) string {
	return c.BinaryPaths[name]
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

// SearchIndexDir returns the directory holding the BM25 full-text index
// (design D3, internal/adapter/searchindex). It is a derived, deletable artifact —
// never the source of truth.
func (c *Config) SearchIndexDir() string {
	return filepath.Join(c.StateDir(), "search-index")
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

func floatOr(value *float64, fallback float64) float64 {
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
func binaryPaths(extra map[string]any) map[string]string {
	paths := make(map[string]string)
	for _, b := range backend.All() {
		if b.Hosted {
			continue
		}
		paths[b.Name] = b.DefaultBinary
		raw, ok := extra[b.ConfigKey].(string)
		if !ok {
			continue
		}
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			paths[b.Name] = trimmed
		}
	}
	return paths
}

func validEmbeddingBackend(backend string) bool {
	for _, b := range validEmbeddingBackends {
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
