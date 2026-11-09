package setup

import (
	"strings"
)

// Pure Windows autostart renderers and parsers.
//
// They live in a file without a GOOS suffix because they depend on nothing
// platform-specific: keeping them here is what lets the rules be tested on
// macOS and Linux, where the Windows autostart itself cannot run.

// windowsRunCommand renders the value stored in the per-user Run key: the
// executable, quoted so a path with spaces survives, plus the explicit config
// path so the watcher never depends on its working directory.
func windowsRunCommand(exe, configPath string) string {
	return `"` + exe + `" serve --config "` + configPath + `"`
}

// WindowsAutostartName is the Run-key value name — the identity of the watcher
// in the per-user autostart list. It is exported, and declared on every
// platform, so the dashboard can look for the same entry the installer wrote
// instead of keeping a second literal that drifts.
const WindowsAutostartName = "patro"

// parseWindowsArgv splits a command line of double-quoted tokens, which is the
// format windowsRunCommand writes: `"C:\a b\patro.exe" serve --config "C:\c d.yaml"`.
// It returns ok=false for anything that does not start with a quoted token,
// because an unquoted leading token cannot contain the spaces an installed path
// may have, and silently mis-splitting it would start the wrong program.
func parseWindowsArgv(command string) (exe string, args []string, ok bool) {
	command = strings.TrimSpace(command)
	if command == "" || command[0] != '"' {
		return "", nil, false
	}
	end := strings.IndexByte(command[1:], '"')
	if end < 0 {
		return "", nil, false
	}
	exe = command[1 : end+1]
	rest := strings.TrimSpace(command[end+2:])
	if rest == "" {
		return exe, nil, true
	}
	fields, ok := splitQuoted(rest)
	if !ok {
		return "", nil, false
	}
	return exe, fields, true
}

// splitQuoted splits the remainder of a command line into tokens, honouring
// double quotes and dropping the quotes.
func splitQuoted(s string) ([]string, bool) {
	var (
		tokens  []string
		current strings.Builder
		inQuote bool
		started bool
	)
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			started = true
		case r == ' ' && !inQuote:
			if started {
				tokens = append(tokens, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if inQuote {
		return nil, false
	}
	if started {
		tokens = append(tokens, current.String())
	}
	return tokens, true
}

// parseRegQueryValue pulls the value of name out of `reg query` output:
//
//	HKEY_CURRENT_USER\Environment
//	    ASSEMBLYAI_API_KEY    REG_SZ    secret
//
// Only the header lines of that output are localized, and `reg` does not quote
// REG_SZ values, so the value line is matched by its first field and read from
// its third one onward. That keeps a value containing spaces intact while
// ignoring the localized text above it. An empty value yields "", which is
// what "the key is not configured" must look like.
func parseRegQueryValue(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != name {
			continue
		}
		return strings.Join(fields[2:], " ")
	}
	return ""
}

// sanitizeImageName reduces s to the characters a Windows image name can carry,
// so it can be embedded in a PowerShell -Filter expression without quoting
// games. It falls back to patro's own name when nothing usable survives.
func sanitizeImageName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "patro.exe"
	}
	return b.String()
}
