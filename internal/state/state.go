// Package state keeps a persistent record of processed videos.
//
// State lives in <dir>/processed.json:
//
//	{"<filename>": {"size": int, "transcript_id": string, "processed_at": iso}}
//
// A video is considered processed when both the file name and its size match
// the recorded entry (same name with different size means a new recording).
// Writes are atomic: data goes to a temp file in the same directory and is
// moved into place with os.Rename.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// record is a single processed-file entry in processed.json.
type record struct {
	Size         int64  `json:"size"`
	TranscriptID string `json:"transcript_id"`
	ProcessedAt  string `json:"processed_at"`
}

// State is a persistent, deduplicating record of processed video files.
type State struct {
	dir  string
	file string
	mu   sync.Mutex
	data map[string]record
}

// New loads the state from <dir>/processed.json. A missing or corrupt file
// yields an empty state.
func New(dir string) *State {
	s := &State{
		dir:  dir,
		file: filepath.Join(dir, "processed.json"),
		data: map[string]record{},
	}
	s.load()
	return s
}

func (s *State) load() {
	raw, err := os.ReadFile(s.file)
	if err != nil {
		return
	}
	data := map[string]record{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}
	s.data = data
}

// IsProcessed reports whether an entry exists for this file name and the
// recorded size matches the current size on disk.
func (s *State) IsProcessed(path string) bool {
	s.mu.Lock()
	entry, ok := s.data[filepath.Base(path)]
	s.mu.Unlock()
	if !ok {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return entry.Size == info.Size()
}

// MarkProcessed records path as processed and persists the state atomically.
func (s *State) MarkProcessed(path, transcriptID string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[filepath.Base(path)] = record{
		Size:         info.Size(),
		TranscriptID: transcriptID,
		ProcessedAt:  time.Now().UTC().Format("2006-01-02T15:04:05.000000Z07:00"),
	}
	return s.saveLocked()
}

// saveLocked writes the state to a temp file in the same directory and
// atomically renames it over processed.json. The caller must hold s.mu.
func (s *State) saveLocked() error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".processed-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s.data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.file); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
