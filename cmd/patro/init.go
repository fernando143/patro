// Interactive setup wizard for patro (the init command).
//
// Guides the user from a fresh install to a running user-level service:
// API key, inbox and library folders, analyzer backend, config file, and
// an optional systemd/LaunchAgent background service.
//
// This is a port of scribe/install_wizard.py, minus the Python/venv/pip
// steps that no longer apply to the Go binary. Prompts use plain bufio +
// fmt, with no external dependencies beyond gopkg.in/yaml.v3.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fernando143/patro/internal/config"
)

// runInitPrompt runs the line-based setup wizard used as a fallback when
// stdin/stdout is not an interactive terminal. flagConfig is the --config
// value ("" when not given).
func runInitPrompt(flagConfig string) int {
	p := &prompter{reader: bufio.NewReader(os.Stdin)}

	fmt.Println("+------------------------------------------------------+")
	fmt.Println("| patro setup                                          |")
	fmt.Println("+------------------------------------------------------+")
	fmt.Println()
	fmt.Println("This wizard will:")
	fmt.Println("  1. Ask for your AssemblyAI API key (transcription).")
	fmt.Println("  2. Ask where patro should listen for recordings.")
	fmt.Println("  3. Ask where to write the knowledge library.")
	fmt.Println("  4. Ask which local AI should write the notes.")
	fmt.Println("  5. Write the configuration file.")
	fmt.Println("  6. Offer to install and start a user-level background service.")
	fmt.Println()

	apiKey := p.required("Enter your AssemblyAI API key", "")
	fmt.Println()

	inbox := p.promptPath("Enter the path to your recordings folder", "~/Videos/obs")
	fmt.Println()

	libraryDir := p.promptPath("Enter the path to your knowledge library folder", "./knowledge")
	fmt.Println()

	backend := p.promptBackend()
	binaryPath := p.promptBinary(backend)
	fmt.Println()

	configPath := determineConfigPath(flagConfig)
	if err := writeConfig(configPath, inbox, libraryDir, backend, binaryPath); err != nil {
		fmt.Printf("Error: cannot write %s: %v\n", configPath, err)
		return 1
	}
	fmt.Printf("Updated %s\n", configPath)
	fmt.Println()

	serviceInstalled := false
	if p.confirm("Install and start the background service now?", true) {
		serviceInstalled = installService(apiKey, configPath)
	}
	fmt.Println()

	printCompletion(configPath, serviceInstalled)
	return 0
}

// printCompletion prints the shared "installation complete" summary for both
// the TUI and the prompt-based wizards.
func printCompletion(configPath string, serviceInstalled bool) {
	fmt.Println("+------------------------------------------------------+")
	fmt.Println("| Installation complete                                |")
	fmt.Println("+------------------------------------------------------+")
	fmt.Printf("Config: %s\n", configPath)
	fmt.Println("Dashboard: patro run dashboard   ·   Web viewer: patro run web")
	switch runtime.GOOS {
	case "linux":
		fmt.Println("View logs: journalctl --user -u patro -f")
		fmt.Println("Check status: systemctl --user status patro")
	case "darwin":
		fmt.Println("View logs: tail -f /tmp/patro.out.log /tmp/patro.err.log")
	}
	if serviceInstalled {
		fmt.Println("Hint: for Homebrew installs, `brew services start patro` also works.")
	}
}

// prompter wraps stdin reading behind question/confirm helpers.
type prompter struct {
	reader *bufio.Reader
}

// readLine reads one trimmed line from stdin.
func (p *prompter) readLine() string {
	line, _ := p.reader.ReadString('\n')
	return strings.TrimSpace(line)
}

// ask prints label (with the default in brackets when non-empty) and
// returns the answer, or def when the user just hits enter.
func (p *prompter) ask(label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	if value := p.readLine(); value != "" {
		return value
	}
	return def
}

// required re-asks until the answer (or the default) is non-empty.
func (p *prompter) required(label, def string) string {
	for {
		if value := p.ask(label, def); value != "" {
			return value
		}
		fmt.Println("This field is required.")
	}
}

// confirm asks a yes/no question; def is the answer on a bare enter.
func (p *prompter) confirm(label string, def bool) bool {
	suffix := " [y/N]: "
	if def {
		suffix = " [Y/n]: "
	}
	for {
		fmt.Print(label + suffix)
		switch strings.ToLower(p.readLine()) {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			fmt.Println("Please answer 'y' or 'n'.")
		}
	}
}

// promptPath asks for a folder path. "~" is expanded and the result must
// be absolute; a missing folder is only created when the user confirms.
func (p *prompter) promptPath(label, def string) string {
	for {
		raw := p.required(label, def)
		path := expandPath(raw)
		info, err := os.Stat(path)
		switch {
		case err == nil && info.IsDir():
			return path
		case err == nil:
			fmt.Printf("%s exists but is not a folder. Please provide a different path.\n", path)
		case os.IsNotExist(err):
			if p.confirm(fmt.Sprintf("Folder does not exist: %s. Create it?", path), false) {
				if mkErr := os.MkdirAll(path, 0o755); mkErr != nil {
					fmt.Printf("Cannot create %s: %v\n", path, mkErr)
					continue
				}
				return path
			}
			fmt.Println("Please provide a different path.")
		default:
			fmt.Printf("Cannot access %s: %v\n", path, err)
		}
	}
}

// promptBackend asks which local AI CLI should write the notes.
func (p *prompter) promptBackend() string {
	for {
		backend := strings.ToLower(p.ask("Which local AI should write the knowledge library? (kimi/claude)", "kimi"))
		if backend == "kimi" || backend == "claude" {
			return backend
		}
		fmt.Println("Please answer 'kimi' or 'claude'.")
	}
}

// promptBinary locates the named CLI, letting the user confirm the found
// path or type an absolute executable path instead.
func (p *prompter) promptBinary(name string) string {
	if found, err := exec.LookPath(name); err == nil {
		if abs, absErr := filepath.Abs(found); absErr == nil {
			found = abs
		}
		if p.confirm(fmt.Sprintf("Found '%s' at %s. Use it?", name, found), true) {
			return found
		}
	}
	for {
		raw := p.required(fmt.Sprintf("Enter the absolute path to the '%s' executable", name), "")
		path := expandPath(raw)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
		fmt.Printf("'%s' is not an executable file. Please try again.\n", path)
	}
}

// determineConfigPath picks the config file to write: the --config value
// when given, else ./config.yaml when it exists (update in place), else
// ~/.config/patro/config.yaml.
func determineConfigPath(flagConfig string) string {
	path := flagConfig
	if path == "" {
		if _, err := os.Stat("config.yaml"); err == nil {
			path = "config.yaml"
		} else if userPath := config.UserConfigPath(); userPath != "" {
			path = userPath
		} else {
			path = "config.yaml"
		}
	}
	return expandPath(path)
}

// writeConfig writes the wizard answers as YAML. An existing file is
// updated in place: unknown keys already present are preserved.
func writeConfig(path, inbox, libraryDir, backend, binaryPath string) error {
	data := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(raw, &data)
	}
	if data == nil {
		data = map[string]any{}
	}
	data["inbox"] = inbox
	data["library"] = libraryDir
	data["analyzer_backend"] = backend
	if backend == "kimi" {
		data["kimi_path"] = binaryPath
		delete(data, "claude_path")
	} else {
		data["claude_path"] = binaryPath
		delete(data, "kimi_path")
	}

	out, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// installService installs and starts the user-level background service
// for the current OS. It returns true when the service was set up. Every
// external command failure is a warning, never fatal to the wizard.
func installService(apiKey, configPath string) bool {
	switch runtime.GOOS {
	case "linux":
		return installLinuxService(apiKey, configPath)
	case "darwin":
		return installMacService(apiKey, configPath)
	default:
		fmt.Printf("Unsupported OS: %s. Service installation skipped.\n", runtime.GOOS)
		return false
	}
}

// installLinuxService writes a systemd --user unit plus an API-key
// drop-in, then enables and starts it.
func installLinuxService(apiKey, configPath string) bool {
	exe, err := os.Executable()
	if err != nil {
		fmt.Printf("Warning: cannot determine own executable path: %v\n", err)
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Warning: cannot determine home directory: %v\n", err)
		return false
	}

	unitDir := filepath.Join(home, ".config", "systemd", "user")
	unit := fmt.Sprintf(`[Unit]
Description=patro: transcribe OBS recordings into a Markdown knowledge library
After=default.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s serve --config %s
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`, filepath.Dir(configPath), exe, configPath)

	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		fmt.Printf("Warning: cannot create %s: %v\n", unitDir, err)
		return false
	}
	unitPath := filepath.Join(unitDir, "patro.service")
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		fmt.Printf("Warning: cannot write %s: %v\n", unitPath, err)
		return false
	}
	fmt.Printf("Wrote %s\n", unitPath)

	overrideDir := filepath.Join(unitDir, "patro.service.d")
	overridePath := filepath.Join(overrideDir, "override.conf")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		fmt.Printf("Warning: cannot create %s: %v\n", overrideDir, err)
		return true
	}
	override := "[Service]\nEnvironment=ASSEMBLYAI_API_KEY=" + apiKey + "\n"
	if err := os.WriteFile(overridePath, []byte(override), 0o600); err != nil {
		fmt.Printf("Warning: cannot write %s: %v\n", overridePath, err)
		return true
	}
	fmt.Printf("Wrote API key to %s\n", overridePath)

	runWarn("systemctl", "--user", "daemon-reload")
	if runWarn("systemctl", "--user", "enable", "--now", "patro") {
		fmt.Println("Service started and enabled")
	}
	return true
}

// installMacService writes a LaunchAgent plist (with the API key embedded)
// and bootstraps it.
func installMacService(apiKey, configPath string) bool {
	exe, err := os.Executable()
	if err != nil {
		fmt.Printf("Warning: cannot determine own executable path: %v\n", err)
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Warning: cannot determine home directory: %v\n", err)
		return false
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.patro</string>

    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
        <string>--config</string>
        <string>%s</string>
    </array>

    <key>WorkingDirectory</key>
    <string>%s</string>

    <key>EnvironmentVariables</key>
    <dict>
        <key>ASSEMBLYAI_API_KEY</key>
        <string>%s</string>
    </dict>

    <key>KeepAlive</key>
    <true/>

    <key>RunAtLoad</key>
    <true/>

    <key>StandardOutPath</key>
    <string>/tmp/patro.out.log</string>

    <key>StandardErrorPath</key>
    <string>/tmp/patro.err.log</string>
</dict>
</plist>
`, xmlEscape(exe), xmlEscape(configPath), xmlEscape(filepath.Dir(configPath)), xmlEscape(apiKey))

	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		fmt.Printf("Warning: cannot create %s: %v\n", plistDir, err)
		return false
	}
	plistPath := filepath.Join(plistDir, "com.patro.plist")
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		fmt.Printf("Warning: cannot write %s: %v\n", plistPath, err)
		return false
	}
	fmt.Printf("Wrote %s\n", plistPath)

	uid := os.Getuid()
	// Bootout first in case an older agent is loaded; failure is expected
	// when nothing is loaded, so its error is ignored entirely.
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/com.patro", uid)).Run()
	if runWarn("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath) {
		fmt.Println("LaunchAgent started")
	}
	return true
}

// runWarn runs an external command, streaming its output; on failure it
// prints a warning and returns false instead of aborting the wizard.
func runWarn(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Warning: %s %s failed: %v\n", name, strings.Join(args, " "), err)
		return false
	}
	return true
}

// xmlEscape escapes the five XML predefined entities for plist strings.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
