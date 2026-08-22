package main

import (
	"fmt"
	"strings"

	"github.com/fernando143/patro/internal/analyzer/backend"
	"github.com/fernando143/patro/internal/setup"
)

// The wizard's backend choices are derived from the registry so they cannot
// fall behind what config accepts, which is exactly what happened while the
// list was written out by hand here.

// wizardBackendNames returns every selectable backend, in display order.
func wizardBackendNames() []string {
	return backend.Names()
}

// backendPromptLabel is the question shown on the no-TTY path, where the
// available choices reach the user only through the prompt text.
func backendPromptLabel() string {
	return fmt.Sprintf(
		"Which AI should write the knowledge library? (%s)",
		strings.Join(backend.Names(), "/"),
	)
}

// resolveWizardBinary locates the CLI for b. A hosted backend runs in the
// cloud, so it reports no binary and no failure: there is nothing to find.
func resolveWizardBinary(b backend.Backend) (string, bool) {
	if b.Hosted {
		return "", true
	}
	found, err := setup.ResolveBinary(b.DefaultBinary)
	if err != nil {
		return "", false
	}
	return found, true
}
