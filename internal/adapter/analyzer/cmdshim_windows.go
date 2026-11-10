//go:build windows

package analyzer

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// execCLI builds the process for one non-interactive CLI run.
//
// A batch-file shim is handed to cmd.exe as one pre-rendered command line
// assigned to SysProcAttr.CmdLine. Building it here rather than letting Go
// assemble the arguments is deliberate: cmd.exe does not understand the C
// runtime's backslash escaping, so the per-argument form would arrive mangled.
//
// The command is run as `cmd /d /s /c "…"`: /d skips AutoRun scripts, and /s
// together with the outer quotes makes cmd strip exactly that one outer pair
// and treat the rest literally, which is what keeps quoted arguments intact.
//
// Caveat: cancellation kills cmd.exe, and its child (node) can outlive it.
// The analyzer timeout therefore bounds the wait, not necessarily the work.
func execCLI(ctx context.Context, binaryPath string, args []string) *exec.Cmd {
	if !isCmdShim(binaryPath) {
		return exec.CommandContext(ctx, binaryPath, args...)
	}
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, comspec)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: comspec + ` /d /s /c "` + windowsCommandLine(binaryPath, args) + `"`,
	}
	return cmd
}
