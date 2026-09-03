package migration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fernando143/patro/internal/adapter/embed"
)

type fakeRepresenter map[string][]float32

func (f fakeRepresenter) Represent(_ context.Context, doc embed.Document) (*embed.Representation, error) {
	vec := []float32{0, 1}
	for marker, candidate := range f {
		if strings.Contains(doc.Text, marker) {
			vec = candidate
			break
		}
	}
	return &embed.Representation{
		DocumentID: doc.ID,
		Chunks:     []embed.Chunk{{Kind: "content", Ordinal: 0, TokenCount: 1, Vector: vec}},
	}, nil
}

func fixtureService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "knowledge")
	if err := os.MkdirAll(filepath.Join(root, "topics"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "meetings"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(slug, title, marker, body string) {
		t.Helper()
		data := "# " + title + "\n\n## 2026-01-01\n\n" + marker + " " + body + "\n"
		if err := os.WriteFile(filepath.Join(root, "topics", slug+".md"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("alpha", "Alpha", "PAIR_A", "short")
	write("alpha-old", "Alpha Legacy", "PAIR_B", "a much longer canonical body")
	write("unrelated", "Unrelated", "OTHER", "different")
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("original index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	return &Service{
		LibraryRoot: root, StateDir: filepath.Join(dir, ".state"), Threshold: .9,
		Representer: fakeRepresenter{"PAIR_A": {1, 0}, "PAIR_B": {.99, .14106736}, "OTHER": {0, 1}},
		Now:         func() time.Time { return now },
	}, root
}

func TestBuildPlanIsDeterministicFilteredAndDisjoint(t *testing.T) {
	s, _ := fixtureService(t)
	before := treeSnapshot(t, s.LibraryRoot)
	first, err := s.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(first.Proposals) != 1 {
		t.Fatalf("plans differ or proposals = %d", len(first.Proposals))
	}
	p := first.Proposals[0]
	if p.SourceSlug != "alpha" || p.TargetSlug != "alpha-old" {
		t.Fatalf("proposal = %s -> %s", p.SourceSlug, p.TargetSlug)
	}
	if p.SourceHash == "" || p.TargetHash == "" || p.Score < .9 {
		t.Fatalf("proposal lacks review identity: %+v", p)
	}
	if strings.Contains(p.SourceSlug+p.TargetSlug, "unrelated") {
		t.Fatal("unrelated topic was proposed")
	}
	if after := treeSnapshot(t, s.LibraryRoot); before != after {
		t.Fatal("planning mutated the knowledge library")
	}
	if _, err := os.Stat(s.StateDir); !os.IsNotExist(err) {
		t.Fatal("planning created state")
	}
}

func TestBuildPlanUsesCompleteRepresentations(t *testing.T) {
	s, _ := fixtureService(t)
	s.Representer = fakeRepresenter{"PAIR_A": {1, 0}, "PAIR_B": {.99, .14106736}, "OTHER": {0, 1}}

	plan, err := s.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Proposals) != 1 || plan.Proposals[0].SourceSlug != "alpha" {
		t.Fatalf("plan = %+v, want one representation-scored proposal", plan)
	}
}

func TestBuildPlanPreventsChains(t *testing.T) {
	s, root := fixtureService(t)
	if err := os.WriteFile(filepath.Join(root, "topics", "third.md"), []byte("# Third\nPAIR_C"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Representer.(fakeRepresenter)["PAIR_C"] = []float32{.98, .02}
	plan, err := s.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range plan.Proposals {
		if seen[p.SourceSlug] || seen[p.TargetSlug] {
			t.Fatalf("overlapping proposal: %+v", p)
		}
		seen[p.SourceSlug], seen[p.TargetSlug] = true, true
	}
}

func TestApplySelectiveCreatesBackupAndRebuilds(t *testing.T) {
	s, root := fixtureService(t)
	plan, err := s.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := 0
	s.RebuildDerived = func(context.Context) error { rebuilt++; return nil }
	result, err := s.Apply(context.Background(), plan, plan.Proposals[:1])
	if err != nil {
		t.Fatal(err)
	}
	if result.Merged != 1 || rebuilt != 1 {
		t.Fatalf("result = %+v, rebuilt = %d", result, rebuilt)
	}
	if _, err := os.Stat(filepath.Join(root, "topics", "alpha.md")); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	target, _ := os.ReadFile(filepath.Join(root, "topics", "alpha-old.md"))
	if !strings.Contains(string(target), "Historical migration from `alpha`") {
		t.Fatalf("target lacks provenance: %s", target)
	}
	if _, err := os.Stat(filepath.Join(root, "topics", "unrelated.md")); err != nil {
		t.Fatal("unaccepted topic changed")
	}
	if _, err := os.Stat(filepath.Join(result.BackupDir, "topics", "alpha.md")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	backupIndex, err := os.ReadFile(filepath.Join(result.BackupDir, "index.md"))
	if err != nil || string(backupIndex) != "original index\n" {
		t.Fatalf("index backup = %q, err = %v", backupIndex, err)
	}
	index, _ := os.ReadFile(filepath.Join(root, "index.md"))
	if strings.Contains(string(index), "topics/alpha.md") {
		t.Fatal("index references removed source")
	}
}

func TestApplyLeavesRejectedProposalUntouched(t *testing.T) {
	s, root := fixtureService(t)
	write := func(slug, marker string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "topics", slug+".md"), []byte("# "+slug+"\n\n## 2026-01-01\n\n"+marker+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("second", "SECOND_A")
	write("second-old", "SECOND_B")
	representer := s.Representer.(fakeRepresenter)
	representer["SECOND_A"] = []float32{.7, .7}
	representer["SECOND_B"] = []float32{.71, .69}
	plan, err := s.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Proposals) != 2 {
		t.Fatalf("proposals = %d, want 2", len(plan.Proposals))
	}
	rejected := plan.Proposals[1]
	if _, err := s.Apply(context.Background(), plan, plan.Proposals[:1]); err != nil {
		t.Fatal(err)
	}
	if err := validateIdentity(rejected.SourcePath, rejected.SourceHash); err != nil {
		t.Fatal("rejected source changed:", err)
	}
	if err := validateIdentity(rejected.TargetPath, rejected.TargetHash); err != nil {
		t.Fatal("rejected target changed:", err)
	}
}

func TestApplyRejectsStalePlanBeforeBackup(t *testing.T) {
	s, _ := fixtureService(t)
	plan, err := s.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Proposals[0]
	if err := os.WriteFile(p.SourcePath, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Apply(context.Background(), plan, []Proposal{p})
	if !errors.Is(err, ErrStalePlan) {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(s.StateDir, "backups")); !os.IsNotExist(statErr) {
		t.Fatal("stale apply created a backup")
	}
}

func TestApplyRollsBackOnDerivedFailure(t *testing.T) {
	s, _ := fixtureService(t)
	plan, _ := s.BuildPlan(context.Background())
	calls := 0
	s.RebuildDerived = func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("index failure")
		}
		return nil
	}
	_, err := s.Apply(context.Background(), plan, plan.Proposals)
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("error = %v", err)
	}
	for _, p := range plan.Proposals {
		if err := validateIdentity(p.SourcePath, p.SourceHash); err != nil {
			t.Fatal(err)
		}
		if err := validateIdentity(p.TargetPath, p.TargetHash); err != nil {
			t.Fatal(err)
		}
	}
}

func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		b.WriteString(rel + "=" + contentHash(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
