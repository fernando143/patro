// TUI setup wizard (huh) with an 80s-synthwave theme. runInit dispatches to
// the full-screen form on an interactive terminal and falls back to the
// line-based prompt wizard (runInitPrompt) otherwise.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/fernando143/patro/internal/delivery/setup"
	"github.com/fernando143/patro/internal/delivery/tui"

	"github.com/fernando143/patro/internal/adapter/analyzer/backend"
)

// runInit picks the wizard flavor based on whether we have a real terminal.
func runInit(flagConfig string) int {
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		return runInitTUI(flagConfig)
	}
	return runInitPrompt(flagConfig)
}

// runInitTUI drives the synthwave huh wizard.
// backendSelectOptions builds the wizard's backend menu from the registry.
func backendSelectOptions() []huh.Option[string] {
	all := backend.All()
	opts := make([]huh.Option[string], 0, len(all))
	for _, b := range all {
		opts = append(opts, huh.NewOption(b.Label, b.Name))
	}
	return opts
}

func runInitTUI(flagConfig string) int {
	apiKey := ""
	inbox := "~/Videos/obs"
	library := "./knowledge"
	selected := backend.Default
	installSvc := true

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("▓▒░ P A T R O ░▒▓").
				Description("Transcribe OBS recordings into a Markdown knowledge library.\nThis wizard writes your config and can install the background service."),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("AssemblyAI API key").
				Description("Used for transcription. Stored in the service env, never in config.yaml.").
				EchoMode(huh.EchoModePassword).
				Value(&apiKey).
				Validate(requiredValidate),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Recordings folder (inbox)").
				Description("Where patro watches for new recordings. Created if missing.").
				Value(&inbox).
				Validate(dirValidate),
			huh.NewInput().
				Title("Knowledge library folder").
				Description("Where the Markdown library is written. Created if missing.").
				Value(&library).
				Validate(dirValidate),
		),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("AI backend").
				Description("Which AI writes the notes.").
				Options(backendSelectOptions()...).
				Value(&selected),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Install and start the background service now?").
				Value(&installSvc),
		),
	).WithTheme(tui.SynthwaveHuhTheme())

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Println("Setup cancelled.")
			return 1
		}
		fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		return 1
	}

	inboxPath := setup.ExpandPath(inbox)
	libraryPath := setup.ExpandPath(library)
	for _, p := range []string{inboxPath, libraryPath} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "patro: cannot create %s: %v\n", p, err)
			return 1
		}
	}

	chosen, ok := backend.Get(selected)
	if !ok {
		fmt.Fprintf(os.Stderr, "patro: unknown analyzer backend %q\n", selected)
		return 1
	}
	binaryPath := ""
	if !chosen.Hosted {
		if binaryPath, ok = resolveBinaryTUI(chosen.DefaultBinary); !ok {
			return 1
		}
	}

	configPath := setup.ConfigPath(flagConfig)
	if err := setup.WriteConfig(configPath, setup.Values{
		Inbox: inboxPath, Library: libraryPath, Backend: chosen.Name, BinaryPath: binaryPath,
	}); err != nil {
		fmt.Printf("Error: cannot write %s: %v\n", configPath, err)
		return 1
	}
	fmt.Printf("Updated %s\n", configPath)

	serviceInstalled := false
	if installSvc {
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

// resolveBinaryTUI locates the backend CLI, prompting for an absolute path
// (via a small huh form) when it is not on PATH. The bool is false when the
// user aborts.
func resolveBinaryTUI(name string) (string, bool) {
	if found, err := setup.ResolveBinary(name); err == nil {
		return found, true
	}

	path := ""
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(fmt.Sprintf("Path to the '%s' executable", name)).
				Description(fmt.Sprintf("'%s' was not found on PATH. Enter its absolute path.", name)).
				Value(&path).
				Validate(executableValidate),
		),
	).WithTheme(tui.SynthwaveHuhTheme())
	if err := form.Run(); err != nil {
		if !errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintf(os.Stderr, "patro: %v\n", err)
		}
		return "", false
	}
	return setup.ExpandPath(path), true
}

// requiredValidate rejects empty input.
func requiredValidate(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("this field is required")
	}
	return nil
}

// dirValidate accepts a folder path that is missing (created later) or an
// existing directory, rejecting a path that exists as a non-directory.
func dirValidate(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("this field is required")
	}
	info, err := os.Stat(setup.ExpandPath(s))
	if err == nil && !info.IsDir() {
		return errors.New("path exists but is not a folder")
	}
	return nil
}

// executableValidate requires an existing executable file.
func executableValidate(s string) error {
	return setup.ValidateExecutable(s)
}
