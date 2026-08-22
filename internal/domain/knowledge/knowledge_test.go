package knowledge

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/domain/meeting"
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

func TestNewLibraryFailsWhenASubdirIsBlocked(t *testing.T) {
	root := t.TempDir()
	// topics/ is created first and succeeds; meetings/ is pre-occupied by a
	// plain file, so the loop's second MkdirAll must fail and return early.
	if err := os.WriteFile(filepath.Join(root, "meetings"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := NewLibrary(root); err == nil {
		t.Error("NewLibrary() error = nil, want error when meetings/ cannot be created")
	}
}

func TestAddMeeting(t *testing.T) {
	l := newTestLibrary(t)
	transcript := &meeting.TranscriptResult{
		ID:   "t1",
		Text: "Full transcript text.",
		Utterances: []meeting.Utterance{
			{Speaker: "A", Text: "Hello."},
		},
	}
	analysis := &meeting.AnalysisResult{
		Title:   "Weekly sync",
		Summary: "We discussed the roadmap.",
		Topics: []meeting.Topic{
			{Slug: "roadmap", Name: "Roadmap", Content: "- item one"},
		},
	}

	notePath, err := l.AddMeeting(transcript, analysis, "/inbox/weekly-sync.mkv")
	if err != nil {
		t.Fatalf("AddMeeting: %v", err)
	}
	if _, err := os.Stat(notePath); err != nil {
		t.Errorf("meeting note not written at %s: %v", notePath, err)
	}
	if _, err := os.Stat(filepath.Join(l.TranscriptsDir, "t1.txt")); err != nil {
		t.Errorf("transcript not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.TopicsDir, "roadmap.md")); err != nil {
		t.Errorf("topic file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.Root, "index.md")); err != nil {
		t.Errorf("index not rebuilt: %v", err)
	}
}

func TestAddMeetingPropagatesWriteTranscriptError(t *testing.T) {
	l := newTestLibrary(t)
	if err := os.Chmod(l.TranscriptsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(l.TranscriptsDir, 0o755) })

	_, err := l.AddMeeting(
		&meeting.TranscriptResult{ID: "t1"},
		&meeting.AnalysisResult{Title: "x"},
		"/inbox/x.mkv",
	)
	if err == nil {
		t.Error("AddMeeting() error = nil, want error when the transcript cannot be written")
	}
}

func TestAddMeetingPropagatesWriteMeetingNoteError(t *testing.T) {
	l := newTestLibrary(t)
	if err := os.Chmod(l.MeetingsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(l.MeetingsDir, 0o755) })

	_, err := l.AddMeeting(
		&meeting.TranscriptResult{ID: "t1"},
		&meeting.AnalysisResult{Title: "x"},
		"/inbox/x.mkv",
	)
	if err == nil {
		t.Error("AddMeeting() error = nil, want error when the meeting note cannot be written")
	}
}

func TestAddMeetingPropagatesAppendTopicSectionError(t *testing.T) {
	l := newTestLibrary(t)
	if err := os.Chmod(l.TopicsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(l.TopicsDir, 0o755) })

	_, err := l.AddMeeting(
		&meeting.TranscriptResult{ID: "t1"},
		&meeting.AnalysisResult{
			Title:  "x",
			Topics: []meeting.Topic{{Slug: "roadmap", Name: "Roadmap", Content: "- item"}},
		},
		"/inbox/x.mkv",
	)
	if err == nil {
		t.Error("AddMeeting() error = nil, want error when a topic section cannot be written")
	}
}

func TestAddMeetingPropagatesRebuildIndexError(t *testing.T) {
	l := newTestLibrary(t)
	if err := os.Chmod(l.Root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(l.Root, 0o755) })

	_, err := l.AddMeeting(
		&meeting.TranscriptResult{ID: "t1"},
		&meeting.AnalysisResult{Title: "x"},
		"/inbox/x.mkv",
	)
	if err == nil {
		t.Error("AddMeeting() error = nil, want error when index.md cannot be written")
	}
}

func TestWriteTranscript(t *testing.T) {
	tests := []struct {
		name       string
		transcript *meeting.TranscriptResult
		want       string
	}{
		{
			name: "with utterances",
			transcript: &meeting.TranscriptResult{
				ID: "t1",
				Utterances: []meeting.Utterance{
					{Speaker: "A", Text: "hello there"},
					{Speaker: "B", Text: "general kenobi"},
				},
			},
			want: "Speaker A: hello there\n\nSpeaker B: general kenobi\n",
		},
		{
			name: "without utterances",
			transcript: &meeting.TranscriptResult{
				ID:   "t2",
				Text: "raw transcript text",
			},
			want: "raw transcript text\n",
		},
		{
			name:       "empty",
			transcript: &meeting.TranscriptResult{ID: "t3"},
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
		transcript *meeting.TranscriptResult
		analysis   *meeting.AnalysisResult
		videoPath  string
		date       string
		wantFile   string
		want       string
	}{
		{
			name: "all sections",
			transcript: &meeting.TranscriptResult{
				ID:       "abc123",
				Language: "en",
				Chapters: []meeting.Chapter{
					{Headline: "Intro", Gist: "intro gist", Start: 90000, End: 3723000},
					{Gist: "Wrap-up", Start: 0, End: 5000},
					{Start: 61000, End: 62000},
				},
			},
			analysis: &meeting.AnalysisResult{
				Title:     "Weekly Sync",
				Summary:   "Discussed the roadmap.",
				KeyPoints: []string{"Point one", "Point two"},
				Decisions: []string{"Ship it"},
				ActionItems: []meeting.ActionItem{
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
			transcript: &meeting.TranscriptResult{ID: "x", Language: "es"},
			analysis:   &meeting.AnalysisResult{Title: "Nada"},
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

	topic := meeting.Topic{Slug: "go-migration", Name: "Go Migration", Content: "\n  Migrated the library. \n\n"}
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
	second := meeting.Topic{Slug: "go-migration", Name: "Go Migration", Content: "Second content."}
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
	want := []meeting.TopicRef{
		{Slug: "api-design", Name: "API Design"},
		{Slug: "go-migration", Name: "Go Migration"},
		{Slug: "legacy", Name: "legacy"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ExistingTopics() = %+v, want %+v", got, want)
	}
}

func TestResolveTranscriptID(t *testing.T) {
	l := newTestLibrary(t)

	inLibraryPath := filepath.Join(l.TranscriptsDir, "abc123.txt")
	writeFile(t, inLibraryPath, "Speaker A: hello.\n")

	extDir := t.TempDir()
	extPath := filepath.Join(extDir, "some-recording.txt")
	writeFile(t, extPath, "Speaker A: hello there, this is external.\n")

	id1, external1, err := l.ResolveTranscriptID(inLibraryPath)
	if err != nil {
		t.Fatalf("ResolveTranscriptID(in-library) error = %v", err)
	}
	if external1 {
		t.Error("ResolveTranscriptID(in-library) external = true, want false")
	}
	if id1 != "abc123" {
		t.Errorf("ResolveTranscriptID(in-library) id = %q, want %q", id1, "abc123")
	}

	id2, external2, err := l.ResolveTranscriptID(extPath)
	if err != nil {
		t.Fatalf("ResolveTranscriptID(external) error = %v", err)
	}
	if !external2 {
		t.Error("ResolveTranscriptID(external) external = false, want true")
	}
	if !strings.HasPrefix(id2, "ext-") || len(id2) != len("ext-")+12 {
		t.Errorf("ResolveTranscriptID(external) id = %q, want \"ext-\"+12 hex chars", id2)
	}

	// Stable: re-resolving the same external file yields the same ID.
	id2Again, _, err := l.ResolveTranscriptID(extPath)
	if err != nil {
		t.Fatalf("ResolveTranscriptID(external, second call) error = %v", err)
	}
	if id2Again != id2 {
		t.Errorf("ResolveTranscriptID(external) id = %q on second call, want stable %q", id2Again, id2)
	}

	// A different external file's content yields a different ID.
	extPath2 := filepath.Join(extDir, "other-recording.txt")
	writeFile(t, extPath2, "Speaker B: totally different content.\n")
	id3, _, err := l.ResolveTranscriptID(extPath2)
	if err != nil {
		t.Fatalf("ResolveTranscriptID(external 2) error = %v", err)
	}
	if id3 == id2 {
		t.Errorf("ResolveTranscriptID for different content produced the same id %q", id3)
	}
}

func TestFindMeetingNoteByTranscriptID(t *testing.T) {
	l := newTestLibrary(t)

	found := &meeting.TranscriptResult{ID: "abc123", Language: "en"}
	analysis := &meeting.AnalysisResult{Title: "Weekly Sync"}
	notePath, err := l.WriteMeetingNote(found, analysis, "meeting.mkv", "2026-01-05")
	if err != nil {
		t.Fatalf("WriteMeetingNote setup error = %v", err)
	}

	// A note whose "- **Date:** " line is missing entirely (simulating a
	// malformed/missing date), so lookup must still find it via the
	// transcript link and report an empty Date, never an error.
	malformedPath := filepath.Join(l.MeetingsDir, "2026-01-06-malformed.md")
	writeFile(t, malformedPath, strings.Join([]string{
		"# Malformed",
		"",
		"- **Raw transcript:** [transcript](../transcripts/xyz789.txt)",
		"",
	}, "\n"))

	unreadablePath := filepath.Join(l.MeetingsDir, "2026-01-07-unreadable.md")
	writeFile(t, unreadablePath, "[transcript](../transcripts/unreadable-id.txt)\n")
	if err := os.Chmod(unreadablePath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadablePath, 0o644) })

	t.Run("found", func(t *testing.T) {
		got, err := l.FindMeetingNoteByTranscriptID("abc123")
		if err != nil {
			t.Fatalf("FindMeetingNoteByTranscriptID: %v", err)
		}
		if got == nil {
			t.Fatal("FindMeetingNoteByTranscriptID = nil, want a match")
		}
		if got.Path != notePath {
			t.Errorf("Path = %q, want %q", got.Path, notePath)
		}
		if got.Date != "2026-01-05" {
			t.Errorf("Date = %q, want %q", got.Date, "2026-01-05")
		}
		if got.SourceVideo != "meeting.mkv" {
			t.Errorf("SourceVideo = %q, want %q", got.SourceVideo, "meeting.mkv")
		}
	})

	t.Run("not found", func(t *testing.T) {
		got, err := l.FindMeetingNoteByTranscriptID("does-not-exist")
		if err != nil {
			t.Fatalf("FindMeetingNoteByTranscriptID: %v", err)
		}
		if got != nil {
			t.Errorf("FindMeetingNoteByTranscriptID = %+v, want nil", got)
		}
	})

	t.Run("malformed Date line yields empty Date, no error", func(t *testing.T) {
		got, err := l.FindMeetingNoteByTranscriptID("xyz789")
		if err != nil {
			t.Fatalf("FindMeetingNoteByTranscriptID: %v", err)
		}
		if got == nil {
			t.Fatal("FindMeetingNoteByTranscriptID = nil, want a match (link present)")
		}
		if got.Date != "" {
			t.Errorf("Date = %q, want empty for a missing Date line", got.Date)
		}
	})

	t.Run("unreadable file is skipped, not fatal", func(t *testing.T) {
		got, err := l.FindMeetingNoteByTranscriptID("unreadable-id")
		if err != nil {
			t.Fatalf("FindMeetingNoteByTranscriptID: %v, want nil error (skip unreadable)", err)
		}
		if got != nil {
			t.Errorf("FindMeetingNoteByTranscriptID = %+v, want nil (file was unreadable)", got)
		}
	})
}

func TestWriteMeetingNoteAtOverwritesExactPath(t *testing.T) {
	l := newTestLibrary(t)

	transcript := &meeting.TranscriptResult{ID: "abc123", Language: "en"}
	analysis := &meeting.AnalysisResult{Title: "Old Title"}
	priorPath, err := l.WriteMeetingNote(transcript, analysis, "meeting.mkv", "2026-01-05")
	if err != nil {
		t.Fatalf("WriteMeetingNote setup error = %v", err)
	}

	// The new title's slug differs from the file's own slug ("old-title"),
	// but WriteMeetingNoteAt must still overwrite the exact given path.
	newAnalysis := &meeting.AnalysisResult{Title: "Completely New Title", Summary: "Updated."}
	gotPath, err := l.WriteMeetingNoteAt(priorPath, transcript, newAnalysis, "meeting.mkv", "2026-01-05")
	if err != nil {
		t.Fatalf("WriteMeetingNoteAt: %v", err)
	}
	if gotPath != priorPath {
		t.Errorf("WriteMeetingNoteAt path = %q, want exact prior path %q", gotPath, priorPath)
	}

	// No second file was created alongside it.
	matches, _ := filepath.Glob(filepath.Join(l.MeetingsDir, "*.md"))
	if len(matches) != 1 {
		t.Errorf("meeting notes = %v, want exactly one file", matches)
	}

	content := readFile(t, priorPath)
	if !strings.Contains(content, "# Completely New Title") {
		t.Errorf("content = %q, want the overwritten title", content)
	}
}
