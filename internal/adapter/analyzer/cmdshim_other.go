//go:build !windows

package analyzer

import (
	"context"
	"os/exec"
)

// execCLI builds the process for one non-interactive CLI run. Outside Windows
// there is no shim indirection: PATH lookup returns the script itself, and the
// kernel runs it through its shebang.
func execCLI(ctx context.Context, binaryPath string, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, binaryPath, args...)
}
