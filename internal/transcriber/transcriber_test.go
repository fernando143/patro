package transcriber

import (
	"context"
	"path/filepath"
	"testing"
)

// Transcribe has no injection seam for the AssemblyAI client, so it can only
// be unit-tested up to the point where it would talk to the network. The
// file-open failure happens before any request is made, so it is the one
// path reachable here without a real API call.
func TestTranscribeMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.mkv")

	_, err := Transcribe(context.Background(), missing, "irrelevant-key")
	if err == nil {
		t.Fatal("Transcribe error = nil, want error when the video file does not exist")
	}
}

func TestDerefStr(t *testing.T) {
	if got := derefStr(nil); got != "" {
		t.Errorf("derefStr(nil) = %q, want %q", got, "")
	}
	s := "hello"
	if got := derefStr(&s); got != "hello" {
		t.Errorf("derefStr(&%q) = %q, want %q", s, got, "hello")
	}
}

func TestDerefInt64(t *testing.T) {
	if got := derefInt64(nil); got != 0 {
		t.Errorf("derefInt64(nil) = %d, want 0", got)
	}
	n := int64(42)
	if got := derefInt64(&n); got != 42 {
		t.Errorf("derefInt64(&%d) = %d, want 42", n, got)
	}
}
