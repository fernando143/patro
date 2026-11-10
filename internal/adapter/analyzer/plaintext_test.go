package analyzer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/domain/meeting"
)

func TestPlainTextAnswer(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{
			name:   "trims the padding a CLI adds around its answer",
			stdout: "\n\n{\"topics\": []}\n\n",
			want:   "{\"topics\": []}",
		},
		{
			// The answer itself may be prose with internal newlines; only the
			// edges are trimmed.
			name:   "keeps internal newlines",
			stdout: "Here is the JSON:\n{\n  \"topics\": []\n}\n",
			want:   "Here is the JSON:\n{\n  \"topics\": []\n}",
		},
		{
			name:   "blank output stays blank",
			stdout: "   \n\t\n",
			want:   "",
		},
		{
			name:   "empty output",
			stdout: "",
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := plainTextAnswer(tc.stdout); got != tc.want {
				t.Errorf("plainTextAnswer(%q) = %q, want %q", tc.stdout, got, tc.want)
			}
		})
	}
}

// TestAnalyzeCLIParsesAPlainTextBackend covers the wiring, not just the reader:
// the registry entry for a PlainText backend has to reach AnalyzeCLI, whose
// answer arrives as the CLI's raw stdout rather than as a stream of events. It
// runs under the real "pi" backend name, so it also fails if that entry loses
// its PlainText flag.
func TestAnalyzeCLIParsesAPlainTextBackend(t *testing.T) {
	const answer = `{"meeting": {"title": "PlainText", "summary": "from stdout"}, "topics": [{"slug": "plain-topic"}]}`

	dir := t.TempDir()
	binary := fakeCLI(t, dir, "pi", fakeControl{Stdout: answer})

	got, err := AnalyzeCLI(context.Background(), &meeting.TranscriptResult{ID: "plain", Text: "hello"}, nil, CLIOptions{
		Backend:    "pi",
		BinaryPath: binary,
		WorkDir:    dir,
		StateDir:   filepath.Join(dir, ".state"),
	})
	if err != nil {
		t.Fatalf("AnalyzeCLI(pi) error = %v", err)
	}
	if got.Title != "PlainText" {
		t.Errorf("Title = %q, want %q", got.Title, "PlainText")
	}
	if got.Summary != "from stdout" {
		t.Errorf("Summary = %q, want %q", got.Summary, "from stdout")
	}
}
