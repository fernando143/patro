package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// withCapturedOutput swaps the package-level output sink for buf for the
// duration of fn, then restores whatever was there before (Init mutates the
// same global, so tests must not leak state into each other).
func withCapturedOutput(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	mu.Lock()
	prev := output
	output = buf
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		output = prev
		mu.Unlock()
	})
}

func TestInfofFormat(t *testing.T) {
	var buf bytes.Buffer
	withCapturedOutput(t, &buf)

	Infof("processing %s", "meeting.mkv")

	line := buf.String()
	want := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3} INFO patro: processing meeting\.mkv\n$`)
	if !want.MatchString(line) {
		t.Errorf("Infof output = %q, want to match %s", line, want)
	}
}

func TestWarnfAndErrorfLevels(t *testing.T) {
	var buf bytes.Buffer
	withCapturedOutput(t, &buf)

	Warnf("disk usage at %d%%", 90)
	Errorf("upload failed: %s", "timeout")

	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2:\n%s", len(lines), buf.String())
	}
	if !bytes.Contains(lines[0], []byte("WARNING patro: disk usage at 90%")) {
		t.Errorf("Warnf line = %q, want WARNING level with formatted message", lines[0])
	}
	if !bytes.Contains(lines[1], []byte("ERROR patro: upload failed: timeout")) {
		t.Errorf("Errorf line = %q, want ERROR level with formatted message", lines[1])
	}
}

func TestInitTeesToFile(t *testing.T) {
	// Init replaces the global sink permanently for the process, so make
	// sure it is restored for any test running after this one.
	mu.Lock()
	prev := output
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		output = prev
		mu.Unlock()
	})

	dir := t.TempDir()
	logFile := filepath.Join(dir, "nested", "patro.log")

	if err := Init(logFile); err != nil {
		t.Fatalf("Init error = %v", err)
	}

	Infof("service started")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", logFile, err)
	}
	if !bytes.Contains(data, []byte("INFO patro: service started")) {
		t.Errorf("log file content = %q, want it to contain the Infof message", data)
	}
}

func TestInitParentDirCreationError(t *testing.T) {
	dir := t.TempDir()
	// A plain file where a parent directory of the log file must be
	// created: os.MkdirAll fails because a path component is not a
	// directory.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	err := Init(filepath.Join(blocker, "nested", "patro.log"))
	if err == nil {
		t.Fatal("Init error = nil, want error when the log directory cannot be created")
	}
}

func TestInitOpenFileError(t *testing.T) {
	dir := t.TempDir()
	// logFile itself is a directory: OpenFile with O_WRONLY must fail.
	logDir := filepath.Join(dir, "patro.log")
	if err := os.Mkdir(logDir, 0o755); err != nil {
		t.Fatalf("Mkdir error = %v", err)
	}

	err := Init(logDir)
	if err == nil {
		t.Fatal("Init error = nil, want error when the log file path is a directory")
	}
}
