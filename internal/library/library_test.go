package library

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/types"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain words", "Hello World", "hello-world"},
		{"mayúsculas", "REUNIÓN SEMANAL", "reunion-semanal"},
		{"accents", "Reunión de Planificación", "reunion-de-planificacion"},
		{"símbolos", "Símbolos & más: cosas!", "simbolos-mas-cosas"},
		{"eñe", "Ñandú", "nandu"},
		{"café y té", "Café & té", "cafe-te"},
		{"mixed digits", "Sprint 42 Review", "sprint-42-review"},
		{"repeated separators", "a--b__c  d", "a-b-c-d"},
		{"surrounding dashes", "--ya--", "ya"},
		{"non-ascii only", "日本語テスト", "untitled"},
		{"symbols only", "!!!", "untitled"},
		{"empty", "", "untitled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slugify(tt.input); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func newTestLibrary(t *testing.T) *Library {
	t.Helper()
	l, err := NewLibrary(t.TempDir())
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	return l
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestWriteTranscript(t *testing.T) {
	tests := []struct {
		name       string
		transcript *types.TranscriptResult
		want       string
	}{
		{
			name: "with utterances",
			transcript: &types.TranscriptResult{
				ID: "t1",
				Utterances: []types.Utterance{
					{Speaker: "A", Text: "hello there"},
					{Speaker: "B", Text: "general kenobi"},
				},
			},
			want: "Speaker A: hello there\n\nSpeaker B: general kenobi\n",
		},
		{
			name: "without utterances",
			transcript: &types.TranscriptResult{
				ID:   "t2",
				Text: "raw transcript text",
			},
			want: "raw transcript text\n",
		},
		{
			name:       "empty",
			transcript: &types.TranscriptResult{ID: "t3"},
			want:       "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLibrary(t)
			path, err := l.WriteTranscript(tt.transcript)
			if err != nil {
				t.Fatalf("WriteTranscript: %v", err)
			}
			wantPath := filepath.Join(l.TranscriptsDir, tt.transcript.ID+".txt")
			if path != wantPath {
				t.Errorf("path = %q, want %q", path, wantPath)
			}
			if got := readFile(t, path); got != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteMeetingNote(t *testing.T) {
	tests := []struct {
		name       string
		transcript *types.TranscriptResult
		analysis   *types.AnalysisResult
		videoPath  string
		date       string
		wantFile   string
		want       string
	}{
		{
			name: "all sections",
			transcript: &types.TranscriptResult{
				ID:       "abc123",
				Language: "en",
				Chapters: []types.Chapter{
					{Headline: "Intro", Gist: "intro gist", Start: 90000, End: 3723000},
					{Gist: "Wrap-up", Start: 0, End: 5000},
					{Start: 61000, End: 62000},
				},
			},
			analysis: &types.AnalysisResult{
				Title:     "Weekly Sync",
				Summary:   "Discussed the roadmap.",
				KeyPoints: []string{"Point one", "Point two"},
				Decisions: []string{"Ship it"},
				ActionItems: []types.ActionItem{
					{Owner: "Ana", Task: "Write docs"},
					{Owner: "Bob", Task: "Fix bug"},
				},
			},
			videoPath: "/home/user/Videos/obs/weekly.mkv",
			date:      "2026-07-17",
			wantFile:  "2026-07-17-weekly-sync.md",
			want: strings.Join([]string{
				"# Weekly Sync",
				"",
				"- **Date:** 2026-07-17",
				"- **Source video:** `weekly.mkv`",
				"- **Language:** en",
				"- **Raw transcript:** [transcript](../transcripts/abc123.txt)",
				"",
				"## Summary",
				"",
				"Discussed the roadmap.",
				"",
				"## Key points",
				"",
				"- Point one",
				"- Point two",
				"",
				"## Decisions",
				"",
				"- Ship it",
				"",
				"## Action items",
				"",
				"- [ ] **Ana**: Write docs",
				"- [ ] **Bob**: Fix bug",
				"",
				"## Chapters",
				"",
				"- `01:30–1:02:03` Intro",
				"- `00:00–00:05` Wrap-up",
				"- `01:01–01:02` Chapter",
				"",
			}, "\n"),
		},
		{
			name:       "no optional sections",
			transcript: &types.TranscriptResult{ID: "x", Language: "es"},
			analysis:   &types.AnalysisResult{Title: "Nada"},
			videoPath:  "v.mkv",
			date:       "2026-01-02",
			wantFile:   "2026-01-02-nada.md",
			want: strings.Join([]string{
				"# Nada",
				"",
				"- **Date:** 2026-01-02",
				"- **Source video:** `v.mkv`",
				"- **Language:** es",
				"- **Raw transcript:** [transcript](../transcripts/x.txt)",
				"",
				"## Summary",
				"",
				"(no summary)",
				"",
			}, "\n"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLibrary(t)
			path, err := l.WriteMeetingNote(tt.transcript, tt.analysis, tt.videoPath, tt.date)
			if err != nil {
				t.Fatalf("WriteMeetingNote: %v", err)
			}
			wantPath := filepath.Join(l.MeetingsDir, tt.wantFile)
			if path != wantPath {
				t.Errorf("path = %q, want %q", path, wantPath)
			}
			if got := readFile(t, path); got != tt.want {
				t.Errorf("content mismatch\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestAppendTopicSection(t *testing.T) {
	l := newTestLibrary(t)

	topic := types.Topic{Slug: "go-migration", Name: "Go Migration", Content: "\n  Migrated the library. \n\n"}
	path, err := l.AppendTopicSection(topic, "2026-07-17", "Weekly Sync", "/x/meetings/2026-07-17-weekly-sync.md")
	if err != nil {
		t.Fatalf("AppendTopicSection: %v", err)
	}
	wantPath := filepath.Join(l.TopicsDir, "go-migration.md")
	if path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}

	want := "# Go Migration\n" +
		"\n## 2026-07-17 — Weekly Sync\n\n" +
		"Migrated the library.\n\n" +
		"*Source: [Weekly Sync](../meetings/2026-07-17-weekly-sync.md)*\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("after create\ngot:\n%s\nwant:\n%s", got, want)
	}

	// Appending to an existing file keeps prior content and adds a section.
	second := types.Topic{Slug: "go-migration", Name: "Go Migration", Content: "Second content."}
	if _, err := l.AppendTopicSection(second, "2026-07-18", "Daily", "/x/meetings/2026-07-18-daily.md"); err != nil {
		t.Fatalf("AppendTopicSection (append): %v", err)
	}
	want += "\n## 2026-07-18 — Daily\n\n" +
		"Second content.\n\n" +
		"*Source: [Daily](../meetings/2026-07-18-daily.md)*\n"
	if got := readFile(t, path); got != want {
		t.Errorf("after append\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRebuildIndex(t *testing.T) {
	l := newTestLibrary(t)

	writeFile(t, filepath.Join(l.TopicsDir, "go-migration.md"),
		"# Go Migration\n\n## 2026-07-10 — Alpha\n\nstuff\n\n## 2026-07-17 — Beta\n\nmore\n")
	writeFile(t, filepath.Join(l.TopicsDir, "api-design.md"),
		"# API Design\n\nNo dated sections yet.\n")
	writeFile(t, filepath.Join(l.TopicsDir, "legacy.md"),
		"no heading here\n\n## 2026-01-05 — Old\n")

	writeFile(t, filepath.Join(l.MeetingsDir, "2026-07-15-alpha.md"), "# Alpha\n")
	writeFile(t, filepath.Join(l.MeetingsDir, "2026-07-17-beta.md"), "# Beta\n")
	writeFile(t, filepath.Join(l.MeetingsDir, "2026-07-16-notitle.md"), "no heading\n")

	path, err := l.RebuildIndex()
	if err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	if wantPath := filepath.Join(l.Root, "index.md"); path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}

	want := strings.Join([]string{
		"# Knowledge library",
		"",
		"## Topics",
		"",
		"- [API Design](topics/api-design.md)",
		"- [Go Migration](topics/go-migration.md) — last updated 2026-07-17",
		"- [legacy](topics/legacy.md) — last updated 2026-01-05",
		"",
		"## Meetings",
		"",
		"- [2026-07-17-beta](meetings/2026-07-17-beta.md) — Beta",
		"- [2026-07-16-notitle](meetings/2026-07-16-notitle.md) — 2026-07-16-notitle",
		"- [2026-07-15-alpha](meetings/2026-07-15-alpha.md) — Alpha",
		"",
	}, "\n")
	if got := readFile(t, path); got != want {
		t.Errorf("index mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRebuildIndexEmpty(t *testing.T) {
	l := newTestLibrary(t)
	path, err := l.RebuildIndex()
	if err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	want := strings.Join([]string{
		"# Knowledge library",
		"",
		"## Topics",
		"",
		"(no topics yet)",
		"",
		"## Meetings",
		"",
		"(no meetings yet)",
		"",
	}, "\n")
	if got := readFile(t, path); got != want {
		t.Errorf("index mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestExistingTopics(t *testing.T) {
	l := newTestLibrary(t)

	writeFile(t, filepath.Join(l.TopicsDir, "go-migration.md"), "# Go Migration\n")
	writeFile(t, filepath.Join(l.TopicsDir, "api-design.md"), "# API Design\n\n## 2026-07-10 — Alpha\n")
	writeFile(t, filepath.Join(l.TopicsDir, "legacy.md"), "no heading here\n")

	got := l.ExistingTopics()
	want := []types.TopicRef{
		{Slug: "api-design", Name: "API Design"},
		{Slug: "go-migration", Name: "Go Migration"},
		{Slug: "legacy", Name: "legacy"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ExistingTopics() = %+v, want %+v", got, want)
	}
}
