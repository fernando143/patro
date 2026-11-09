//go:build !windows

package tui

import (
	"errors"
	"os"
	"syscall"
)

// processExists reports whether a process with this PID currently exists. It
// sends signal 0 (a no-op permission/existence probe); EPERM still proves the
// process exists. patro's serve and dashboard always run as the same user on
// the same machine, so this is a reliable liveness check for the snapshot.
func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
