package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Values are the setup wizard's answers.
type Values struct {
	Inbox      string
	Library    string
	Backend    string
	BinaryPath string
}

// WriteConfig writes the wizard answers as YAML. An existing file is updated
// in place: unknown keys already present are preserved.
func WriteConfig(path string, v Values) error {
	data := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(raw, &data)
	}
	if data == nil {
		data = map[string]any{}
	}
	data["inbox"] = v.Inbox
	data["library"] = v.Library
	data["analyzer_backend"] = v.Backend
	if v.Backend == "kimi" {
		data["kimi_path"] = v.BinaryPath
		delete(data, "claude_path")
	} else {
		data["claude_path"] = v.BinaryPath
		delete(data, "kimi_path")
	}
	return writeYAML(path, data)
}

// SetBackend changes analyzer_backend and, for a CLI backend, its *_path key.
// Every other key in the file is preserved, including the other backend's
// path, so switching back later keeps the previously detected binary.
// binaryPath is ignored when backend is "lemur", which is hosted.
//
// Unlike WriteConfig this is a partial edit of a file the user may have
// written by hand, so a malformed YAML file is an error rather than something
// to silently overwrite.
func SetBackend(path, backend, binaryPath string) error {
	data := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(raw, &data); err != nil {
			return fmt.Errorf("cannot parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if data == nil {
		data = map[string]any{}
	}

	switch backend {
	case "kimi":
		data["kimi_path"] = binaryPath
	case "claude":
		data["claude_path"] = binaryPath
	case "lemur":
		// Hosted: no local binary, and both *_path keys are left untouched.
	default:
		return fmt.Errorf("unknown analyzer backend %q", backend)
	}
	data["analyzer_backend"] = backend

	return writeYAML(path, data)
}

// writeYAML marshals data to path, creating parent directories as needed.
func writeYAML(path string, data map[string]any) error {
	out, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
