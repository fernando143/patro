// Package types defines the shared data types passed between the
// transcription, analysis, and library stages of the patro pipeline.
package types

// Chapter is a single auto-generated chapter of a transcript.
type Chapter struct {
	Headline string
	Gist     string
	Start    int64 // milliseconds
	End      int64 // milliseconds
}

// Utterance is a single speaker turn in a diarized transcript.
type Utterance struct {
	Speaker string
	Text    string
}

// TranscriptResult is the outcome of transcribing one video file.
type TranscriptResult struct {
	ID         string
	Text       string
	Chapters   []Chapter
	Utterances []Utterance
	Language   string
}

// Topic is a knowledge-library topic extracted from a meeting.
type Topic struct {
	Slug    string
	Name    string
	Content string
}

// TopicRef is a lightweight {slug, name} reference to an existing topic file.
type TopicRef struct {
	Slug string
	Name string
}

// ActionItem is a task assigned to a meeting participant.
type ActionItem struct {
	Owner string
	Task  string
}

// AnalysisResult is the structured analysis produced from a transcript.
type AnalysisResult struct {
	Title       string
	Summary     string
	KeyPoints   []string
	Decisions   []string
	ActionItems []ActionItem
	Topics      []Topic
}
