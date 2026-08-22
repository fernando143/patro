// Package layout owns patro's on-disk layout: the directories and files the
// knowledge library is made of, and the derived artifacts kept under the
// state directory.
//
// Before this package the same joins — "topics", "meetings",
// "vectors/topics.json", "reconciliation.json" — were spelled out in eleven
// files across seven packages, so renaming a directory meant finding every
// copy. internal/adapter/status set the precedent this package follows: declare the
// name once, expose an accessor, and let callers stop knowing the string.
//
// Paths come in as plain strings rather than a *config.Config so this
// package stays below internal/platform/config and remains usable from
// migration.Service, which holds bare roots of its own.
package layout

import (
	"path/filepath"
	"strings"
)

// Names of the directories and files inside the knowledge library root.
const (
	TopicsDirName      = "topics"
	MeetingsDirName    = "meetings"
	TranscriptsDirName = "transcripts"
	IndexFileName      = "index.md"
)

// URL path segments served by the web viewer.
//
// They deliberately duplicate the directory names above instead of aliasing
// them: the library's on-disk layout and the viewer's public URLs are
// separate contracts that merely happen to agree today. Renaming a directory
// must never silently rewrite every published link.
const (
	TopicsURLSegment   = "topics"
	MeetingsURLSegment = "meetings"
)

// Names of the derived artifacts under the state directory.
const (
	vectorsDirName  = "vectors"
	vectorStoreFile = "topics.json"
	ledgerFileName  = "reconciliation.json"
	processedFile   = "processed.json"
	searchIndexDir  = "search-index"
	tempDirName     = "tmp"
)

// Library resolves paths inside the knowledge library root.
type Library string

// Root returns the library root itself.
func (l Library) Root() string { return string(l) }

// Topics is the directory holding one Markdown file per topic.
func (l Library) Topics() string { return filepath.Join(string(l), TopicsDirName) }

// Meetings is the directory holding one Markdown note per processed meeting.
func (l Library) Meetings() string { return filepath.Join(string(l), MeetingsDirName) }

// Transcripts is the directory holding the raw transcript text files.
func (l Library) Transcripts() string { return filepath.Join(string(l), TranscriptsDirName) }

// Index is the regenerated table of contents at the library root.
func (l Library) Index() string { return filepath.Join(string(l), IndexFileName) }

// State resolves paths inside patro's state directory.
type State string

// Root returns the state directory itself.
func (s State) Root() string { return string(s) }

// VectorStore is the multi-vector representation store for library topics.
func (s State) VectorStore() string {
	return filepath.Join(string(s), vectorsDirName, vectorStoreFile)
}

// Ledger is the reconciliation audit trail.
func (s State) Ledger() string { return filepath.Join(string(s), ledgerFileName) }

// Processed is the deduplication record of already-processed videos.
func (s State) Processed() string { return filepath.Join(string(s), processedFile) }

// SearchIndex is the derived BM25 index directory. It is deletable and
// rebuildable — never a source of truth.
func (s State) SearchIndex() string { return filepath.Join(string(s), searchIndexDir) }

// Temp is the scratch directory for transcripts handed to analyzer CLIs.
func (s State) Temp() string { return filepath.Join(string(s), tempDirName) }

// Stem returns the file name without its directory or extension:
// "a/b/note.md" becomes "note". Topic and meeting slugs are derived from
// file names throughout patro, so this is the shared spelling of that rule.
func Stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
