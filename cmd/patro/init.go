// Interactive setup wizard for patro (the init command).
//
// Guides the user from a fresh install to a running user-level service:
// API key, inbox and library folders, analyzer backend, config file, and
// an optional systemd/LaunchAgent background service.
//
// This is a port of scribe/install_wizard.py, minus the Python/venv/pip
// steps that no longer apply to the Go binary. Prompts use plain bufio +
// fmt; the config and service files themselves are written by
// internal/setup, which the settings TUI shares.
package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/fernando143/patro/internal/setup"
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

	configPath := setup.ConfigPath(flagConfig)
	if err := setup.WriteConfig(configPath, setup.Values{
		Inbox: inbox, Library: libraryDir, Backend: backend, BinaryPath: binaryPath,
	}); err != nil {
		fmt.Printf("Error: cannot write %s: %v\n", configPath, err)
		return 1
	}
	fmt.Printf("Updated %s\n", configPath)
	fmt.Println()

	serviceInstalled := false
	if p.confirm("Install and start the background service now?", true) {
		var log []string
		log, serviceInstalled = setup.InstallService(apiKey, configPath)
		for _, line := range log {
			fmt.Println(line)
		}
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
	fmt.Println("TUI: patro run tui   ·   Web viewer: patro run web")
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
		path := setup.ExpandPath(raw)
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
	if found, err := setup.ResolveBinary(name); err == nil {
		if p.confirm(fmt.Sprintf("Found '%s' at %s. Use it?", name, found), true) {
			return found
		}
	}
	for {
		raw := p.required(fmt.Sprintf("Enter the absolute path to the '%s' executable", name), "")
		path := setup.ExpandPath(raw)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
		fmt.Printf("'%s' is not an executable file. Please try again.\n", path)
	}
}
