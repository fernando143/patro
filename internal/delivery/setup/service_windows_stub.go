//go:build !windows

package setup

// These stubs exist for one reason: the runtime.GOOS switch in service.go stays
// a single readable table, so every branch's function has to exist in every
// build. On a non-Windows build the "windows" case can never be selected, so
// none of these bodies is ever executed.

func installWindowsService(apiKey, configPath string) ([]string, bool) { return nil, false }

func setWindowsAPIKey(apiKey string) error { return ErrNoService }

func restartWindowsService() error { return ErrNoService }

func windowsServiceAPIKeyConfigured() bool { return false }

// WindowsWatcherInstalled and WindowsWatcherRunning are exported for the
// dashboard, which asks every platform the same two questions.
func WindowsWatcherInstalled() bool { return false }

func WindowsWatcherRunning() bool { return false }
