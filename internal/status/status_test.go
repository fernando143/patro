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

func TestSnapshotUnmarshalsOldFormatWithoutMaintenance(t *testing.T) {
	old := `{
		"pid": 123,
		"started_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:01Z",
		"queue": ["a.mkv"],
		"current": null,
		"processed_session": 2,
		"failed_session": 0,
		"failures": [],
		"recent": []
	}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap == nil {
		t.Fatal("snap is nil")
	}
	if snap.Maintenance != nil {
		t.Fatalf("Maintenance = %+v, want nil for an old-format snapshot with no maintenance key", snap.Maintenance)
	}
	if snap.ProcessedSession != 2 {
		t.Fatalf("processed = %d, want 2", snap.ProcessedSession)
	}
}

func TestNilTrackerMaintenanceIsSafe(t *testing.T) {
	var tr *Tracker
	// None of these should panic.
	tr.MaintenanceStart(PhaseRebuildingIndex, 10)
	tr.MaintenanceProgress(5)
	tr.MaintenanceDone()
}

func TestMaintenanceCoexistsWithCurrentJob(t *testing.T) {
	dir := t.TempDir()
	tr, err := NewTracker(dir)
	if err != nil {
		t.Fatal(err)
	}

	tr.Start("video.mkv")
	tr.Stage(StageAnalyzing)
	tr.MaintenanceStart(PhaseReconciling, 7)
	tr.MaintenanceProgress(3)

	snap, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.Current == nil || snap.Current.File != "video.mkv" {
		t.Fatalf("Current = %+v, want video.mkv still in flight", snap.Current)
	}
	if snap.Current.Stage != StageAnalyzing {
		t.Fatalf("stage = %q, want %q", snap.Current.Stage, StageAnalyzing)
	}
	if snap.Maintenance == nil {
		t.Fatal("Maintenance is nil, want it to coexist with Current")
	}
	if snap.Maintenance.Phase != PhaseReconciling || snap.Maintenance.Total != 7 || snap.Maintenance.Done != 3 {
		t.Fatalf("Maintenance = %+v, want {reconciling, done 3, total 7}", snap.Maintenance)
	}
}

func TestMaintenanceStartAndDoneFlushUnconditionally(t *testing.T) {
	tr := newTestTracker(t)
	base := time.Now()
	tr.now = func() time.Time { return base }

	before := tr.flushCount
	tr.MaintenanceStart(PhaseRebuildingIndex, 100)
	if tr.flushCount != before+1 {
		t.Fatalf("flushCount = %d, want %d: MaintenanceStart must flush unconditionally", tr.flushCount, before+1)
	}

	before = tr.flushCount
	tr.MaintenanceDone()
	if tr.flushCount != before+1 {
		t.Fatalf("flushCount = %d, want %d: MaintenanceDone must flush unconditionally", tr.flushCount, before+1)
	}
	if tr.snap.Maintenance != nil {
		t.Fatalf("Maintenance = %+v, want nil after MaintenanceDone", tr.snap.Maintenance)
	}
}

func TestMaintenanceProgressNoOpWithoutStart(t *testing.T) {
	tr := newTestTracker(t)
	before := tr.flushCount
	tr.MaintenanceProgress(5) // no MaintenanceStart yet
	if tr.flushCount != before {
		t.Fatalf("flushCount = %d, want unchanged at %d: MaintenanceProgress must no-op with no active run", tr.flushCount, before)
	}
}

func TestMaintenanceProgressFlushThresholds(t *testing.T) {
	tr := newTestTracker(t)
	base := time.Now()
	tr.now = func() time.Time { return base }

	tr.MaintenanceStart(PhaseRebuildingIndex, 1000)
	afterStart := tr.flushCount

	// Sub-1%, no time elapsed: must not flush.
	for i := 1; i <= 5; i++ {
		tr.MaintenanceProgress(i)
	}
	if tr.flushCount != afterStart {
		t.Fatalf("flushCount = %d, want unchanged at %d after sub-threshold updates", tr.flushCount, afterStart)
	}

	// A >=1%% jump must flush immediately, even with no time elapsed.
	tr.MaintenanceProgress(20) // 2% of total
	if tr.flushCount != afterStart+1 {
		t.Fatalf("flushCount = %d, want %d after a >=1%% jump", tr.flushCount, afterStart+1)
	}

	// Advance time past the 250ms floor with no meaningful percent change:
	// must flush on elapsed time alone.
	base = base.Add(300 * time.Millisecond)
	tr.MaintenanceProgress(21)
	if tr.flushCount != afterStart+2 {
		t.Fatalf("flushCount = %d, want %d after the time-based flush", tr.flushCount, afterStart+2)
	}

	// In-memory state must always be current even when a write is skipped.
	tr.MaintenanceProgress(999)
	if tr.snap.Maintenance.Done != 999 {
		t.Fatalf("Done = %d, want 999 (in-memory state must track every update)", tr.snap.Maintenance.Done)
	}
}

func TestMaintenanceProgressManyUpdatesProduceFewWrites(t *testing.T) {
	tr := newTestTracker(t)
	base := time.Now()
	tr.now = func() time.Time { return base }

	tr.MaintenanceStart(PhaseRebuildingIndex, 1000)
	before := tr.flushCount

	n := 1000
	for i := 1; i <= n; i++ {
		tr.MaintenanceProgress(i) // 0.1% steps; no time advance
	}
	writes := tr.flushCount - before
	if writes >= n/2 {
		t.Fatalf("flushCount grew by %d for %d updates, want far fewer writes", writes, n)
	}
	if tr.snap.Maintenance.Done != n {
		t.Fatalf("Done = %d, want %d: in-memory state must still be current", tr.snap.Maintenance.Done, n)
	}
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
