package setup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ErrNoService reports that no user-level patro service is installed, so
// there is nothing to update or restart.
var ErrNoService = errors.New("no patro background service is installed; run `patro init` to install one")

// apiKeyPlistRe matches the API key's value inside the LaunchAgent plist,
// capturing the opening tags, the value itself, and the closing tag.
// SetAPIKey rewrites only the value: regenerating the whole plist would also
// recompute ProgramArguments from the *currently running* binary, silently
// repointing the user's agent at, say, a local dev build.
var apiKeyPlistRe = regexp.MustCompile(`(?s)(<key>ASSEMBLYAI_API_KEY</key>\s*<string>)([^<]*)(</string>)`)

// InstallService installs and starts the user-level background service for
// the current OS. It returns the lines the caller should print and whether
// the service was set up. External command failures become log lines, never
// errors: a half-installed service is still better than none.
func InstallService(apiKey, configPath string) (log []string, installed bool) {
	switch runtime.GOOS {
	case "linux":
		return installLinuxService(apiKey, configPath)
	case "darwin":
		return installMacService(apiKey, configPath)
	default:
		return []string{fmt.Sprintf("Unsupported OS: %s. Service installation skipped.", runtime.GOOS)}, false
	}
}

// SetAPIKey rewrites the AssemblyAI key of an already-installed service and
// reloads it so the change takes effect. It returns ErrNoService when nothing
// is installed.
func SetAPIKey(apiKey string) error {
	switch runtime.GOOS {
	case "linux":
		return setLinuxAPIKey(apiKey)
	case "darwin":
		return setMacAPIKey(apiKey)
	default:
		return ErrNoService
	}
}

// RestartService reloads and restarts an installed service without touching
// the API key. A config change (analyzer backend, paths) is only picked up by
// a running `patro serve` after this, since it loads the config once at
// startup.
func RestartService() error {
	switch runtime.GOOS {
	case "linux":
		unitPath, err := linuxUnitPath()
		if err != nil {
			return err
		}
		if _, err := os.Stat(unitPath); err != nil {
			return ErrNoService
		}
		if err := runQuiet("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return runQuiet("systemctl", "--user", "restart", "patro")
	case "darwin":
		plistPath, err := macPlistPath()
		if err != nil {
			return err
		}
		if _, err := os.Stat(plistPath); err != nil {
			return ErrNoService
		}
		return reloadLaunchAgent(plistPath)
	default:
		return ErrNoService
	}
}

// ServiceAPIKeyConfigured reports whether the installed service carries a
// non-empty ASSEMBLYAI_API_KEY. Any read failure counts as "not configured".
func ServiceAPIKeyConfigured() bool {
	switch runtime.GOOS {
	case "linux":
		path, err := linuxOverridePath()
		if err != nil {
			return false
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		for _, line := range strings.Split(string(raw), "\n") {
			const prefix = "Environment=ASSEMBLYAI_API_KEY="
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), prefix)) != ""
			}
		}
		return false
	case "darwin":
		path, err := macPlistPath()
		if err != nil {
			return false
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		m := apiKeyPlistRe.FindSubmatch(raw)
		if m == nil {
			return false
		}
		return strings.TrimSpace(string(m[2])) != ""
	default:
		return false
	}
}

// ---- paths ----

func linuxUnitDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func linuxUnitPath() (string, error) {
	dir, err := linuxUnitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "patro.service"), nil
}

func linuxOverridePath() (string, error) {
	dir, err := linuxUnitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "patro.service.d", "override.conf"), nil
}

func macPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.patro.plist"), nil
}

// ---- linux ----

// writeLinuxOverride writes the API-key drop-in with owner-only permissions.
func writeLinuxOverride(apiKey string) error {
	overridePath, err := linuxOverridePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(overridePath), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(overridePath), err)
	}
	content := "[Service]\nEnvironment=ASSEMBLYAI_API_KEY=" + apiKey + "\n"
	if err := os.WriteFile(overridePath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", overridePath, err)
	}
	// WriteFile does not chmod a file that already exists, so an override
	// created with looser permissions would silently keep them.
	if err := os.Chmod(overridePath, 0o600); err != nil {
		return fmt.Errorf("cannot chmod %s: %w", overridePath, err)
	}
	return nil
}

func setLinuxAPIKey(apiKey string) error {
	// Check the unit, not the drop-in: the drop-in is what we are about to write.
	unitPath, err := linuxUnitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(unitPath); err != nil {
		return ErrNoService
	}
	if err := writeLinuxOverride(apiKey); err != nil {
		return err
	}
	if err := runQuiet("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return runQuiet("systemctl", "--user", "restart", "patro")
}

// installLinuxService writes a systemd --user unit plus an API-key drop-in,
// then enables and starts it.
func installLinuxService(apiKey, configPath string) ([]string, bool) {
	var log []string

	exe, err := serviceExecutablePath()
	if err != nil {
		return append(log, fmt.Sprintf("Warning: cannot determine own executable path: %v", err)), false
	}
	unitDir, err := linuxUnitDir()
	if err != nil {
		return append(log, fmt.Sprintf("Warning: %v", err)), false
	}

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
		return append(log, fmt.Sprintf("Warning: cannot create %s: %v", unitDir, err)), false
	}
	unitPath := filepath.Join(unitDir, "patro.service")
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return append(log, fmt.Sprintf("Warning: cannot write %s: %v", unitPath, err)), false
	}
	log = append(log, fmt.Sprintf("Wrote %s", unitPath))

	// From here the unit exists, so the service counts as installed even if
	// the key or the start-up fails.
	if err := writeLinuxOverride(apiKey); err != nil {
		return append(log, fmt.Sprintf("Warning: %v", err)), true
	}
	overridePath, _ := linuxOverridePath()
	log = append(log, fmt.Sprintf("Wrote API key to %s", overridePath))

	if err := runQuiet("systemctl", "--user", "daemon-reload"); err != nil {
		log = append(log, "Warning: "+err.Error())
	}
	if err := runQuiet("systemctl", "--user", "enable", "--now", "patro"); err != nil {
		log = append(log, "Warning: "+err.Error())
	} else {
		log = append(log, "Service started and enabled")
	}
	return log, true
}

// ---- darwin ----

func setMacAPIKey(apiKey string) error {
	plistPath, err := macPlistPath()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(plistPath)
	if err != nil {
		return ErrNoService
	}
	if !apiKeyPlistRe.Match(raw) {
		return fmt.Errorf("%s has no ASSEMBLYAI_API_KEY entry; it was not written by patro init — re-run `patro init`", plistPath)
	}
	// Replace only the key's value so ProgramArguments keeps pointing at the
	// binary the service was installed with.
	updated := apiKeyPlistRe.ReplaceAll(raw, []byte("${1}"+xmlEscape(apiKey)+"${3}"))

	mode := os.FileMode(0o644)
	if info, err := os.Stat(plistPath); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(plistPath, updated, mode); err != nil {
		return fmt.Errorf("cannot write %s: %w", plistPath, err)
	}
	return reloadLaunchAgent(plistPath)
}

// reloadLaunchAgent bootstraps the agent, unloading any previous instance.
func reloadLaunchAgent(plistPath string) error {
	uid := os.Getuid()
	// Failure is expected when nothing is loaded, so this error is ignored.
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/com.patro", uid)).Run()
	return runQuiet("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath)
}

// installMacService writes a LaunchAgent plist (with the API key embedded)
// and bootstraps it.
func installMacService(apiKey, configPath string) ([]string, bool) {
	var log []string

	exe, err := serviceExecutablePath()
	if err != nil {
		return append(log, fmt.Sprintf("Warning: cannot determine own executable path: %v", err)), false
	}
	plistPath, err := macPlistPath()
	if err != nil {
		return append(log, fmt.Sprintf("Warning: %v", err)), false
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

	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return append(log, fmt.Sprintf("Warning: cannot create %s: %v", filepath.Dir(plistPath), err)), false
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return append(log, fmt.Sprintf("Warning: cannot write %s: %v", plistPath, err)), false
	}
	log = append(log, fmt.Sprintf("Wrote %s", plistPath))

	if err := reloadLaunchAgent(plistPath); err != nil {
		log = append(log, "Warning: "+err.Error())
	} else {
		log = append(log, "LaunchAgent started")
	}
	return log, true
}

// ---- shared helpers ----

// serviceExecutablePath returns the patro path a background service should
// launch. os.Executable resolves symlinks on some platforms, which under
// Homebrew yields a version-pinned Cellar path that goes stale on the next
// `brew upgrade`; prefer the stable opt/ symlink when it exists so installed
// services keep running the current binary.
func serviceExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if opt := cellarOptPath(exe); opt != "" {
		if info, err := os.Stat(opt); err == nil && !info.IsDir() {
			return opt, nil
		}
	}
	return exe, nil
}

// cellarOptPath maps a Homebrew Cellar path such as
// /opt/homebrew/Cellar/patro/0.2.0/bin/patro to its version-independent
// sibling /opt/homebrew/opt/patro/bin/patro. It returns "" when exe is not a
// versioned Cellar path.
func cellarOptPath(exe string) string {
	sep := string(filepath.Separator)
	parts := strings.Split(exe, sep)
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] != "Cellar" {
			continue
		}
		mapped := append([]string{}, parts[:i]...)
		mapped = append(mapped, "opt", parts[i+1]) // keep the formula, drop the version
		mapped = append(mapped, parts[i+3:]...)
		return strings.Join(mapped, sep)
	}
	return ""
}

// runQuiet runs an external command, capturing its output instead of
// streaming it. Streaming would corrupt a Bubble Tea alt screen.
func runQuiet(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return nil
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
