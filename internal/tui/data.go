package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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
	snap           *status.Snapshot
	processedTotal int
	log            []logLine
	service        serviceHealth
	err            error
}

// loadData reads status.json, processed.json, the log tail and the service
// health. Individual failures degrade gracefully rather than aborting.
func loadData(stateDir, logFile string, logTailLines int) dashboardData {
	d := dashboardData{}

	snap, err := status.Read(stateDir)
	if err != nil {
		d.err = err
	}
	d.snap = snap

	d.processedTotal = countProcessed(filepath.Join(stateDir, "processed.json"))
	d.log = tailLog(logFile, logTailLines)
	d.service = serviceStatus()

	return d
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
