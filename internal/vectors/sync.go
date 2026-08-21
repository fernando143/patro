package vectors

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/fernando143/patro/internal/embed"
)

type SyncState string

const (
	StateDirty        SyncState = "Dirty"
	StateBuilding     SyncState = "Building"
	StatePrepared     SyncState = "Prepared"
	StateCommitIntent SyncState = "CommitIntent"
	StateCurrent      SyncState = "Current"
)

type DocumentRepresenter interface {
	Represent(context.Context, embed.Document) (*embed.Representation, error)
}

// CommitFS isolates durable filesystem ordering from the sync state machine.
type CommitFS interface {
	CreateTemp(dir, pattern string) (string, error)
	WriteFile(path string, data []byte) error
	LinkOrCopyRollback(source, rollback string) error
	SyncFile(path string) error
	Close(path string) error
	Rename(source, target string) error
	SyncDir(path string) error
	Restore(rollback, target string) error
	Remove(path string) error
}

type OSCommitFS struct{}

func (OSCommitFS) CreateTemp(dir, pattern string) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	return name, nil
}

func (OSCommitFS) WriteFile(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }

func (OSCommitFS) LinkOrCopyRollback(source, rollback string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(rollback, data, 0o600)
}

func (OSCommitFS) SyncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	return file.Sync()
}

func (OSCommitFS) Close(string) error { return nil }

func (OSCommitFS) Rename(source, target string) error { return os.Rename(source, target) }

func (OSCommitFS) SyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (OSCommitFS) Restore(rollback, target string) error {
	data, err := os.ReadFile(rollback)
	if err != nil {
		if os.IsNotExist(err) {
			return os.Remove(target)
		}
		return err
	}
	return os.WriteFile(target, data, 0o600)
}

func (OSCommitFS) Remove(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

type V2Store struct {
	path     string
	identity embed.RepresentationIdentity
	fs       CommitFS

	mu      sync.Mutex
	state   SyncState
	entries map[string]embed.Representation

	beforeCommit func()
	beforeRename func()
}

// stem returns the file name without its final extension, e.g.
// "topics/foo.md" -> "foo".
func stem(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}

func (s *V2Store) NearestRepresentations(ctx context.Context, query embed.Representation, mode embed.ScoreMode, limit int) ([]embed.RankedResult, error) {
	s.mu.Lock()
	if s.state != StateCurrent {
		s.mu.Unlock()
		return nil, ErrV2NeedsRebuild
	}
	documents := make([]embed.Representation, 0, len(s.entries))
	for _, representation := range s.entries {
		documents = append(documents, representation)
	}
	s.mu.Unlock()
	results, err := embed.Rank(ctx, query, documents, mode)
	if err != nil {
		return nil, err
	}
	if limit >= 0 && limit < len(results) {
		results = results[:limit]
	}
	return results, nil
}

func NewV2Store(path string, identity embed.RepresentationIdentity, fs CommitFS) *V2Store {
	if fs == nil {
		fs = OSCommitFS{}
	}
	store := &V2Store{path: path, identity: identity, fs: fs, state: StateDirty, entries: map[string]embed.Representation{}}
	if loaded, needs, err := LoadV2(path, identity); err == nil && !needs {
		for _, representation := range loaded {
			store.entries[representation.DocumentID] = representation
		}
		store.state = StateCurrent
	}
	return store
}

func (s *V2Store) State() SyncState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *V2Store) NeedsSync() bool { return s.State() != StateCurrent }

func (s *V2Store) MarkDirty() {
	s.mu.Lock()
	s.state = StateDirty
	s.mu.Unlock()
}

func (s *V2Store) SetBeforeCommitHook(hook func()) {
	s.mu.Lock()
	s.beforeCommit = hook
	s.mu.Unlock()
}

func (s *V2Store) SetBeforeRenameHook(hook func()) {
	s.mu.Lock()
	s.beforeRename = hook
	s.mu.Unlock()
}

func (s *V2Store) Sync(ctx context.Context, source string, representer DocumentRepresenter) error {
	if representer == nil {
		return errors.New("vectors: representation sync requires a representer")
	}
	s.mu.Lock()
	s.state = StateBuilding
	oldEntries := cloneRepresentations(s.entries)
	s.mu.Unlock()

	files, err := filepath.Glob(filepath.Join(source, "*.md"))
	if err != nil {
		return s.failDirty(err)
	}
	sort.Strings(files)
	next := make(map[string]embed.Representation, len(files))
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return s.failDirty(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return s.failDirty(err)
		}
		id := stem(path)
		sourceHash := sourceHash(string(data))
		if old, ok := oldEntries[id]; ok && old.SourceHash == sourceHash {
			next[id] = old
			continue
		}
		representation, err := representer.Represent(ctx, embed.Document{ID: id, Text: string(data)})
		if err != nil {
			return s.failDirty(err)
		}
		next[id] = *representation
	}
	representations := make([]embed.Representation, 0, len(next))
	for _, representation := range next {
		representations = append(representations, representation)
	}
	data, err := marshalV2(s.identity, representations)
	if err != nil {
		return s.failDirty(err)
	}

	s.mu.Lock()
	s.state = StatePrepared
	beforeCommit := s.beforeCommit
	beforeRename := s.beforeRename
	s.mu.Unlock()
	if beforeCommit != nil {
		beforeCommit()
	}
	if err := ctx.Err(); err != nil {
		return s.failDirty(err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return s.failDirty(err)
	}
	candidate, err := s.fs.CreateTemp(dir, ".vectors-candidate-*")
	if err != nil {
		return s.failDirty(err)
	}
	rollback := s.path + ".rollback"
	cleanupCandidate := func() error { return s.fs.Remove(candidate) }
	cleanupArtifacts := func() error { return errors.Join(s.fs.Remove(candidate), s.fs.Remove(rollback)) }
	if err := s.fs.WriteFile(candidate, data); err != nil {
		return s.failWithCleanup(err, cleanupCandidate)
	}
	if err := s.fs.SyncFile(candidate); err != nil {
		return s.failWithCleanup(err, cleanupCandidate)
	}
	if err := s.fs.Close(candidate); err != nil {
		return s.failWithCleanup(err, cleanupCandidate)
	}
	if err := s.fs.LinkOrCopyRollback(s.path, rollback); err != nil {
		return s.failWithCleanup(err, cleanupArtifacts)
	}
	if err := s.fs.SyncFile(rollback); err != nil {
		return s.failWithCleanup(err, cleanupArtifacts)
	}
	if err := s.fs.Close(rollback); err != nil {
		return s.failWithCleanup(err, cleanupArtifacts)
	}
	if err := s.fs.SyncDir(dir); err != nil {
		return s.failWithCleanup(err, cleanupArtifacts)
	}
	if err := ctx.Err(); err != nil {
		return s.failWithCleanup(err, func() error { return errors.Join(cleanupCandidate(), s.fs.Remove(rollback)) })
	}

	s.mu.Lock()
	s.state = StateCommitIntent
	s.mu.Unlock()
	if beforeRename != nil {
		beforeRename()
	}
	if err := s.fs.Rename(candidate, s.path); err != nil {
		return s.rollback(err, rollback, candidate, dir)
	}
	if err := s.fs.SyncDir(dir); err != nil {
		return s.rollback(err, rollback, candidate, dir)
	}
	if err := s.fs.Remove(rollback); err != nil {
		return s.rollback(err, rollback, candidate, dir)
	}
	if err := s.fs.SyncDir(dir); err != nil {
		return s.rollback(err, rollback, candidate, dir)
	}
	s.mu.Lock()
	s.entries = next
	s.state = StateCurrent
	s.mu.Unlock()
	return nil
}

func (s *V2Store) failDirty(err error) error {
	s.mu.Lock()
	s.state = StateDirty
	s.mu.Unlock()
	return err
}

func (s *V2Store) failWithCleanup(err error, cleanup func() error) error {
	return s.failDirty(errors.Join(err, cleanup()))
}

func (s *V2Store) rollback(primary error, rollback, candidate, dir string) error {
	restoreErr := s.fs.Restore(rollback, s.path)
	if restoreErr == nil {
		restoreErr = errors.Join(s.fs.SyncFile(s.path), s.fs.Close(s.path))
	}
	syncErr := s.fs.SyncDir(dir)
	cleanupErr := errors.Join(s.fs.Remove(candidate), s.fs.Remove(rollback))
	s.mu.Lock()
	s.state = StateDirty
	s.mu.Unlock()
	return errors.Join(primary, restoreErr, syncErr, cleanupErr)
}

func cloneRepresentations(source map[string]embed.Representation) map[string]embed.Representation {
	result := make(map[string]embed.Representation, len(source))
	for id, representation := range source {
		representation.Chunks = append([]embed.Chunk(nil), representation.Chunks...)
		result[id] = representation
	}
	return result
}

func sourceHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:])
}
