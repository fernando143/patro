package layout

import (
	"path/filepath"
	"testing"
)

// TestLibraryPaths pins every library accessor to the exact path the code
// built by hand before this package existed.
func TestLibraryPaths(t *testing.T) {
	root := filepath.Join("home", "fernando", "knowledge")
	lib := Library(root)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Root", lib.Root(), root},
		{"Topics", lib.Topics(), filepath.Join(root, "topics")},
		{"Meetings", lib.Meetings(), filepath.Join(root, "meetings")},
		{"Transcripts", lib.Transcripts(), filepath.Join(root, "transcripts")},
		{"Index", lib.Index(), filepath.Join(root, "index.md")},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

// TestStatePaths pins every state accessor to the exact path the code built
// by hand before this package existed.
func TestStatePaths(t *testing.T) {
	dir := filepath.Join("home", "fernando", ".state")
	st := State(dir)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Root", st.Root(), dir},
		{"VectorStore", st.VectorStore(), filepath.Join(dir, "vectors", "topics.json")},
		{"Ledger", st.Ledger(), filepath.Join(dir, "reconciliation.json")},
		{"Processed", st.Processed(), filepath.Join(dir, "processed.json")},
		{"SearchIndex", st.SearchIndex(), filepath.Join(dir, "search-index")},
		{"Temp", st.Temp(), filepath.Join(dir, "tmp")},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

// TestURLSegmentsAreIndependentOfDirectoryNames guards the one thing that
// must not be "simplified": the viewer's public URL segments and the
// library's directory names are separate contracts. They agree today, and
// this test exists so that a future directory rename fails loudly here
// instead of silently rewriting every published link.
func TestURLSegmentsAreIndependentOfDirectoryNames(t *testing.T) {
	if TopicsURLSegment != "topics" {
		t.Errorf("TopicsURLSegment = %q, want %q", TopicsURLSegment, "topics")
	}
	if MeetingsURLSegment != "meetings" {
		t.Errorf("MeetingsURLSegment = %q, want %q", MeetingsURLSegment, "meetings")
	}
}

// TestStem covers the behavior of the three separate stem() implementations
// this function replaces. They were written differently — one used
// strings.TrimSuffix, two sliced the base by extension length — so this
// table proves they agreed before two of them were deleted.
func TestStem(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"nested markdown", filepath.Join("a", "b", "c.md"), "c"},
		{"bare file", "c.md", "c"},
		{"no extension", "noext", "noext"},
		{"dotfile is all extension", ".hidden", ""},
		{"empty", "", ""},
		{"multiple dots keeps all but last", "a.b.c.txt", "a.b.c"},
		{"trailing separator", filepath.Join("a", "b") + string(filepath.Separator), "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Stem(tt.path); got != tt.want {
				t.Errorf("Stem(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
