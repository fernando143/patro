// Command promptsync fetches the "production"-labeled versions of the
// analyzer prompt, its response schema, and its conditional fragments from
// Langfuse Prompt Management, writing each to its embedded file under
// internal/adapter/analyzer/.
//
// It exists so all of them can be authored and versioned in Langfuse
// without making the built patro binary depend on Langfuse at runtime:
// this tool runs once, at release time (see
// .github/workflows/release.yml) or on-demand during development, and its
// output is plain files compiled into the binary via go:embed.
// internal/adapter/analyzer.TestResponseSchemaMatchesParser and
// TestPromptFragmentsRetainPlaceholders are the build-time guards against
// a Langfuse edit drifting from what the Go code actually reads.
//
// Required environment variables:
//   - LANGFUSE_BASE_URL (e.g. https://cloud.langfuse.com)
//   - LANGFUSE_PUBLIC_KEY
//   - LANGFUSE_SECRET_KEY
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const promptLabel = "production"

// artifacts pairs each Langfuse prompt with the embedded file it fills.
var artifacts = []struct {
	promptName string
	destPath   string
}{
	{"meeting-summary-analyzer", "internal/adapter/analyzer/prompt_template.txt"},
	{"meeting-summary-schema", "internal/adapter/analyzer/response_schema.json"},
	{"meeting-summary-topics-with-existing", "internal/adapter/analyzer/prompt_fragments/topics_with_existing.txt"},
	{"meeting-summary-topics-empty", "internal/adapter/analyzer/prompt_fragments/topics_empty.txt"},
	{"meeting-summary-source-with-path", "internal/adapter/analyzer/prompt_fragments/source_with_path.txt"},
	{"meeting-summary-source-without-path", "internal/adapter/analyzer/prompt_fragments/source_without_path.txt"},
	{"meeting-summary-language-known", "internal/adapter/analyzer/prompt_fragments/language_known.txt"},
	{"meeting-summary-language-unknown", "internal/adapter/analyzer/prompt_fragments/language_unknown.txt"},
}

type promptResponse struct {
	Prompt string `json:"prompt"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "promptsync: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	host := os.Getenv("LANGFUSE_BASE_URL")
	publicKey := os.Getenv("LANGFUSE_PUBLIC_KEY")
	secretKey := os.Getenv("LANGFUSE_SECRET_KEY")
	if host == "" || publicKey == "" || secretKey == "" {
		return fmt.Errorf("LANGFUSE_BASE_URL, LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY must all be set")
	}

	for _, a := range artifacts {
		if err := syncPrompt(host, publicKey, secretKey, a.promptName, a.destPath); err != nil {
			return fmt.Errorf("syncing %q: %w", a.promptName, err)
		}
	}
	return nil
}

func syncPrompt(host, publicKey, secretKey, promptName, destPath string) error {
	url := fmt.Sprintf("%s/api/public/v2/prompts/%s?label=%s", host, promptName, promptLabel)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.SetBasicAuth(publicKey, secretKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching prompt: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("langfuse returned %s: %s", resp.Status, body)
	}

	var pr promptResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	if pr.Prompt == "" {
		return fmt.Errorf("langfuse response had an empty prompt field")
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(destPath), err)
	}
	if err := os.WriteFile(destPath, []byte(pr.Prompt), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}

	fmt.Printf("promptsync: wrote %s (%d bytes) from Langfuse label %q\n", destPath, len(pr.Prompt), promptLabel)
	return nil
}
