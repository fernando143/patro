//go:build e2e

// Package e2e runs patro's analyzer against real LLM CLIs (Suite 1:
// Transcript -> Meeting Summary). See README.md for the suite design.
//
// These tests shell out to the real `claude`/`codex` binaries — no mocks,
// no skip-on-missing-binary, no env-var gate. Any failure (missing binary,
// timeout, non-zero exit, unparseable response, or a missing checklist
// fact) fails the test immediately.
//
// Gated behind the "e2e" build tag (not run by plain `go test ./...`, so
// CI — which has neither CLI installed — never touches it) because that
// fail-fast policy is the point: these tests must not degrade into a
// silent skip just because a binary or credential is missing. Run with
// `go test -tags e2e ./test/e2e/...`.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/analyzer"
	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/types"
	"gopkg.in/yaml.v3"
)

// summaryCase is one row of the Suite 1 table: a fixture case-id run
// against one analyzer backend.
type summaryCase struct {
	name    string
	caseID  string
	backend string
}

var summaryCases = []summaryCase{
	{"simple-short/claude", "simple-short", "claude"},
	{"simple-short/codex", "simple-short", "codex"},
	{"multi-topic-long/claude", "multi-topic-long", "claude"},
	{"multi-topic-long/codex", "multi-topic-long", "codex"},
	{"crossing-speakers/claude", "crossing-speakers", "claude"},
	{"crossing-speakers/codex", "crossing-speakers", "codex"},
	{"noisy-incomplete/claude", "noisy-incomplete", "claude"},
	{"noisy-incomplete/codex", "noisy-incomplete", "codex"},
	{"mixed-language-jargon/claude", "mixed-language-jargon", "claude"},
	{"mixed-language-jargon/codex", "mixed-language-jargon", "codex"},
}

// summaryChecklist is the golden reference for one case:
// fixtures/summaries/<case-id>/checklist.yaml.
//
// Keywords are hand-curated by the fixture author, not derived
// automatically (no stemming, no embeddings): only a human who wrote the
// fixture can tell apart "the team postponed the release" (decided) from
// "the team discussed postponing the release" (merely discussed), and
// list the word forms a real LLM might legitimately use for the former.
type summaryChecklist struct {
	ExpectedTopicKeywords [][]string        `yaml:"expected_topic_keywords"`
	Decisions             []checklistFact   `yaml:"decisions"`
	ActionItems           []checklistAction `yaml:"action_items"`
	KeyFacts              []checklistFact   `yaml:"key_facts"`
}

// checklistFact is one atomic fact: a human-readable description (for
// error messages) plus its required keyword groups. Keywords is a list of
// concept groups (AND across groups: every concept must be present) where
// each group lists interchangeable word forms for that one concept (OR
// within a group: any single form counts) — e.g. [["postergar", "posterga",
// "postergó"], ["pagos"]] requires some form of "postergar" AND "pagos".
type checklistFact struct {
	Text     string     `yaml:"text"`
	Keywords [][]string `yaml:"keywords"`
}

type checklistAction struct {
	Text     string     `yaml:"text"`
	Owner    string     `yaml:"owner"`
	Keywords [][]string `yaml:"keywords"`
}

func TestSummaryGeneration(t *testing.T) {
	for _, tc := range summaryCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transcript := loadTranscriptFixture(t, tc.caseID)
			want := loadSummaryChecklist(t, tc.caseID)

			cfg := &config.Config{
				AnalyzerBackend: tc.backend,
				ClaudePath:      "claude",
				CodexPath:       "codex",
				Dir:             t.TempDir(),
			}

			got, err := analyzer.AnalyzeCLI(context.Background(), transcript, nil, cfg)
			if err != nil {
				t.Fatalf("%s AnalyzeCLI(%s): %v", tc.backend, tc.caseID, err)
			}
			saveAnalysisOutput(t, tc, got)

			assertSummaryChecklist(t, tc, want, got)
		})
	}
}

func loadTranscriptFixture(t *testing.T, caseID string) *types.TranscriptResult {
	t.Helper()
	path := filepath.Join("fixtures", "transcripts", caseID, "transcript.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading transcript fixture %s: %v", path, err)
	}
	return &types.TranscriptResult{ID: caseID, Text: string(data)}
}

func loadSummaryChecklist(t *testing.T, caseID string) summaryChecklist {
	t.Helper()
	path := filepath.Join("fixtures", "summaries", caseID, "checklist.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading checklist fixture %s: %v", path, err)
	}
	var c summaryChecklist
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatalf("parsing checklist fixture %s: %v", path, err)
	}
	return c
}

// outputDir holds the full raw AnalysisResult per case/backend, for manual
// review — the report only surfaces the specific fields each check
// compared, not the complete generated output (e.g. full topic content).
const outputDir = "output"

func saveAnalysisOutput(t *testing.T, tc summaryCase, got *types.AnalysisResult) {
	t.Helper()
	dir := filepath.Join(outputDir, tc.caseID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating output dir %s: %v", dir, err)
	}
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshaling analysis output for %s/%s: %v", tc.caseID, tc.backend, err)
	}
	path := filepath.Join(dir, tc.backend+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing analysis output %s: %v", path, err)
	}
}

func assertSummaryChecklist(t *testing.T, tc summaryCase, want summaryChecklist, got *types.AnalysisResult) {
	t.Helper()

	topicsHaystack := strings.Join(topicSlugs(got.Topics), " ")
	topicOK := keywordsPresent(want.ExpectedTopicKeywords, topicsHaystack)
	recordCheck(tc, "expected_topic", "expected_topic_keywords", want.ExpectedTopicKeywords, topicsHaystack, topicOK)
	if !topicOK {
		t.Errorf("expected_topic_keywords %v not found among generated topics %v", want.ExpectedTopicKeywords, topicSlugs(got.Topics))
	}

	decisionsText := strings.Join(got.Decisions, "\n")
	for _, fact := range want.Decisions {
		ok := keywordsPresent(fact.Keywords, decisionsText)
		recordCheck(tc, "decision", fact.Text, fact.Keywords, decisionsText, ok)
		if !ok {
			t.Errorf("decision not found in output: %q (keywords %v)\ngenerated decisions: %v", fact.Text, fact.Keywords, got.Decisions)
		}
	}

	for _, item := range want.ActionItems {
		ownerItems := actionItemsByOwner(got.ActionItems, item.Owner)
		if len(ownerItems) == 0 {
			recordCheck(tc, "action_item", item.Text, item.Keywords, fmt.Sprintf("no action item for owner %q; generated action items: %v", item.Owner, got.ActionItems), false)
			t.Errorf("action item owner %q not found among generated action items: %v", item.Owner, got.ActionItems)
			continue
		}

		tasks := make([]string, len(ownerItems))
		ok := false
		for i, oi := range ownerItems {
			tasks[i] = oi.Task
			if keywordsPresent(item.Keywords, oi.Task) {
				ok = true
			}
		}
		recordCheck(tc, "action_item", item.Text, item.Keywords, strings.Join(tasks, " | "), ok)
		if !ok {
			t.Errorf("action item task for owner %q not found among their %d task(s): want %q (keywords %v), got %v", item.Owner, len(ownerItems), item.Text, item.Keywords, tasks)
		}
	}

	fullText := analysisText(got)
	for _, fact := range want.KeyFacts {
		ok := keywordsPresent(fact.Keywords, fullText)
		recordCheck(tc, "key_fact", fact.Text, fact.Keywords, fullText, ok)
		if !ok {
			t.Errorf("key fact not found in output: %q (keywords %v)", fact.Text, fact.Keywords)
		}
	}
}

func topicSlugs(topics []types.Topic) []string {
	slugs := make([]string, len(topics))
	for i, tp := range topics {
		slugs[i] = tp.Slug
	}
	return slugs
}

// actionItemsByOwner returns every action item assigned to owner — not
// just the first — since a real LLM can legitimately give one person more
// than one task, and the checklist-relevant one isn't guaranteed to be
// first in the list. Owner names are matched case-insensitively: the
// analyzer copies them near-verbatim from the transcript (see the "owner"
// field in analyzer.BuildPrompt's schema), so they aren't paraphrased the
// way free-text facts are.
func actionItemsByOwner(items []types.ActionItem, owner string) []types.ActionItem {
	var matches []types.ActionItem
	for _, it := range items {
		if strings.EqualFold(strings.TrimSpace(it.Owner), strings.TrimSpace(owner)) {
			matches = append(matches, it)
		}
	}
	return matches
}

func analysisText(a *types.AnalysisResult) string {
	var b strings.Builder
	b.WriteString(a.Title)
	b.WriteString("\n")
	b.WriteString(a.Summary)
	b.WriteString("\n")
	b.WriteString(strings.Join(a.KeyPoints, "\n"))
	for _, tp := range a.Topics {
		b.WriteString("\n")
		b.WriteString(tp.Content)
	}
	return b.String()
}

// keywordsPresent reports whether haystack satisfies every concept group:
// each group needs at least one of its word-form alternatives present
// (case-insensitive substring). Groups come hand-picked from the
// checklist fixture, not derived automatically — see summaryChecklist's
// doc comment for why.
func keywordsPresent(groups [][]string, haystack string) bool {
	haystack = strings.ToLower(haystack)
	for _, alternatives := range groups {
		if !anyPresent(alternatives, haystack) {
			return false
		}
	}
	return true
}

func anyPresent(alternatives []string, lowerHaystack string) bool {
	for _, kw := range alternatives {
		if strings.Contains(lowerHaystack, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
