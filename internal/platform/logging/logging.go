// Package logging provides patro's application logging.
//
// The line format matches Python's default logging format, with the logger
// name "patro":
//
//	2026-07-17 20:50:37,911 INFO patro: message
//
// Output is teed to stdout and, after Init, to the log file. All writes are
// serialized with a mutex, so the package is safe for concurrent use.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	mu     sync.Mutex
	output io.Writer = os.Stdout
	file   *os.File  // the log file opened by Init; nil until then
)

// Init opens logFile (creating parent directories) and tees subsequent log
// output to both stdout and the file. Without Init, output goes to stdout
// only.
func Init(logFile string) error {
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return err
	}
	fh, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	mu.Lock()
	file = fh
	output = io.MultiWriter(os.Stdout, fh)
	mu.Unlock()
	return nil
}

// Close closes the log file opened by Init, if any, and restores
// stdout-only output. It is a no-op when Init was never called or was
// already closed, and, like every other function in this package, is safe
// for concurrent use. Callers that remove the directory holding the log
// file (tests, short-lived commands) must call Close first: an open handle
// keeps the file undeletable on Windows.
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	var err error
	if file != nil {
		err = file.Close()
		file = nil
	}
	output = os.Stdout
	return err
}

// Infof logs a message at INFO level.
func Infof(format string, args ...any) {
	logf("INFO", format, args...)
}

// Warnf logs a message at WARNING level.
func Warnf(format string, args ...any) {
	logf("WARNING", format, args...)
}

// Errorf logs a message at ERROR level.
func Errorf(format string, args ...any) {
	logf("ERROR", format, args...)
}

// logf writes one formatted log line: "<yyyy-MM-dd HH:mm:ss,mmm> LEVEL
// patro: <message>\n". The comma before the milliseconds matches Python's
// default asctime format.
func logf(level, format string, args ...any) {
	now := time.Now()
	message := fmt.Sprintf(format, args...)
	mu.Lock()
	fmt.Fprintf(output, "%s,%03d %s patro: %s\n",
		now.Format("2006-01-02 15:04:05"), now.Nanosecond()/1e6, level, message)
	mu.Unlock()
}
