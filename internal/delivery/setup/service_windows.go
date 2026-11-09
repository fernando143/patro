//go:build windows

package setup

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fernando143/patro/internal/platform/config"
)

// Windows has no user-level systemd unit or LaunchAgent, and the two obvious
// substitutes both break the project's "no administrator rights" promise:
//
//   - a real service needs the Service Control Protocol (a Go process without a
//     handler is killed by the SCM) and `sc create` needs elevation;
//   - `schtasks /sc onlogon` is refused with "access denied" for an
//     unprivileged user, because a logon trigger is a security boundary.
//
// What remains is the HKCU Run key: the per-user autostart list every Windows
// version honours, writable by the user it belongs to. It starts the watcher at
// logon with the user's own token. What it cannot do is supervise: there is no
// restart-on-failure, so a crashed watcher comes back at the next logon or when
// the settings screen restarts it.
const (
	windowsRunKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	windowsEnvKey = `HKCU\Environment`
	// DETACHED_PROCESS gives the watcher no console of its own and
	// CREATE_NEW_PROCESS_GROUP keeps Ctrl+C in the parent from reaching it.
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// installWindowsService registers the watcher in the per-user autostart list,
// stores the API key in the user environment, and starts it right away — the
// equivalent of `systemctl --user enable --now`.
func installWindowsService(apiKey, configPath string) ([]string, bool) {
	var log []string

	exe, err := serviceExecutablePath()
	if err != nil {
		return append(log, fmt.Sprintf("Warning: cannot determine own executable path: %v", err)), false
	}
	command := windowsRunCommand(exe, configPath)
	if err := regAdd(windowsRunKey, WindowsAutostartName, command); err != nil {
		return append(log, "Warning: "+err.Error()), false
	}
	log = append(log, fmt.Sprintf("Registered %q to start at logon (%s)", WindowsAutostartName, windowsRunKey))

	// From here the entry exists, so the service counts as installed even if
	// the key or the start-up fails — the same contract as the Linux unit.
	if err := writeWindowsUserEnv(config.APIKeyEnvVar, apiKey); err != nil {
		return append(log, "Warning: "+err.Error()), true
	}
	log = append(log, "Wrote "+config.APIKeyEnvVar+" to the user environment")

	if err := startWindowsWatcher(command); err != nil {
		log = append(log, "Warning: "+err.Error())
	} else {
		log = append(log, "Watcher started")
	}
	return log, true
}

// setWindowsAPIKey rewrites the stored key and restarts the watcher so the
// change takes effect: a running `serve` reads its key once at startup.
func setWindowsAPIKey(apiKey string) error {
	if !WindowsWatcherInstalled() {
		return ErrNoService
	}
	if err := writeWindowsUserEnv(config.APIKeyEnvVar, apiKey); err != nil {
		return err
	}
	return restartWindowsService()
}

// restartWindowsService cycles the watcher using the command stored in the
// autostart entry, which is why it needs no configuration path of its own.
func restartWindowsService() error {
	command, ok := windowsAutostartCommand()
	if !ok {
		return ErrNoService
	}
	stopWindowsWatcher(command)
	return startWindowsWatcher(command)
}

// WindowsWatcherInstalled reports whether the autostart entry exists.
func WindowsWatcherInstalled() bool {
	_, ok := windowsAutostartCommand()
	return ok
}

// WindowsWatcherRunning reports whether a watcher process is alive. It asks
// PowerShell rather than tasklist because Get-CimInstance's property names are
// not localized, while tasklist output is.
func WindowsWatcherRunning() bool {
	image := windowsWatcherImage()
	query := fmt.Sprintf(`@(Get-CimInstance Win32_Process -Filter "Name='%s'" | `+
		`Where-Object { $_.CommandLine -like '* serve*' }).Count`, image)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", query).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != "0"
}

// windowsServiceAPIKeyConfigured reports whether the user environment carries a
// non-empty key. Any read failure counts as "not configured", matching the
// other platforms' best-effort reads.
func windowsServiceAPIKeyConfigured() bool {
	out, err := exec.Command("reg", "query", windowsEnvKey, "/v", config.APIKeyEnvVar).Output()
	if err != nil {
		return false
	}
	return parseRegQueryValue(string(out), config.APIKeyEnvVar) != ""
}

// windowsAutostartCommand reads back the command the installer stored.
func windowsAutostartCommand() (string, bool) {
	out, err := exec.Command("reg", "query", windowsRunKey, "/v", WindowsAutostartName).Output()
	if err != nil {
		return "", false
	}
	value := parseRegQueryValue(string(out), WindowsAutostartName)
	return value, value != ""
}

// windowsWatcherImage is the image name to look for in the process list,
// derived from the executable the autostart entry points at so a renamed build
// is still found.
func windowsWatcherImage() string {
	command, ok := windowsAutostartCommand()
	if !ok {
		return sanitizeImageName("")
	}
	exe, _, ok := parseWindowsArgv(command)
	if !ok {
		return sanitizeImageName("")
	}
	return sanitizeImageName(filepath.Base(exe))
}

// startWindowsWatcher launches the watcher detached from the caller so it
// outlives the wizard or the settings screen that started it.
func startWindowsWatcher(command string) error {
	exe, args, ok := parseWindowsArgv(command)
	if !ok {
		return fmt.Errorf("cannot parse the stored autostart command %q; re-run `patro init`", command)
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNewProcessGroup,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start the watcher: %w", err)
	}
	// Nothing waits for it: the watcher must outlive this process.
	return cmd.Process.Release()
}

// stopWindowsWatcher ends any running watcher, matching on the command line so
// that a `patro process` or `patro run tui` invocation of the same executable
// is left alone.
func stopWindowsWatcher(command string) {
	image := sanitizeImageName("")
	if exe, _, ok := parseWindowsArgv(command); ok {
		image = sanitizeImageName(filepath.Base(exe))
	}
	query := fmt.Sprintf(`Get-CimInstance Win32_Process -Filter "Name='%s'" | `+
		`Where-Object { $_.CommandLine -like '* serve*' } | `+
		`ForEach-Object { Stop-Process -Id $_.ProcessId -Force }`, image)
	_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", query).Run()
}

// regAdd writes one REG_SZ value under key.
//
// It does not use runQuiet: that helper echoes the whole command line when a
// command fails, and the API key travels through `reg add`. A failure here must
// never print the secret into patro.log.
func regAdd(key, name, value string) error {
	out, err := exec.Command("reg", "add", key, "/v", name, "/t", "REG_SZ", "/d", value, "/f").CombinedOutput()
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("cannot write %s\\%s: %w: %s", key, name, err, msg)
	}
	return fmt.Errorf("cannot write %s\\%s: %w", key, name, err)
}

// writeWindowsUserEnv stores name=value in the per-user environment, which is
// the Windows spelling of `export` and the environment every later process
// inherits.
func writeWindowsUserEnv(name, value string) error {
	return regAdd(windowsEnvKey, name, value)
}
