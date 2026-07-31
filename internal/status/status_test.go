package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestTracker(t *testing.T) *Tracker {
	t.Helper()
	tr, err := NewTracker(t.TempDir())
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	return tr
}

func TestTrackerLifecycle(t *testing.T) {
	tr := newTestTracker(t)

	tr.Enqueue("/inbox/a.mkv")
	tr.Enqueue("/inbox/b.mkv")
	tr.Enqueue("/inbox/a.mkv") // duplicate ignored

	if got := len(tr.snap.Queue); got != 2 {
		t.Fatalf("queue len = %d, want 2", got)
	}

	tr.Dequeue("/inbox/a.mkv")
	if got := len(tr.snap.Queue); got != 1 {
		t.Fatalf("queue len after dequeue = %d, want 1", got)
	}

	tr.Start("/inbox/a.mkv")
	if tr.snap.Current == nil || tr.snap.Current.File != "a.mkv" {
		t.Fatalf("current = %+v, want a.mkv", tr.snap.Current)
	}
	if tr.snap.Current.Stage != StageTranscribing {
		t.Fatalf("stage = %q, want %q", tr.snap.Current.Stage, StageTranscribing)
	}

	tr.Stage(StageAnalyzing)
	if tr.snap.Current.Stage != StageAnalyzing {
		t.Fatalf("stage = %q, want %q", tr.snap.Current.Stage, StageAnalyzing)
	}

	tr.Done("/inbox/a.mkv", "Meeting A")
	if tr.snap.Current != nil {
		t.Fatalf("current after done = %+v, want nil", tr.snap.Current)
	}
	if tr.snap.ProcessedSession != 1 {
		t.Fatalf("processed = %d, want 1", tr.snap.ProcessedSession)
	}
	if len(tr.snap.Recent) != 1 || tr.snap.Recent[0].Title != "Meeting A" {
		t.Fatalf("recent = %+v", tr.snap.Recent)
	}

	tr.Start("/inbox/b.mkv")
	tr.Fail("/inbox/b.mkv", "boom")
	if tr.snap.FailedSession != 1 {
		t.Fatalf("failed = %d, want 1", tr.snap.FailedSession)
	}
	if len(tr.snap.Failures) != 1 || tr.snap.Failures[0].Reason != "boom" {
		t.Fatalf("failures = %+v", tr.snap.Failures)
	}
}

func TestReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tr, err := NewTracker(dir)
	if err != nil {
		t.Fatal(err)
	}
	tr.Enqueue("x.mkv")
	tr.Start("x.mkv")
	tr.Done("x.mkv", "X")

	snap, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap == nil {
		t.Fatal("snap is nil")
	}
	if snap.ProcessedSession != 1 {
		t.Fatalf("processed = %d, want 1", snap.ProcessedSession)
	}
	if snap.PID == 0 {
		t.Error("PID not recorded")
	}
	if snap.StartedAt.IsZero() {
		t.Error("StartedAt not recorded")
	}
}

func TestReadMissingReturnsNil(t *testing.T) {
	snap, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap != nil {
		t.Fatalf("snap = %+v, want nil", snap)
	}
}

func TestReadCorruptJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Error("Read() error = nil, want error for corrupt JSON")
	}
}

func TestDequeueMissingEntryIsNoop(t *testing.T) {
	tr := newTestTracker(t)
	tr.Enqueue("a.mkv")
	tr.Dequeue("not-in-queue.mkv")
	if len(tr.snap.Queue) != 1 || tr.snap.Queue[0] != "a.mkv" {
		t.Errorf("queue = %v, want a.mkv untouched", tr.snap.Queue)
	}
}

func TestFlushLockedStateDirBlockedByFile(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(blocker, "state")

	if _, err := NewTracker(stateDir); err == nil {
		t.Error("NewTracker() error = nil, want error when the state dir cannot be created")
	}
}

func TestFlushLockedStateDirReadOnly(t *testing.T) {
	stateDir := t.TempDir()
	tr, err := NewTracker(stateDir)
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	if err := os.Chmod(stateDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) })

	tr.Enqueue("a.mkv")
	snap, err := Read(stateDir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(snap.Queue) != 0 {
		t.Errorf("queue = %v, want the failed flush to leave the on-disk snapshot unchanged", snap.Queue)
	}
}

func TestFlushLockedRenameOntoDirectoryFails(t *testing.T) {
	stateDir := t.TempDir()
	// status.json already exists as a directory: NewTracker's initial
	// flush cannot replace it with the freshly written temp file.
	if err := os.MkdirAll(filepath.Join(stateDir, FileName), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := NewTracker(stateDir); err == nil {
		t.Error("NewTracker() error = nil, want error when status.json is a directory")
	}
}

func TestNilTrackerIsSafe(t *testing.T) {
	var tr *Tracker
	// None of these should panic.
	tr.Enqueue("a")
	tr.Start("a")
	tr.Stage(StageWriting)
	tr.Done("a", "t")
	tr.Fail("a", "r")
}

func TestCapsRecentAndFailures(t *testing.T) {
	tr := newTestTracker(t)
	base := time.Now()
	tr.now = func() time.Time { base = base.Add(time.Second); return base }

	for i := 0; i < maxRecent+5; i++ {
		tr.Done("f.mkv", "t")
	}
	if len(tr.snap.Recent) != maxRecent {
		t.Fatalf("recent len = %d, want %d", len(tr.snap.Recent), maxRecent)
	}
	for i := 0; i < maxFailures+5; i++ {
		tr.Fail("f.mkv", "r")
	}
	if len(tr.snap.Failures) != maxFailures {
		t.Fatalf("failures len = %d, want %d", len(tr.snap.Failures), maxFailures)
	}
}
