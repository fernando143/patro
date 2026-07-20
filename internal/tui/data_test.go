package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/status"
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
