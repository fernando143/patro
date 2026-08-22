package migration

import (
	"path/filepath"
	"testing"

	"github.com/fernando143/patro/internal/adapter/embed"
	"github.com/fernando143/patro/internal/platform/config"
)

// ConfiguredService had no test at all before this one, and it held one of
// the five hand-rolled copies of the representation-store bootstrap. These
// tests pin what it wires so the extraction of that bootstrap cannot change
// the service's shape unnoticed.

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		Library:          filepath.Join(dir, "knowledge"),
		Dir:              dir,
		EmbeddingBackend: "cybertron",
		MergeThreshold:   0.87,
	}
}

// TestConfiguredServiceWiresConfigValues pins the plain fields taken
// straight from config.
func TestConfiguredServiceWiresConfigValues(t *testing.T) {
	cfg := testConfig(t)

	s, err := ConfiguredService(cfg)
	if err != nil {
		t.Skipf("embedding backend unavailable in this environment: %v", err)
	}

	if s.LibraryRoot != cfg.Library {
		t.Errorf("LibraryRoot = %q, want %q", s.LibraryRoot, cfg.Library)
	}
	if s.StateDir != cfg.StateDir() {
		t.Errorf("StateDir = %q, want %q", s.StateDir, cfg.StateDir())
	}
	if s.Threshold != cfg.MergeThreshold {
		t.Errorf("Threshold = %v, want %v", s.Threshold, cfg.MergeThreshold)
	}
	if s.Representer == nil {
		t.Error("Representer = nil, want the configured embedding backend")
	}
	if s.RebuildDerived == nil {
		t.Error("RebuildDerived = nil, want a rebuild hook")
	}
}

// TestConfiguredServiceRejectsUnknownBackend pins the abort policy: unlike
// the pipeline, which degrades so a missing store never blocks a recording,
// migration refuses to build at all.
func TestConfiguredServiceRejectsUnknownBackend(t *testing.T) {
	cfg := testConfig(t)
	cfg.EmbeddingBackend = "no-such-backend"

	s, err := ConfiguredService(cfg)
	if err == nil {
		t.Fatal("ConfiguredService error = nil, want an error for an unknown backend")
	}
	if s != nil {
		t.Errorf("service = %v, want nil on failure", s)
	}
}

// TestConfiguredServiceRepresenterSatisfiesPort guards the interface the
// service actually consumes, independent of the concrete backend type.
func TestConfiguredServiceRepresenterSatisfiesPort(t *testing.T) {
	cfg := testConfig(t)

	s, err := ConfiguredService(cfg)
	if err != nil {
		t.Skipf("embedding backend unavailable in this environment: %v", err)
	}

	var _ DocumentRepresenter = s.Representer
	if _, ok := s.Representer.(embed.Embedder); !ok {
		t.Error("Representer does not satisfy embed.Embedder")
	}
}
