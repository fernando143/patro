package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/state"
	"github.com/fernando143/patro/internal/status"
)

// serviceHealth is the tri-state health of the background service.
type serviceHealth int

const (
	serviceUnknown serviceHealth = iota
	serviceActive
	serviceInactive
)

// logLine is one parsed line of patro.log.
type logLine struct {
	Time    string // HH:MM:SS
	Level   string // INFO / WARNING / ERROR
	Message string
	Raw     string // original line when it doesn't match the format
}

// dashboardData is the full snapshot the view renders, loaded once per tick.
type dashboardData struct {
	snap *status.Snapshot
	// statusMissing is true when status.json does not exist at all: no serve
	// process has ever published live state to this state directory (or the
	// one running predates the status feature).
	statusMissing bool
	// statusStale is true when status.json exists but the serve process that
	// wrote it is gone; its queue/current are cleared because they describe
	// work that is no longer running.
	statusStale bool
	// inboxBacklog counts unprocessed videos sitting in the inbox. It is only
	// computed when there is no live snapshot, as a fallback for the queue.
	inboxBacklog   int
	processedTotal int
	log            []logLine
	service        serviceHealth
	err            error
}

// loadData reads status.json, processed.json, the log tail and the service
// health. Individual failures degrade gracefully rather than aborting.
func loadData(cfg *config.Config, logTailLines int) dashboardData {
	stateDir := cfg.StateDir()
	d := dashboardData{}

	snap, err := status.Read(stateDir)
	if err != nil {
		d.err = err
	}
	d.snap = snap
	d.statusMissing = snap == nil && err == nil
	if snap != nil && !processAlive(snap.PID) {
		// Leftover file from a previous serve run: the queue and in-flight
		// job describe work that is no longer running, so drop them and keep
		// only the historical counters and lists.
		d.statusStale = true
		snap.Current = nil
		snap.Queue = nil
	}
	if d.snap == nil || d.statusStale {
		d.inboxBacklog = countInboxBacklog(cfg, stateDir)
	}

	d.processedTotal = countProcessed(filepath.Join(stateDir, "processed.json"))
	d.log = tailLog(cfg.LogFile(), logTailLines)
	d.service = serviceStatus()

	return d
}

// processAlive reports whether a process with this PID currently exists. It
// sends signal 0 (a no-op permission/existence probe); EPERM still proves the
// process exists. patro's serve and dashboard always run as the same user on
// the same machine, so this is a reliable liveness check for the snapshot.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// countInboxBacklog counts video files in the inbox not yet recorded in
// processed.json (same name and size), i.e. the work a healthy serve would
// pick up. It is the queue fallback when no live snapshot is available.
func countInboxBacklog(cfg *config.Config, stateDir string) int {
	entries, err := os.ReadDir(cfg.Inbox)
	if err != nil {
		return 0
	}
	st := state.New(stateDir)
	backlog := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(cfg.Inbox, entry.Name())
		if cfg.IsVideo(path) && !st.IsProcessed(path) {
			backlog++
		}
	}
	return backlog
}

// countProcessed returns the number of entries in processed.json.
func countProcessed(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0
	}
	return len(m)
}

// tailLog reads the last n lines of the log file and parses them.
func tailLog(path string, n int) []logLine {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	parsed := make([]logLine, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		parsed = append(parsed, parseLogLine(l))
	}
	return parsed
}

// parseLogLine parses "2026-07-18 19:17:56,504 INFO patro: message". Lines
// that do not match are kept verbatim in Raw.
func parseLogLine(line string) logLine {
	// Expected prefix: "<date> <time>,<ms> <LEVEL> patro: <msg>"
	fields := strings.SplitN(line, " ", 4)
	if len(fields) < 4 {
		return logLine{Raw: line}
	}
	timePart := fields[1] // HH:MM:SS,mmm
	if i := strings.IndexByte(timePart, ','); i >= 0 {
		timePart = timePart[:i]
	}
	level := fields[2]
	rest := fields[3] // "patro: message"
	msg := strings.TrimPrefix(rest, "patro: ")
	if level != "INFO" && level != "WARNING" && level != "ERROR" {
		return logLine{Raw: line}
	}
	return logLine{Time: timePart, Level: level, Message: msg}
}

// serviceStatus queries the platform service manager for patro's state.
func serviceStatus() serviceHealth {
	switch runtime.GOOS {
	case "linux":
		out, _ := exec.Command("systemctl", "--user", "is-active", "patro").Output()
		switch strings.TrimSpace(string(out)) {
		case "active":
			return serviceActive
		case "inactive", "failed", "deactivating":
			return serviceInactive
		default:
			return serviceUnknown
		}
	case "darwin":
		uid := os.Getuid()
		err := exec.Command("launchctl", "print", fmt.Sprintf("gui/%d/com.patro", uid)).Run()
		if err == nil {
			return serviceActive
		}
		return serviceInactive
	default:
		return serviceUnknown
	}
}
