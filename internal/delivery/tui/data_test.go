package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/adapter/status"
	"github.com/fernando143/patro/internal/platform/config"

	"github.com/fernando143/patro/internal/adapter/ledger"
)

// deadPID returns the PID of a process that has already exited and been
// reaped, so no live process holds it (barring immediate PID reuse).
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", ":")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot spawn helper process: %v", err)
	}
	return cmd.Process.Pid
}

// testConfig builds a config rooted in a temp dir with an existing inbox.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	inbox := filepath.Join(dir, "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		Inbox:           inbox,
		Library:         filepath.Join(dir, "knowledge"),
		VideoExtensions: []string{".mkv"},
		AnalyzerBackend: "kimi",
		Dir:             dir,
	}
}

// writeSnapshot marshals snap into <stateDir>/status.json.
func writeSnapshot(t *testing.T, stateDir string, snap status.Snapshot) {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, status.FileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCountProcessed(t *testing.T) {
	dir := t.TempDir()

	if got := countProcessed(filepath.Join(dir, "missing.json")); got != 0 {
		t.Errorf("countProcessed(missing file) = %d, want 0", got)
	}

	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countProcessed(corrupt); got != 0 {
		t.Errorf("countProcessed(corrupt file) = %d, want 0", got)
	}

	valid := filepath.Join(dir, "processed.json")
	if err := os.WriteFile(valid, []byte(`{"a.mkv": {}, "b.mkv": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countProcessed(valid); got != 2 {
		t.Errorf("countProcessed(valid file) = %d, want 2", got)
	}
}

func TestParseLogLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want logLine
	}{
		{
			name: "well-formed INFO line",
			line: "2026-07-18 19:17:56,504 INFO patro: Processing meeting.mkv ...",
			want: logLine{Time: "19:17:56", Level: "INFO", Message: "Processing meeting.mkv ..."},
		},
		{
			name: "WARNING line",
			line: "2026-07-18 19:18:00,000 WARNING patro: disk usage high",
			want: logLine{Time: "19:18:00", Level: "WARNING", Message: "disk usage high"},
		},
		{
			name: "unrecognized level falls back to raw",
			line: "2026-07-18 19:18:00,000 DEBUG patro: verbose noise",
			want: logLine{Raw: "2026-07-18 19:18:00,000 DEBUG patro: verbose noise"},
		},
		{
			name: "too few fields falls back to raw",
			line: "not a log line",
			want: logLine{Raw: "not a log line"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLogLine(tt.line); got != tt.want {
				t.Errorf("parseLogLine(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestTailLogReturnsLastNParsedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patro.log")
	content := "2026-07-18 19:17:56,001 INFO patro: one\n" +
		"2026-07-18 19:17:56,002 INFO patro: two\n" +
		"2026-07-18 19:17:56,003 INFO patro: three\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := tailLog(path, 2)
	if len(got) != 2 {
		t.Fatalf("tailLog(n=2) returned %d lines, want 2", len(got))
	}
	if got[0].Message != "two" || got[1].Message != "three" {
		t.Errorf("tailLog(n=2) = %+v, want the last two lines", got)
	}
}

func TestTailLogMissingFile(t *testing.T) {
	if got := tailLog(filepath.Join(t.TempDir(), "missing.log"), 10); got != nil {
		t.Errorf("tailLog(missing file) = %v, want nil", got)
	}
}

// serviceStatus shells out to `systemctl --user is-active patro`, a
// read-only query safe to run for real: this development machine runs an
// actual, active patro.service, so the result is deterministic here.
// serviceStatus shells out to the real systemctl/launchctl with no
// injectable seam, so its result depends on whether this machine happens to
// have a patro service installed. This only checks it returns a valid
// tri-state value without erroring, rather than asserting a specific state.
func TestServiceStatusReadsRealService(t *testing.T) {
	switch got := serviceStatus(); got {
	case serviceActive, serviceInactive, serviceUnknown:
	default:
		t.Errorf("serviceStatus() = %v, want one of serviceActive/serviceInactive/serviceUnknown", got)
	}
}

func TestLoadDataMissingStatus(t *testing.T) {
	cfg := testConfig(t)
	if err := os.WriteFile(filepath.Join(cfg.Inbox, "new.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := loadData(cfg, 10)

	if !d.statusMissing {
		t.Error("statusMissing = false, want true when status.json does not exist")
	}
	if d.statusStale {
		t.Error("statusStale = true, want false when status.json does not exist")
	}
	if d.inboxBacklog != 1 {
		t.Errorf("inboxBacklog = %d, want 1", d.inboxBacklog)
	}
}

func TestLoadDataLiveStatus(t *testing.T) {
	cfg := testConfig(t)
	writeSnapshot(t, cfg.StateDir(), status.Snapshot{
		PID:     os.Getpid(),
		Queue:   []string{"b.mkv"},
		Current: &status.Job{File: "a.mkv", Stage: status.StageTranscribing},
	})

	d := loadData(cfg, 10)

	if d.statusMissing || d.statusStale {
		t.Fatalf("flags = missing:%v stale:%v, want live", d.statusMissing, d.statusStale)
	}
	if d.snap == nil || d.snap.Current == nil || d.snap.Current.File != "a.mkv" {
		t.Errorf("current job not preserved: %+v", d.snap)
	}
	if len(d.snap.Queue) != 1 {
		t.Errorf("queue = %v, want 1 entry", d.snap.Queue)
	}
}

func TestLoadDataStaleStatus(t *testing.T) {
	cfg := testConfig(t)
	for _, name := range []string{"x.mkv", "y.mkv"} {
		if err := os.WriteFile(filepath.Join(cfg.Inbox, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSnapshot(t, cfg.StateDir(), status.Snapshot{
		PID:              deadPID(t),
		Queue:            []string{"x.mkv"},
		Current:          &status.Job{File: "y.mkv", Stage: status.StageAnalyzing},
		ProcessedSession: 2,
		Recent:           []status.Recent{{File: "z.mkv", Title: "Z"}},
	})

	d := loadData(cfg, 10)

	if !d.statusStale {
		t.Fatal("statusStale = false, want true for a snapshot from a dead process")
	}
	if d.statusMissing {
		t.Error("statusMissing = true, want false when the file exists")
	}
	if d.snap.Current != nil {
		t.Errorf("stale current not cleared: %+v", d.snap.Current)
	}
	if len(d.snap.Queue) != 0 {
		t.Errorf("stale queue not cleared: %v", d.snap.Queue)
	}
	if d.snap.ProcessedSession != 2 || len(d.snap.Recent) != 1 {
		t.Errorf("historical data not preserved: %+v", d.snap)
	}
	if d.inboxBacklog != 2 {
		t.Errorf("inboxBacklog = %d, want 2", d.inboxBacklog)
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("processAlive(own pid) = false, want true")
	}
	if processAlive(0) || processAlive(-1) {
		t.Error("processAlive(non-positive pid) = true, want false")
	}
	if pid := deadPID(t); processAlive(pid) {
		t.Errorf("processAlive(%d) = true for a reaped process, want false", pid)
	}
}

func TestCountFlaggedTopicsMissingLedger(t *testing.T) {
	if got := countFlaggedTopics(t.TempDir()); got != 0 {
		t.Errorf("countFlaggedTopics(no ledger) = %d, want 0", got)
	}
}

func TestCountFlaggedTopicsCorruptLedgerDegradesToZero(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reconciliation.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countFlaggedTopics(dir); got != 0 {
		t.Errorf("countFlaggedTopics(corrupt ledger) = %d, want 0", got)
	}
}

func TestCountFlaggedTopicsCountsLatestPerSlug(t *testing.T) {
	dir := t.TempDir()
	entries := struct {
		Entries []ledger.Entry `json:"entries"`
	}{Entries: []ledger.Entry{
		{Slug: "a", Flagged: true},
		{Slug: "b", Flagged: true},
	}}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reconciliation.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countFlaggedTopics(dir); got != 2 {
		t.Errorf("countFlaggedTopics = %d, want 2", got)
	}
}

// loadData must surface the flagged count, and clear a stale (dead-writer)
// snapshot's Maintenance the same way it already clears Current/Queue.
func TestLoadDataSurfacesFlaggedCountAndClearsStaleMaintenance(t *testing.T) {
	cfg := testConfig(t)
	entries := struct {
		Entries []ledger.Entry `json:"entries"`
	}{Entries: []ledger.Entry{{Slug: "x", Flagged: true}}}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.StateDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDir(), "reconciliation.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	writeSnapshot(t, cfg.StateDir(), status.Snapshot{
		PID:         deadPID(t),
		Maintenance: &status.Maintenance{Phase: status.PhaseRebuildingIndex, Done: 3, Total: 10},
	})

	d := loadData(cfg, 10)

	if d.flaggedCount != 1 {
		t.Errorf("flaggedCount = %d, want 1", d.flaggedCount)
	}
	if !d.statusStale {
		t.Fatal("statusStale = false, want true for a snapshot from a dead process")
	}
	if got := d.maintenance(); got != nil {
		t.Errorf("maintenance() = %v, want nil for a stale (dead-writer) snapshot", got)
	}
}

// A live snapshot's Maintenance must survive untouched.
func TestLoadDataSurfacesLiveMaintenance(t *testing.T) {
	cfg := testConfig(t)
	writeSnapshot(t, cfg.StateDir(), status.Snapshot{
		PID:         os.Getpid(),
		Maintenance: &status.Maintenance{Phase: status.PhaseReconciling, Done: 1, Total: 4},
	})

	d := loadData(cfg, 10)

	got := d.maintenance()
	if got == nil || got.Phase != status.PhaseReconciling || got.Done != 1 || got.Total != 4 {
		t.Errorf("maintenance() = %+v, want the live Maintenance from status.json", got)
	}
}

func TestCountInboxBacklogSkipsProcessed(t *testing.T) {
	cfg := testConfig(t)
	video := filepath.Join(cfg.Inbox, "done.mkv")
	if err := os.WriteFile(video, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Inbox, "pending.mkv"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Inbox, "notes.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Record done.mkv with its real size so IsProcessed matches it.
	if err := os.MkdirAll(cfg.StateDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	processed := `{"done.mkv": {"size": 4, "transcript_id": "t1", "processed_at": "2026-07-20T00:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(cfg.StateDir(), "processed.json"), []byte(processed), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := countInboxBacklog(cfg, cfg.StateDir()); got != 1 {
		t.Errorf("countInboxBacklog = %d, want 1 (only pending.mkv)", got)
	}
}
