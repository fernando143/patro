package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/fernando143/patro/internal/analyzer/backend"
)

// Values are the setup wizard's answers.
//
// The threshold fields are pointers, unlike everything else here: the init
// wizard never collects them, so WriteConfig must leave merge_threshold /
// new_topic_threshold / topic_prompt_limit untouched (whatever is already in
// the file, or config.Load's own defaults) rather than zeroing them out on
// every `patro init` re-run. A nil pointer means "leave it alone".
type Values struct {
	Inbox             string
	Library           string
	Backend           string
	BinaryPath        string
	MergeThreshold    *float64
	NewTopicThreshold *float64
	TopicPromptLimit  *int
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
	// A hosted or unrecognized backend leaves every *_path key alone, which
	// is what the previous per-backend switch did by simply not matching.
	if selected, ok := backend.Get(v.Backend); ok && !selected.Hosted {
		for _, b := range backend.All() {
			if b.Hosted {
				continue
			}
			if b.Name == selected.Name {
				data[b.ConfigKey] = v.BinaryPath
				continue
			}
			delete(data, b.ConfigKey)
		}
	}
	if v.MergeThreshold != nil {
		data["merge_threshold"] = *v.MergeThreshold
	}
	if v.NewTopicThreshold != nil {
		data["new_topic_threshold"] = *v.NewTopicThreshold
	}
	if v.TopicPromptLimit != nil {
		data["topic_prompt_limit"] = *v.TopicPromptLimit
	}
	return writeYAML(path, data)
}

// SetThresholds changes merge_threshold, new_topic_threshold and
// topic_prompt_limit, mirroring SetBackend's partial-edit semantics: every
// other key in the file is preserved, and a malformed existing file is an
// error rather than something to silently overwrite.
func SetThresholds(path string, merge, newTopic float64, promptLimit int) error {
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
	data["merge_threshold"] = merge
	data["new_topic_threshold"] = newTopic
	data["topic_prompt_limit"] = promptLimit
	return writeYAML(path, data)
}

// SetBackend changes analyzer_backend and, for a CLI backend, its *_path key.
// Every other key in the file is preserved, including the other backend's
// path, so switching back later keeps the previously detected binary.
// binaryPath is ignored for a hosted backend such as "lemur".
//
// Unlike WriteConfig this is a partial edit of a file the user may have
// written by hand, so a malformed YAML file is an error rather than something
// to silently overwrite.
func SetBackend(path, name, binaryPath string) error {
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

	selected, ok := backend.Get(name)
	if !ok {
		return fmt.Errorf("unknown analyzer backend %q", name)
	}
	// Unlike WriteConfig this never deletes the other backends' paths: a
	// user who switches away and back keeps the binary already detected for
	// the original backend.
	if !selected.Hosted {
		data[selected.ConfigKey] = binaryPath
	}
	data["analyzer_backend"] = selected.Name

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
