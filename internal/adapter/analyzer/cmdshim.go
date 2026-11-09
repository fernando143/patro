// Command-line handling for the npm-style CLI shims the analyzer backends
// shell out to.
//
// On Windows, npm, pnpm and yarn install a global command as three files: a
// POSIX-style `pi`, a `pi.ps1`, and a `pi.cmd`. Go's exec.LookPath honors
// PATHEXT, so looking up "pi" resolves to pi.cmd — but CreateProcess cannot
// execute a batch file, so running that path directly fails with "not a valid
// Win32 application". The only way to start it is through the command
// interpreter, which brings its own quoting rules: it is not the C runtime's
// backslash convention that Go's own escaping emits.
//
// Everything here is platform-neutral so the rendering rules can be unit
// tested on every OS; only the process construction in
// cmdshim_windows.go/cmdshim_other.go is build-tagged.
package analyzer

import (
	"path/filepath"
	"strings"
)

// isCmdShim reports whether binaryPath is a batch-file shim that has to be
// started through the command interpreter.
func isCmdShim(binaryPath string) bool {
	switch strings.ToLower(filepath.Ext(binaryPath)) {
	case ".cmd", ".bat":
		return true
	default:
		return false
	}
}

// windowsCommandLine renders one invocation as a single cmd.exe command line:
// the binary and every argument, each double-quoted so that the
// metacharacters cmd.exe would otherwise interpret — & | < > ^ — stay literal.
func windowsCommandLine(binaryPath string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteForCmd(binaryPath))
	for _, arg := range args {
		parts = append(parts, quoteForCmd(arg))
	}
	return strings.Join(parts, " ")
}

// quoteForCmd quotes one command-line token the way cmd.exe expects.
//
// Two rules matter. A raw newline terminates a cmd.exe command, so everything
// after it would be silently dropped: newlines and CRLF collapse to a space,
// which prose prompts tolerate. And an embedded double quote is escaped with a
// backslash, the convention the C runtime — and therefore the Node-based CLI
// at the end of the shim — uses when it splits the line back into argv.
func quoteForCmd(arg string) string {
	arg = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(arg)
	arg = strings.ReplaceAll(arg, `"`, `\"`)
	return `"` + arg + `"`
}
