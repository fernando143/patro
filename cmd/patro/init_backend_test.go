package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/analyzer/backend"
	"github.com/fernando143/patro/internal/config"
)

// The setup wizard had silently fallen two backends behind: config accepted
// kimi, claude, codex and lemur, and the settings TUI offered all four, but
// a fresh `patro init` offered only kimi and claude. A user could not select
// codex or lemur until after finishing setup and reopening Settings.
//
// These tests assert the wizard is derived from the registry rather than
// from its own hand-written list.

func TestInitWizardOffersEveryConfiguredBackend(t *testing.T) {
	got := wizardBackendNames()
	want := config.ValidAnalyzerBackends()

	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("wizard offers %v, config accepts %v", got, want)
	}
}

// TestInitWizardPromptNamesEveryBackend covers the no-TTY path, where the
// choices only reach the user through the prompt text.
func TestInitWizardPromptNamesEveryBackend(t *testing.T) {
	prompt := backendPromptLabel()
	for _, b := range backend.All() {
		if !strings.Contains(prompt, b.Name) {
			t.Errorf("prompt %q does not offer %q", prompt, b.Name)
		}
	}
}

// TestInitWizardDefaultIsRegistered guards the value used when the user just
// presses enter.
func TestInitWizardDefaultIsRegistered(t *testing.T) {
	if !backend.IsValid(backend.Default) {
		t.Fatalf("wizard default %q is not a registered backend", backend.Default)
	}
}

// TestHostedBackendNeedsNoBinary pins the branch that must not ask a user to
// locate a CLI for a backend that runs in the cloud.
func TestHostedBackendNeedsNoBinary(t *testing.T) {
	b, ok := backend.Get("lemur")
	if !ok {
		t.Fatal("lemur is not registered")
	}
	if !b.Hosted {
		t.Fatal("lemur should be hosted")
	}
	if path, ok := resolveWizardBinary(b); ok && path != "" {
		t.Errorf("resolveWizardBinary(lemur) = %q, want no binary for a hosted backend", path)
	}
}
