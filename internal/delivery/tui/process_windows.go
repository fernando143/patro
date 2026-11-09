//go:build windows

package tui

import "syscall"

// processExists reports whether a process with this PID currently exists.
//
// The Unix signal-0 probe cannot work here: Windows os.FindProcess succeeds for
// any PID, and os.Process.Signal only supports Kill, so every probe failed and
// every status snapshot looked stale. OpenProcess with query-only rights is the
// Windows equivalent — it opens a handle we immediately close and fails when no
// such process exists. PROCESS_QUERY_INFORMATION is the strongest query right
// syscall exports for Windows without a wider dependency; opening patro's own
// user process with it always succeeds.
func processExists(pid int) bool {
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = syscall.CloseHandle(handle)
	return true
}
