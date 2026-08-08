// Package analyzer distills meeting transcripts into structured knowledge.
//
// Every backend answers the same prompt (BuildPrompt) with a strict JSON
// payload: meeting metadata (title, summary, key points, decisions, action
// items) and a list of knowledge topics with distilled Markdown content.
// The prompt includes the topics already present in the library so the
// model reuses existing slugs when the subject matches and only creates
// new slugs for genuinely new subjects.
//
// Parsing is defensive: the JSON block is extracted even when wrapped in
// prose or ``` fences; on total failure ParseAnalysis falls back to a
// minimal result with a single "general" topic holding the raw response.
//
// This is a port of scribe/analyzer.py (prompt builder and shared parser).
package analyzer

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/types"
)

// promptTemplate is the versioned prompt skeleton. It is authored and
// versioned in Langfuse Prompt Management; tools/promptsync fetches the
// "production"-labeled version and overwrites this file at release time
// (see .github/workflows/release.yml), so the built binary never talks to
// Langfuse itself. The {{source}}/{{topics}}/{{schema}}/{{language_rule}}
// placeholders are filled in by BuildPrompt below.
//
//go:embed prompt_template.txt
var promptTemplate string

// rawResponseSchema is the JSON schema example embedded verbatim in the
// prompt; the backends are instructed to match it exactly. Like
// promptTemplate, it is authored and versioned in Langfuse (as a separate
// prompt, so it can be validated on its own — see
// TestResponseSchemaMatchesParser) and synced by tools/promptsync.
//
//go:embed response_schema.json
var rawResponseSchema string

// responseSchema drops the file's trailing newline so substituting it into
// promptTemplate reproduces the exact prompt text the analyzer contract
// tests were pinned against.
var responseSchema = strings.TrimRight(rawResponseSchema, "\n")

// promptFragments holds the copy for BuildPrompt's conditional blocks —
// each is a separate Langfuse prompt (see tools/promptsync) because each
// condition (existing topics or not, transcript path or not, known
// language or not) needs its own independent wording, not one template
// with branches. Go still decides WHICH fragment applies; Langfuse only
// controls what each one says.
//
//go:embed prompt_fragments
var promptFragments embed.FS

// fragment reads and trims one file from promptFragments. Failure here
// means a fragment file is missing from the embedded build — a build-time
// defect, not a runtime condition, hence the panic.
func fragment(name string) string {
	data, err := promptFragments.ReadFile("prompt_fragments/" + name)
	if err != nil {
		panic("analyzer: missing embedded prompt fragment " + name + ": " + err.Error())
	}
	return strings.TrimRight(string(data), "\n")
}

var (
	topicsWithExistingFragment = fragment("topics_with_existing.txt")
	topicsEmptyFragment        = fragment("topics_empty.txt")
	sourceWithPathFragment     = fragment("source_with_path.txt")
	sourceWithoutPathFragment  = fragment("source_without_path.txt")
	languageKnownFragment      = fragment("language_known.txt")
	languageUnknownFragment    = fragment("language_unknown.txt")
)

// fencePattern matches a wrapping ```json ... ``` (or plain ``` ... ```)
// fence, capturing its body.
var fencePattern = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// BuildPrompt builds the analyzer prompt. existing holds {slug, name} refs
// of the topics already present in the library.
//
// When transcriptPath is given, the prompt instructs the model to read the
// transcript from that file (used by the CLI backends, where embedding the
// whole transcript in the prompt would risk ARG_MAX on long meetings).
//
// The output is byte-for-byte identical to scribe/analyzer.py's
// build_prompt.
func BuildPrompt(existing []types.TopicRef, language, transcriptPath string) string {
	topicsBlock := topicsEmptyFragment
	if len(existing) > 0 {
		lines := make([]string, len(existing))
		for i, t := range existing {
			lines[i] = fmt.Sprintf("- %s: %s", t.Slug, t.Name)
		}
		topicsBlock = strings.NewReplacer("{{topics_list}}", strings.Join(lines, "\n")).
			Replace(topicsWithExistingFragment)
	}

	sourceBlock := sourceWithoutPathFragment
	if transcriptPath != "" {
		sourceBlock = strings.NewReplacer("{{transcript_path}}", transcriptPath).
			Replace(sourceWithPathFragment)
	}

	languageRule := languageUnknownFragment
	if language != "" && language != "unknown" {
		languageRule = strings.NewReplacer("{{language}}", fmt.Sprintf("%q", language)).
			Replace(languageKnownFragment)
	}

	replacer := strings.NewReplacer(
		"{{source}}", sourceBlock,
		"{{topics}}", topicsBlock,
		"{{schema}}", responseSchema,
		"{{language_rule}}", languageRule,
	)
	return replacer.Replace(promptTemplate)
}

// ParseAnalysis parses a raw analyzer text response into an AnalysisResult.
//
// Shared by every backend. It never fails on malformed input: it logs a
// warning and falls back to a minimal result with a single "general" topic
// holding the raw response (trimmed to 2000 characters).
func ParseAnalysis(raw, backendName string) *types.AnalysisResult {
	payload, err := extractJSON(raw)
	if err == nil {
		var result *types.AnalysisResult
		if result, err = parseResponse(payload); err == nil {
			return result
		}
	}
	logging.Warnf("%s response could not be parsed (%s); using minimal fallback", backendName, err)

	summary := strings.TrimSpace(raw)
	if runes := []rune(summary); len(runes) > 2000 {
		summary = string(runes[:2000])
	}
	if summary == "" {
		summary = "Analysis failed to produce a parseable result."
	}
	return &types.AnalysisResult{
		Title:   "Untitled meeting",
		Summary: summary,
		Topics:  []types.Topic{{Slug: "general", Name: "General", Content: summary}},
	}
}

// extractJSON pulls the first JSON object out of text, tolerating fences
// and surrounding prose. Mirrors scribe/analyzer.py's _extract_json.
func extractJSON(text string) (map[string]any, error) {
	cleaned := strings.TrimSpace(text)
	// Strip a wrapping ```json ... ``` fence if present.
	if fence := fencePattern.FindStringSubmatch(cleaned); fence != nil {
		cleaned = strings.TrimSpace(fence[1])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(cleaned), &payload); err == nil && payload != nil {
		return payload, nil
	}
	// Fall back to the first {...} block in the text.
	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start != -1 && end > start {
		if err := json.Unmarshal([]byte(cleaned[start:end+1]), &payload); err != nil {
			return nil, fmt.Errorf("no parseable JSON object in analyzer response: %w", err)
		}
		return payload, nil
	}
	return nil, fmt.Errorf("no JSON object found in analyzer response")
}

// parseResponse converts the decoded JSON payload into an AnalysisResult.
// Mirrors scribe/analyzer.py's _parse_response.
func parseResponse(payload map[string]any) (*types.AnalysisResult, error) {
	meeting, ok := payload["meeting"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing 'meeting' object in LeMUR response")
	}

	var actionItems []types.ActionItem
	if items, ok := meeting["action_items"].([]any); ok {
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				owner := "unassigned"
				if rawOwner, exists := obj["owner"]; exists {
					owner = pyStr(rawOwner)
				}
				task := ""
				if rawTask, exists := obj["task"]; exists {
					task = pyStr(rawTask)
				}
				actionItems = append(actionItems, types.ActionItem{Owner: owner, Task: task})
			} else if isTruthy(item) {
				actionItems = append(actionItems, types.ActionItem{Owner: "unassigned", Task: pyStr(item)})
			}
		}
	}

	var topics []types.Topic
	if list, ok := payload["topics"].([]any); ok {
		for _, item := range list {
			obj, ok := item.(map[string]any)
			if !ok || !isTruthy(obj["slug"]) {
				continue
			}
			slug := pyStr(obj["slug"])
			name := slug
			if isTruthy(obj["name"]) {
				name = pyStr(obj["name"])
			}
			content := ""
			if isTruthy(obj["content"]) {
				content = pyStr(obj["content"])
			}
			topics = append(topics, types.Topic{Slug: slug, Name: name, Content: content})
		}
	}

	title := "Untitled meeting"
	if isTruthy(meeting["title"]) {
		title = pyStr(meeting["title"])
	}
	summary := ""
	if isTruthy(meeting["summary"]) {
		summary = pyStr(meeting["summary"])
	}

	return &types.AnalysisResult{
		Title:       title,
		Summary:     summary,
		KeyPoints:   asStrList(meeting["key_points"]),
		Decisions:   asStrList(meeting["decisions"]),
		ActionItems: actionItems,
		Topics:      topics,
	}, nil
}

// asStrList returns the items of value as strings, or nil when value is
// not a JSON array. Mirrors _as_str_list.
func asStrList(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, pyStr(item))
	}
	return out
}

// isTruthy mirrors Python truthiness for decoded JSON values.
func isTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

// pyStr renders a decoded JSON scalar the way Python's str() would.
func pyStr(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprintf("%v", v)
	}
}
