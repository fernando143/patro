package analyzer

import "strings"

// plainTextAnswer is the whole reader for a backend that prints the assistant's
// final message directly on stdout instead of emitting an event stream.
//
// Trimming is the only normalisation needed: ParseAnalysis already tolerates
// prose around the JSON object it looks for, and unwrapping JSONL would find no
// envelope and report every answer as empty.
func plainTextAnswer(stdout string) string {
	return strings.TrimSpace(stdout)
}
