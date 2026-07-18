package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkAndIsProcessedRoundtrip(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "meeting.mkv")
	if err := os.WriteFile(video, []byte("fake video data"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(dir)
	if s.IsProcessed(video) {
		t.Error("IsProcessed() = true before MarkProcessed, want false")
	}

	if err := s.MarkProcessed(video, "transcript-1"); err != nil {
		t.Fatalf("MarkProcessed(): %v", err)
	}
	if !s.IsProcessed(video) {
		t.Error("IsProcessed() = false after MarkProcessed, want true")
	}

	// A fresh State loading the same directory sees the entry.
	if !New(dir).IsProcessed(video) {
		t.Error("IsProcessed() = false after reload, want true")
	}

	// Same name with a different size counts as a new recording.
	if err := os.WriteFile(video, []byte("fake video data, now longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s.IsProcessed(video) {
		t.Error("IsProcessed() = true after size change, want false")
	}

	// A missing file is never processed.
	if s.IsProcessed(filepath.Join(dir, "gone.mkv")) {
		t.Error("IsProcessed() = true for missing file, want false")
	}
}

func TestSaveProducesValidCompatibleJSON(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "café.mkv")
	if err := os.WriteFile(video, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(dir)
	if err := s.MarkProcessed(video, "tr-é"); err != nil {
		t.Fatalf("MarkProcessed(): %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "processed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("processed.json does not end with a newline")
	}
	if !strings.Contains(string(raw), "  \"café.mkv\": {") {
		t.Errorf("processed.json is not indent-2 / has escaped non-ASCII:\n%s", raw)
	}

	var decoded map[string]struct {
		Size         int64  `json:"size"`
		TranscriptID string `json:"transcript_id"`
		ProcessedAt  string `json:"processed_at"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("processed.json is not valid JSON: %v", err)
	}
	entry, ok := decoded["café.mkv"]
	if !ok {
		t.Fatalf("no entry for %q in %v", "café.mkv", decoded)
	}
	if entry.Size != 5 {
		t.Errorf("size = %d, want 5", entry.Size)
	}
	if entry.TranscriptID != "tr-é" {
		t.Errorf("transcript_id = %q, want %q", entry.TranscriptID, "tr-é")
	}
	if entry.ProcessedAt == "" {
		t.Error("processed_at is empty")
	}

	// The atomic write must not leave temp files behind.
	leftovers, err := filepath.Glob(filepath.Join(dir, ".processed-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("leftover temp files: %v", leftovers)
	}
}

func TestNewWithCorruptStateFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "processed.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if s.IsProcessed("anything.mkv") {
		t.Error("IsProcessed() = true with corrupt state file, want false")
	}
}
