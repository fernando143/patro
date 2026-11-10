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
// the binary and every argument, each wrapped in the one pair of quotes cmd.exe
// strips, with the tokens themselves made safe by quoteForCmd.
func windowsCommandLine(binaryPath string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteForCmd(binaryPath))
	for _, arg := range args {
		parts = append(parts, quoteForCmd(arg))
	}
	return strings.Join(parts, " ")
}

// quoteForCmd wraps one token in the pair of double quotes cmd.exe strips,
// after making the token safe to sit between them.
//
// cmd.exe parses this rendered line itself before the batch file ever sees it,
// and it has no escape character: every double quote toggles its in-quote state
// and a backslash is an ordinary character. Escaping an embedded quote as \"
// would leave the quote count unbalanced, close the quoted region early, and let
// a later & | < > ^ be read as a command separator — on text this program does
// not control, because analyzer prompts embed transcript and topic content and
// the gray-zone question embeds topic names. The token is therefore made safe
// instead of escaped: newlines collapse to a space (a raw one ends the command
// outright) and an embedded double quote becomes a single quote, so no unpaired
// toggle can occur and everything up to the closing delimiter stays literal.
//
// Two limits remain, documented rather than hidden: cmd.exe still expands
// %VAR% inside quotes, and a prompt sent through this route shows any JSON
// example with single quotes. Only a backend whose resolved binary is a
// .cmd/.bat shim takes the route at all.
func quoteForCmd(arg string) string {
	arg = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(arg)
	arg = strings.ReplaceAll(arg, `"`, `'`)
	return `"` + arg + `"`
}
