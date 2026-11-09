package setup

import "testing"

// These cover the pure Windows renderers and parsers. They deliberately carry
// no GOOS suffix so they also run on macOS and Linux, where the Windows service
// itself cannot: the rules are platform-neutral string handling, and testing
// them only on Windows would leave them unchecked on most CI runs.

func TestWindowsRunCommand(t *testing.T) {
	tests := []struct {
		name       string
		exe        string
		configPath string
		want       string
	}{
		{
			name:       "plain paths",
			exe:        `C:\Program Files\patro\patro.exe`,
			configPath: `C:\Users\me\.config\patro\config.yaml`,
			want:       `"C:\Program Files\patro\patro.exe" serve --config "C:\Users\me\.config\patro\config.yaml"`,
		},
		{
			// Backslashes must reach the registry verbatim, so the command
			// cannot be rendered with a Go quoted literal.
			name:       "backslashes are not escaped",
			exe:        `C:\patro.exe`,
			configPath: `D:\a b\config.yaml`,
			want:       `"C:\patro.exe" serve --config "D:\a b\config.yaml"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowsRunCommand(tc.exe, tc.configPath); got != tc.want {
				t.Errorf("windowsRunCommand(%q, %q) = %q, want %q", tc.exe, tc.configPath, got, tc.want)
			}
		})
	}
}

// parseWindowsArgv has to round-trip what windowsRunCommand writes: restarting
// the watcher reads its executable and arguments back out of the registry.
func TestParseWindowsArgvRoundTripsWindowsRunCommand(t *testing.T) {
	exe := `C:\Program Files\patro\patro.exe`
	configPath := `C:\Users\me\.config\patro\config.yaml`

	gotExe, gotArgs, ok := parseWindowsArgv(windowsRunCommand(exe, configPath))
	if !ok {
		t.Fatalf("parseWindowsArgv(rendered command) ok = false, want true")
	}
	if gotExe != exe {
		t.Errorf("exe = %q, want %q", gotExe, exe)
	}
	want := []string{"serve", "--config", configPath}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
}

func TestParseWindowsArgv(t *testing.T) {
	tests := []struct {
		name    string
		command string
		exe     string
		args    []string
		ok      bool
	}{
		{
			name:    "exe only",
			command: `"C:\a b\patro.exe"`,
			exe:     `C:\a b\patro.exe`,
			ok:      true,
		},
		{
			name:    "unquoted command is rejected",
			command: `C:\patro.exe serve`,
			ok:      false,
		},
		{
			name:    "unbalanced quotes are rejected",
			command: `"C:\patro.exe" --config "C:\unclosed`,
			ok:      false,
		},
		{
			name:    "empty command is rejected",
			command: "   ",
			ok:      false,
		},
		{
			name:    "bare flags without a value",
			command: `"patro.exe" serve`,
			exe:     "patro.exe",
			args:    []string{"serve"},
			ok:      true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exe, args, ok := parseWindowsArgv(tc.command)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if exe != tc.exe {
				t.Errorf("exe = %q, want %q", exe, tc.exe)
			}
			if len(args) != len(tc.args) {
				t.Fatalf("args = %v, want %v", args, tc.args)
			}
			for i := range tc.args {
				if args[i] != tc.args[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tc.args[i])
				}
			}
		})
	}
}

func TestSanitizeImageName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"patro.exe", "patro.exe"},
		{"patro", "patro"},
		// A quote or a semicolon must not reach the PowerShell -Filter
		// expression that the process lookup builds.
		{"pa'tro.exe", "patro.exe"},
		{"pa;tro.exe", "patro.exe"},
		{"", "patro.exe"},
		{"'\";", "patro.exe"},
	}
	for _, tc := range tests {
		if got := sanitizeImageName(tc.in); got != tc.want {
			t.Errorf("sanitizeImageName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseRegQueryValue(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "typical output",
			out: "\r\nHKEY_CURRENT_USER\\Environment\r\n" +
				"    ASSEMBLYAI_API_KEY    REG_SZ    secret-key\r\n\r\n",
			want: "secret-key",
		},
		{
			// reg localizes its headers, so only the value line is parsed.
			name: "localized header is ignored",
			out: "\r\nHKEY_CURRENT_USER\\Environment\r\n" +
				"    ASSEMBLYAI_API_KEY    REG_SZ    clave\r\n",
			want: "clave",
		},
		{
			name: "value containing spaces is kept whole",
			out: "HKEY_CURRENT_USER\\Environment\n" +
				"    ASSEMBLYAI_API_KEY    REG_SZ    two words\n",
			want: "two words",
		},
		{
			name: "empty value",
			out: "HKEY_CURRENT_USER\\Environment\n" +
				"    ASSEMBLYAI_API_KEY    REG_SZ    \n",
			want: "",
		},
		{
			name: "value absent",
			out: "HKEY_CURRENT_USER\\Environment\n" +
				"    OTHER_KEY    REG_SZ    nope\n",
			want: "",
		},
		{
			name: "empty output",
			out:  "",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRegQueryValue(tc.out, "ASSEMBLYAI_API_KEY"); got != tc.want {
				t.Errorf("parseRegQueryValue(...) = %q, want %q", got, tc.want)
			}
		})
	}
}
