// Package knowledge writes and maintains the Markdown knowledge library.
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
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/fernando143/patro/internal/domain/meeting"
	"github.com/fernando143/patro/internal/platform/logging"

	"github.com/fernando143/patro/internal/platform/layout"

	"github.com/fernando143/patro/internal/adapter/ledger"
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

	// Reconciler decides merge-vs-new for each candidate topic during
	// AddMeetingCtx. Nil (the zero value) preserves today's exact-slug-only
	// behavior: no embedding or reconciliation call ever occurs (design
	// D1). Set it to reconcile.SemanticReconciler (or any Reconciler) to
	// opt in.
	Reconciler Reconciler
}

// NewLibrary creates the library directory layout under root and returns
// the Library handle.
func NewLibrary(root string) (*Library, error) {
	lib := layout.Library(root)
	l := &Library{
		Root:           root,
		TopicsDir:      lib.Topics(),
		MeetingsDir:    lib.Meetings(),
		TranscriptsDir: lib.Transcripts(),
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
func (l *Library) ExistingTopics() []meeting.TopicRef {
	files, err := filepath.Glob(filepath.Join(l.TopicsDir, "*.md"))
	if err != nil {
		return nil
	}
	topics := make([]meeting.TopicRef, 0, len(files))
	for _, path := range files {
		slug := layout.Stem(path)
		name, _ := topicInfo(path, slug)
		topics = append(topics, meeting.TopicRef{Slug: slug, Name: name})
	}
	return topics
}

// ExistingTopicsRecent returns a {slug, name} reference for every topic
// file, sorted by the topic's most recent dated section (topicInfo's
// lastUpdate) descending, capped at n entries. Topics with no dated section
// sort last. n < 0 returns every topic (design D6 — used by the analyzer
// prompt's recency cap).
func (l *Library) ExistingTopicsRecent(n int) []meeting.TopicRef {
	files, err := filepath.Glob(filepath.Join(l.TopicsDir, "*.md"))
	if err != nil {
		return nil
	}
	type entry struct {
		ref        meeting.TopicRef
		lastUpdate string
	}
	entries := make([]entry, 0, len(files))
	for _, path := range files {
		slug := layout.Stem(path)
		name, lastUpdate := topicInfo(path, slug)
		entries = append(entries, entry{meeting.TopicRef{Slug: slug, Name: name}, lastUpdate})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].lastUpdate != entries[j].lastUpdate {
			return entries[i].lastUpdate > entries[j].lastUpdate
		}
		return entries[i].ref.Slug < entries[j].ref.Slug // deterministic tie-break
	})
	if n >= 0 && n < len(entries) {
		entries = entries[:n]
	}
	refs := make([]meeting.TopicRef, len(entries))
	for i, e := range entries {
		refs[i] = e.ref
	}
	return refs
}

// WriteTranscript writes the raw transcript with speaker labels, one
// utterance per paragraph, and returns its path.
func (l *Library) WriteTranscript(t *meeting.TranscriptResult) (string, error) {
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

// transcriptLink renders the "[transcript](../transcripts/<id>.txt)" link
// text used both in a meeting note's "Raw transcript" line and as the exact
// substring FindMeetingNoteByTranscriptID scans for, so the two can never
// drift apart.
func transcriptLink(id string) string {
	return "[transcript](../transcripts/" + id + ".txt)"
}

// WriteMeetingNote writes the full meeting note and returns the path of
// the created file. The path is computed from date and the title's slug;
// use WriteMeetingNoteAt to overwrite a specific existing path instead.
func (l *Library) WriteMeetingNote(t *meeting.TranscriptResult, a *meeting.AnalysisResult, videoPath, date string) (string, error) {
	path := filepath.Join(l.MeetingsDir, date+"-"+Slugify(a.Title)+".md")
	return l.WriteMeetingNoteAt(path, t, a, videoPath, date)
}

// WriteMeetingNoteAt writes the full meeting note to the exact given path,
// overwriting anything already there, and returns that same path. It is the
// building block WriteMeetingNote delegates to after computing its own
// date+slug path; regeneration (design D4) calls it directly to overwrite a
// prior note in place even when the new title's slug differs.
func (l *Library) WriteMeetingNoteAt(path string, t *meeting.TranscriptResult, a *meeting.AnalysisResult, videoPath, date string) (string, error) {
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
		"- **Raw transcript:** " + transcriptLink(t.ID),
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

// PriorNote is an existing meeting note found by
// FindMeetingNoteByTranscriptID. Date is "" when the note's
// "- **Date:** " line is missing or malformed — never an error, since the
// note itself was still found by its transcript link (design D3 edge case).
type PriorNote struct {
	Path        string
	Date        string
	SourceVideo string
}

// ResolveTranscriptID derives the transcript ID for a regenerate run. When
// path already lives directly under l.TranscriptsDir, its filename stem is
// reused as-is (external is false) — no copy is needed since the transcript
// is already in the library. Otherwise a stable, content-addressed ID is
// generated ("ext-" + the first 12 hex characters of the file's sha256),
// so re-running on byte-identical content always yields the same ID
// (design D1), and external is true so the caller knows to copy the file in.
func (l *Library) ResolveTranscriptID(path string) (id string, external bool, err error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	absTranscriptsDir, err := filepath.Abs(l.TranscriptsDir)
	if err != nil {
		return "", false, err
	}
	if filepath.Dir(absPath) == absTranscriptsDir {
		return layout.Stem(absPath), false, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	sum := sha256.Sum256(data)
	return "ext-" + hex.EncodeToString(sum[:])[:12], true, nil
}

// FindMeetingNoteByTranscriptID scans knowledge/meetings/*.md (sorted, first
// match wins) for the one note whose "Raw transcript" line links to id,
// matching the exact substring transcriptLink(id) produces — shared with
// WriteMeetingNoteAt so the two formats cannot drift apart (design D3). A
// file that cannot be read is skipped, not fatal (e.g. permission
// restrictions on one note must not abort regenerating another). Returns
// (nil, nil) when no note links to id.
func (l *Library) FindMeetingNoteByTranscriptID(id string) (*PriorNote, error) {
	files, err := filepath.Glob(filepath.Join(l.MeetingsDir, "*.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	link := transcriptLink(id)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(data)
		if !strings.Contains(text, link) {
			continue
		}

		note := &PriorNote{Path: path}
		for _, line := range strings.Split(text, "\n") {
			if v, ok := strings.CutPrefix(line, "- **Date:** "); ok {
				note.Date = strings.TrimSpace(v)
			}
			if v, ok := strings.CutPrefix(line, "- **Source video:** "); ok {
				note.SourceVideo = strings.Trim(strings.TrimSpace(v), "`")
			}
		}
		return note, nil
	}
	return nil, nil
}

// AppendTopicSection appends a dated section to a topic file, creating the
// file with a "# <name>" heading when needed. meetingFile is the path of
// the meeting note; only its base name is linked. Returns the topic path.
//
// It delegates to AppendTopicSectionAnnotated with an empty annotation, so
// its behavior and byte-for-byte output are unchanged (design D1).
func (l *Library) AppendTopicSection(topic meeting.Topic, date, meetingTitle, meetingFile string) (string, error) {
	return l.AppendTopicSectionAnnotated(topic, date, meetingTitle, meetingFile, "")
}

// AppendTopicSectionAnnotated is AppendTopicSection with an optional
// trailing annotation line (e.g. a merge provenance note — design D4). An
// empty annotation produces output identical to AppendTopicSection.
func (l *Library) AppendTopicSectionAnnotated(topic meeting.Topic, date, meetingTitle, meetingFile, annotation string) (string, error) {
	path := filepath.Join(l.TopicsDir, topic.Slug+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte("# "+topic.Name+"\n"), 0o644); err != nil {
			return "", err
		}
	}

	link := "../meetings/" + filepath.Base(meetingFile)
	section := fmt.Sprintf("\n## %s — %s\n\n%s\n\n*Source: [%s](%s)*\n",
		date, meetingTitle, strings.TrimSpace(topic.Content), meetingTitle, link)
	if annotation != "" {
		section += annotation + "\n"
	}

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
		slug := layout.Stem(path)
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
			stemName := layout.Stem(path)
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

	indexPath := layout.Library(l.Root).Index()
	if err := os.WriteFile(indexPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return "", err
	}
	return indexPath, nil
}

// AddMeeting persists everything for one processed meeting and returns the
// note path.
//
// It delegates to AddMeetingCtx(context.Background(), ...), so with a nil
// Reconciler (the zero value) its behavior is unchanged: exact-slug-only
// topic matching, no embedding or reconciliation call (design D1).
func (l *Library) AddMeeting(t *meeting.TranscriptResult, a *meeting.AnalysisResult, videoPath string) (string, error) {
	return l.AddMeetingCtx(context.Background(), t, a, videoPath)
}

// AddMeetingCtx is AddMeeting with an explicit context, threaded through to
// Reconciler.Reconcile for each candidate topic (design D1). With a nil
// Reconciler, every candidate keeps its own proposed slug/name — today's
// exact-slug-only behavior. With a Reconciler configured, a merge appends
// into the resolved existing topic instead and annotates the section with
// the original proposed slug and score (design D4).
func (l *Library) AddMeetingCtx(ctx context.Context, t *meeting.TranscriptResult, a *meeting.AnalysisResult, videoPath string) (string, error) {
	date := time.Now().UTC().Format("2006-01-02")
	if _, err := l.WriteTranscript(t); err != nil {
		return "", err
	}
	notePath, err := l.WriteMeetingNote(t, a, videoPath, date)
	if err != nil {
		return "", err
	}

	var existing []meeting.TopicRef
	if l.Reconciler != nil {
		existing = l.ExistingTopics()
	}

	for _, topic := range a.Topics {
		resolved := topic
		annotation := ""
		if l.Reconciler != nil {
			res, rerr := l.Reconciler.Reconcile(ctx, topic, existing)
			if rerr != nil {
				return "", rerr
			}
			resolved.Slug = res.Slug
			resolved.Name = res.Name
			if res.Merged {
				annotation = fmt.Sprintf("*Merged from proposed slug `%s` — cosine %.2f*", res.ProposedSlug, res.Score)
			}
		}
		if _, err := l.AppendTopicSectionAnnotated(resolved, date, a.Title, notePath, annotation); err != nil {
			return "", err
		}
	}

	if _, err := l.RebuildIndex(); err != nil {
		return "", err
	}
	return notePath, nil
}

// ReconcileFlagged re-attempts reconciliation for every topic flagged in
// ledgerPath's ledger (design D4/D8, "patro reconcile" — Unit 7), using the
// library's current Reconciler and, by extension, its now-current vector
// store — e.g. right after a rebuild fixed a mismatched or missing store, or
// simply because more topics have accumulated since the candidate was first
// flagged. Only the most recently flagged ledger record per slug is
// retried; a flagged topic whose own file no longer exists (already merged
// or removed by an earlier pass) is skipped, not an error.
//
// A safe-fail-flagged candidate was written to its own standalone topic
// file at ingestion time (design D1's "new, flagged" path); merging it here
// moves that file's content into the newly resolved target topic
// (annotated with its provenance, mirroring AddMeetingCtx's own merge
// annotation) and removes the now-redundant standalone file. The ledger
// entry Reconciler.Reconcile itself writes remains the audit trail (design
// D4) — no content is lost, only relocated within the library.
//
// onProgress, if non-nil, is called after each flagged slug is processed
// with (done, total), matching the representation-sync progress callback
// shape. It returns the number of topics that were merged into an existing
// topic. A
// nil Reconciler makes this a no-op (0, nil); a failure reconciling one
// flagged topic is logged and does not abort the batch.
func (l *Library) ReconcileFlagged(ctx context.Context, ledgerPath string, onProgress func(done, total int)) (int, error) {
	if l.Reconciler == nil {
		return 0, nil
	}

	entries, err := ledger.Read(ledgerPath)
	if err != nil {
		return 0, err
	}

	// Only the latest flagged record per slug matters: an older flagged
	// record for a slug later resolved (merged, or reflagged again) is
	// superseded.
	latest := map[string]ledger.Entry{}
	var order []string
	for _, e := range entries {
		if !e.Flagged {
			continue
		}
		if _, ok := latest[e.Slug]; !ok {
			order = append(order, e.Slug)
		}
		latest[e.Slug] = e
	}

	merged := 0
	total := len(order)
	for i, slug := range order {
		entry := latest[slug]
		wasMerged, mergeErr := l.tryMergeFlagged(ctx, entry)
		if mergeErr != nil {
			logging.Warnf("reconcile flagged topic %q: %v", entry.Slug, mergeErr)
		} else if wasMerged {
			merged++
		}
		if onProgress != nil {
			onProgress(i+1, total)
		}
	}

	if merged > 0 {
		if _, err := l.RebuildIndex(); err != nil {
			return merged, err
		}
	}
	return merged, nil
}

// tryMergeFlagged re-embeds entry's own topic file content, asks the
// Reconciler whether it now matches an existing topic, and — only on a
// genuine merge into a *different* topic — moves the content over and
// removes the redundant standalone file. It reports whether a merge
// happened.
func (l *Library) tryMergeFlagged(ctx context.Context, entry ledger.Entry) (bool, error) {
	path := filepath.Join(l.TopicsDir, entry.Slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // already merged/removed since being flagged
		}
		return false, err
	}
	content := strings.TrimPrefix(string(data), "# "+entry.Name+"\n")

	existing := l.ExistingTopics()
	filtered := make([]meeting.TopicRef, 0, len(existing))
	for _, ref := range existing {
		if ref.Slug != entry.Slug {
			filtered = append(filtered, ref)
		}
	}

	candidate := meeting.Topic{Slug: entry.Slug, Name: entry.Name, Content: content}
	res, err := l.Reconciler.Reconcile(ctx, candidate, filtered)
	if err != nil {
		return false, err
	}
	if !res.Merged || res.Slug == entry.Slug {
		return false, nil
	}

	annotation := fmt.Sprintf("*Merged from proposed slug `%s` — cosine %.2f*", entry.Slug, res.Score)
	target := meeting.Topic{Slug: res.Slug, Name: res.Name, Content: content}
	if _, err := l.appendReconciledSection(target, entry.Slug, annotation); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

// appendReconciledSection appends target's content as a dated section,
// provenance-annotated, mirroring AppendTopicSectionAnnotated's markdown
// shape but for a topic-to-topic merge (there is no meeting note to link
// back to, unlike AddMeetingCtx's per-meeting candidates).
func (l *Library) appendReconciledSection(target meeting.Topic, sourceSlug, annotation string) (string, error) {
	path := filepath.Join(l.TopicsDir, target.Slug+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte("# "+target.Name+"\n"), 0o644); err != nil {
			return "", err
		}
	}

	date := time.Now().UTC().Format("2006-01-02")
	section := fmt.Sprintf("\n## %s — Reconciled from `%s`\n\n%s\n\n%s\n",
		date, sourceSlug, strings.TrimSpace(target.Content), annotation)

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

// stem returns the file name without its final extension (Python's
// Path.stem).
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
