// Package library writes the Markdown knowledge library.
//
// Layout under the configured library root:
//
//	knowledge/
//	├── topics/<slug>.md                  # one file per topic, appended over time
//	├── meetings/<YYYY-MM-DD>-<slug>.md   # full note per processed meeting
//	├── transcripts/<transcript_id>.txt   # raw transcript with speakers
//	└── index.md                          # regenerated on every run
//
// Topic files grow by appending a dated section per meeting; meeting notes
// and raw transcripts are written once; the index is rebuilt from scratch
// each run. The generated Markdown is byte-for-byte identical to the legacy
// Python writer (scribe/library.py).
package library

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/fernando143/patro/internal/types"
)

var (
	// slugTransformer mirrors Python's NFKD + encode("ascii", "ignore"):
	// decompose, drop combining marks, then drop any remaining non-ASCII.
	slugTransformer = transform.Chain(
		norm.NFKD,
		runes.Remove(runes.In(unicode.Mn)),
		runes.Map(func(r rune) rune {
			if r > unicode.MaxASCII {
				return -1
			}
			return r
		}),
	)
	nonSlugChars  = regexp.MustCompile(`[^a-z0-9]+`)
	sectionDateRe = regexp.MustCompile(`(?m)^## (\d{4}-\d{2}-\d{2})`)
)

// Slugify converts text to a lowercase kebab-case slug, accents stripped
// (ASCII only). An empty result becomes "untitled".
func Slugify(s string) string {
	ascii, _, _ := transform.String(slugTransformer, s)
	slug := nonSlugChars.ReplaceAllString(strings.ToLower(ascii), "-")
	if slug = strings.Trim(slug, "-"); slug != "" {
		return slug
	}
	return "untitled"
}

// formatTimestamp renders milliseconds as MM:SS, or H:MM:SS once the
// duration reaches one hour.
func formatTimestamp(ms int64) string {
	totalSeconds := ms / 1000
	seconds := totalSeconds % 60
	minutes := (totalSeconds / 60) % 60
	hours := totalSeconds / 3600
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

// Library writes transcripts, meeting notes, topic files and the index
// under a root directory.
type Library struct {
	Root           string
	TopicsDir      string
	MeetingsDir    string
	TranscriptsDir string
}

// NewLibrary creates the library directory layout under root and returns
// the Library handle.
func NewLibrary(root string) (*Library, error) {
	l := &Library{
		Root:           root,
		TopicsDir:      filepath.Join(root, "topics"),
		MeetingsDir:    filepath.Join(root, "meetings"),
		TranscriptsDir: filepath.Join(root, "transcripts"),
	}
	for _, d := range []string{l.TopicsDir, l.MeetingsDir, l.TranscriptsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// ExistingTopics lists a {slug, name} reference for every topic file, used
// by the analyzer.
func (l *Library) ExistingTopics() []types.TopicRef {
	files, err := filepath.Glob(filepath.Join(l.TopicsDir, "*.md"))
	if err != nil {
		return nil
	}
	topics := make([]types.TopicRef, 0, len(files))
	for _, path := range files {
		slug := stem(path)
		name, _ := topicInfo(path, slug)
		topics = append(topics, types.TopicRef{Slug: slug, Name: name})
	}
	return topics
}

// WriteTranscript writes the raw transcript with speaker labels, one
// utterance per paragraph, and returns its path.
func (l *Library) WriteTranscript(t *types.TranscriptResult) (string, error) {
	path := filepath.Join(l.TranscriptsDir, t.ID+".txt")
	var lines []string
	if len(t.Utterances) > 0 {
		lines = make([]string, 0, len(t.Utterances))
		for _, u := range t.Utterances {
			lines = append(lines, "Speaker "+u.Speaker+": "+u.Text)
		}
	} else if t.Text != "" {
		lines = []string{t.Text}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n\n")+"\n"), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// WriteMeetingNote writes the full meeting note and returns the path of
// the created file.
func (l *Library) WriteMeetingNote(t *types.TranscriptResult, a *types.AnalysisResult, videoPath, date string) (string, error) {
	path := filepath.Join(l.MeetingsDir, date+"-"+Slugify(a.Title)+".md")

	summary := a.Summary
	if summary == "" {
		summary = "(no summary)"
	}

	lines := []string{
		"# " + a.Title,
		"",
		"- **Date:** " + date,
		"- **Source video:** `" + filepath.Base(videoPath) + "`",
		"- **Language:** " + t.Language,
		"- **Raw transcript:** [transcript](../transcripts/" + t.ID + ".txt)",
		"",
		"## Summary",
		"",
		summary,
		"",
	}

	if len(a.KeyPoints) > 0 {
		lines = append(lines, "## Key points", "")
		for _, point := range a.KeyPoints {
			lines = append(lines, "- "+point)
		}
		lines = append(lines, "")
	}

	if len(a.Decisions) > 0 {
		lines = append(lines, "## Decisions", "")
		for _, decision := range a.Decisions {
			lines = append(lines, "- "+decision)
		}
		lines = append(lines, "")
	}

	if len(a.ActionItems) > 0 {
		lines = append(lines, "## Action items", "")
		for _, item := range a.ActionItems {
			lines = append(lines, fmt.Sprintf("- [ ] **%s**: %s", item.Owner, item.Task))
		}
		lines = append(lines, "")
	}

	if len(t.Chapters) > 0 {
		lines = append(lines, "## Chapters", "")
		for _, ch := range t.Chapters {
			label := ch.Headline
			if label == "" {
				label = ch.Gist
			}
			if label == "" {
				label = "Chapter"
			}
			lines = append(lines, fmt.Sprintf("- `%s–%s` %s", formatTimestamp(ch.Start), formatTimestamp(ch.End), label))
		}
		lines = append(lines, "")
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// AppendTopicSection appends a dated section to a topic file, creating the
// file with a "# <name>" heading when needed. meetingFile is the path of
// the meeting note; only its base name is linked. Returns the topic path.
func (l *Library) AppendTopicSection(topic types.Topic, date, meetingTitle, meetingFile string) (string, error) {
	path := filepath.Join(l.TopicsDir, topic.Slug+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte("# "+topic.Name+"\n"), 0o644); err != nil {
			return "", err
		}
	}

	link := "../meetings/" + filepath.Base(meetingFile)
	section := fmt.Sprintf("\n## %s — %s\n\n%s\n\n*Source: [%s](%s)*\n",
		date, meetingTitle, strings.TrimSpace(topic.Content), meetingTitle, link)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(section); err != nil {
		return "", err
	}
	return path, nil
}

// RebuildIndex regenerates index.md: topics with their last update date
// and meetings, newest first. Returns the index path.
func (l *Library) RebuildIndex() (string, error) {
	type topicEntry struct {
		slug, name, lastUpdate string
	}

	topicFiles, err := filepath.Glob(filepath.Join(l.TopicsDir, "*.md"))
	if err != nil {
		return "", err
	}
	topics := make([]topicEntry, 0, len(topicFiles))
	for _, path := range topicFiles {
		slug := stem(path)
		name, lastUpdate := topicInfo(path, slug)
		topics = append(topics, topicEntry{slug, name, lastUpdate})
	}

	meetings, err := filepath.Glob(filepath.Join(l.MeetingsDir, "*.md"))
	if err != nil {
		return "", err
	}
	slices.Reverse(meetings)

	lines := []string{"# Knowledge library", "", "## Topics", ""}
	if len(topics) > 0 {
		for _, t := range topics {
			suffix := ""
			if t.lastUpdate != "" {
				suffix = " — last updated " + t.lastUpdate
			}
			lines = append(lines, fmt.Sprintf("- [%s](topics/%s.md)%s", t.name, t.slug, suffix))
		}
	} else {
		lines = append(lines, "(no topics yet)")
	}
	lines = append(lines, "", "## Meetings", "")
	if len(meetings) > 0 {
		for _, path := range meetings {
			stemName := stem(path)
			title := stemName
			if data, err := os.ReadFile(path); err == nil {
				if line, ok := firstLine(string(data)); ok && strings.HasPrefix(line, "# ") {
					title = strings.TrimSpace(line[2:])
				}
			}
			lines = append(lines, fmt.Sprintf("- [%s](meetings/%s) — %s", stemName, filepath.Base(path), title))
		}
	} else {
		lines = append(lines, "(no meetings yet)")
	}
	lines = append(lines, "")

	indexPath := filepath.Join(l.Root, "index.md")
	if err := os.WriteFile(indexPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return "", err
	}
	return indexPath, nil
}

// AddMeeting persists everything for one processed meeting and returns the
// note path.
func (l *Library) AddMeeting(t *types.TranscriptResult, a *types.AnalysisResult, videoPath string) (string, error) {
	date := time.Now().UTC().Format("2006-01-02")
	if _, err := l.WriteTranscript(t); err != nil {
		return "", err
	}
	notePath, err := l.WriteMeetingNote(t, a, videoPath, date)
	if err != nil {
		return "", err
	}
	for _, topic := range a.Topics {
		if _, err := l.AppendTopicSection(topic, date, a.Title, notePath); err != nil {
			return "", err
		}
	}
	if _, err := l.RebuildIndex(); err != nil {
		return "", err
	}
	return notePath, nil
}

// stem returns the file name without its final extension (Python's
// Path.stem).
func stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// firstLine returns the first line of text, or false when text is empty.
func firstLine(text string) (string, bool) {
	if text == "" {
		return "", false
	}
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSuffix(line, "\r"), true
}

// topicInfo extracts a topic file's display name (first "# " heading,
// falling back to the slug) and the most recent "## <date>" section date.
// Unreadable or empty files yield the slug and no date.
func topicInfo(path, slug string) (name, lastUpdate string) {
	name = slug
	data, err := os.ReadFile(path)
	if err != nil {
		return name, ""
	}
	text := string(data)
	line, ok := firstLine(text)
	if !ok {
		return name, ""
	}
	if strings.HasPrefix(line, "# ") {
		name = strings.TrimSpace(line[2:])
	}
	for _, m := range sectionDateRe.FindAllStringSubmatch(text, -1) {
		if m[1] > lastUpdate {
			lastUpdate = m[1]
		}
	}
	return name, lastUpdate
}
