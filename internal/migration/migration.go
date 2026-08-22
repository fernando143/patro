// Package migration plans and applies human-approved historical topic merges.
package migration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fernando143/patro/internal/embed"

	"github.com/fernando143/patro/internal/layout"
)

// ErrStalePlan means at least one accepted topic changed after preview.
var ErrStalePlan = errors.New("migration: plan is stale; refresh and review it again")

type DocumentRepresenter interface {
	Represent(context.Context, embed.Document) (*embed.Representation, error)
}

// Proposal is one independent source-to-target merge requiring approval.
type Proposal struct {
	SourceSlug     string
	SourceTitle    string
	SourcePath     string
	SourceHash     string
	SourceBytes    int
	SourceSections int
	TargetSlug     string
	TargetTitle    string
	TargetPath     string
	TargetHash     string
	TargetBytes    int
	TargetSections int
	Score          float64
}

// Plan is an immutable preview of all proposed historical merges.
type Plan struct {
	ID        string
	CreatedAt time.Time
	Proposals []Proposal
}

// Result reports the mutation and its rollback backup.
type Result struct {
	Merged        int
	Removed       int
	FilesAffected int
	BackupDir     string
}

// Service owns planning and safe application. RebuildDerived may be nil.
type Service struct {
	LibraryRoot    string
	StateDir       string
	Threshold      float64
	Representer    DocumentRepresenter
	RebuildDerived func(context.Context) error
	Now            func() time.Time
}

type topic struct {
	slug, title, path, hash string
	data                    []byte
	sections                int
	representation          *embed.Representation
}

type edge struct {
	a, b  int
	score float64
}

// BuildPlan represents every topic as a complete multi-vector document and
// greedily selects the strongest disjoint pairs. A slug can appear in only one
// proposal, preventing cycles and chains.
func (s *Service) BuildPlan(ctx context.Context) (Plan, error) {
	if s.Representer == nil {
		return Plan{}, errors.New("migration: multi-vector representation backend is unavailable")
	}
	files, err := filepath.Glob(filepath.Join(layout.Library(s.LibraryRoot).Topics(), "*.md"))
	if err != nil {
		return Plan{}, err
	}
	sort.Strings(files)
	topics := make([]topic, 0, len(files))
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return Plan{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return Plan{}, fmt.Errorf("migration: read %s: %w", path, err)
		}
		representation, err := s.Representer.Represent(ctx, embed.Document{ID: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Text: string(data)})
		if err != nil {
			return Plan{}, fmt.Errorf("migration: represent %s: %w", path, err)
		}
		if representation == nil {
			return Plan{}, fmt.Errorf("migration: represent %s: backend returned nil representation", path)
		}
		slug := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		topics = append(topics, topic{
			slug: slug, title: topicTitle(data, slug), path: path,
			hash: contentHash(data), data: data,
			sections: strings.Count(string(data), "\n## "), representation: representation,
		})
	}

	var edges []edge
	for i := range topics {
		for j := i + 1; j < len(topics); j++ {
			score, err := topicScore(ctx, topics[i], topics[j])
			if err != nil {
				return Plan{}, fmt.Errorf("migration: score %s/%s: %w", topics[i].slug, topics[j].slug, err)
			}
			if score >= s.Threshold {
				edges = append(edges, edge{i, j, score})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].score != edges[j].score {
			return edges[i].score > edges[j].score
		}
		ai := topics[edges[i].a].slug + "\x00" + topics[edges[i].b].slug
		aj := topics[edges[j].a].slug + "\x00" + topics[edges[j].b].slug
		return ai < aj
	})

	used := make(map[string]bool)
	plan := Plan{CreatedAt: s.now()}
	for _, e := range edges {
		a, b := topics[e.a], topics[e.b]
		if used[a.slug] || used[b.slug] {
			continue
		}
		source, target := a, b
		if betterTarget(source, target) {
			source, target = target, source
		}
		plan.Proposals = append(plan.Proposals, Proposal{
			SourceSlug: source.slug, SourceTitle: source.title, SourcePath: source.path,
			SourceHash: source.hash, SourceBytes: len(source.data), SourceSections: source.sections,
			TargetSlug: target.slug, TargetTitle: target.title, TargetPath: target.path,
			TargetHash: target.hash, TargetBytes: len(target.data), TargetSections: target.sections,
			Score: e.score,
		})
		used[source.slug], used[target.slug] = true, true
	}
	var identity strings.Builder
	for _, p := range plan.Proposals {
		fmt.Fprintf(&identity, "%s:%s:%s:%s;", p.SourceSlug, p.SourceHash, p.TargetSlug, p.TargetHash)
	}
	plan.ID = contentHash([]byte(identity.String()))
	return plan, nil
}

func topicScore(ctx context.Context, left, right topic) (float64, error) {
	return embed.SymmetricScore(ctx, *left.representation, *right.representation)
}

// Apply validates every accepted proposal before creating backups or writing.
// On any subsequent failure it restores all Markdown files from the backup.
func (s *Service) Apply(ctx context.Context, plan Plan, accepted []Proposal) (Result, error) {
	if len(accepted) == 0 {
		return Result{}, errors.New("migration: no accepted proposals to apply")
	}
	allowed := make(map[string]bool, len(plan.Proposals))
	for _, p := range plan.Proposals {
		allowed[proposalKey(p)] = true
	}
	seen := make(map[string]bool, len(accepted)*2)
	for _, p := range accepted {
		if !allowed[proposalKey(p)] {
			return Result{}, errors.New("migration: accepted proposal is not part of the reviewed plan")
		}
		if seen[p.SourceSlug] || seen[p.TargetSlug] {
			return Result{}, errors.New("migration: accepted proposals overlap")
		}
		seen[p.SourceSlug], seen[p.TargetSlug] = true, true
		if err := validateIdentity(p.SourcePath, p.SourceHash); err != nil {
			return Result{}, err
		}
		if err := validateIdentity(p.TargetPath, p.TargetHash); err != nil {
			return Result{}, err
		}
	}

	backupDir := filepath.Join(s.StateDir, "backups", "historical-topic-migration", s.now().UTC().Format("20060102T150405.000000000Z"))
	touched := make(map[string]bool)
	for _, p := range accepted {
		touched[p.SourcePath], touched[p.TargetPath] = true, true
	}
	indexPath := layout.Library(s.LibraryRoot).Index()
	touched[indexPath] = true
	if err := backupFiles(backupDir, s.LibraryRoot, touched); err != nil {
		return Result{}, fmt.Errorf("migration: create backup: %w", err)
	}

	rollback := func(cause error) (Result, error) {
		restoreErr := restoreFiles(backupDir, s.LibraryRoot, touched)
		if s.RebuildDerived != nil {
			if err := s.RebuildDerived(context.Background()); restoreErr == nil {
				restoreErr = err
			}
		}
		if restoreErr != nil {
			return Result{BackupDir: backupDir}, fmt.Errorf("migration: %v; rollback failed: %w (backup: %s)", cause, restoreErr, backupDir)
		}
		return Result{BackupDir: backupDir}, fmt.Errorf("migration: %w; changes rolled back from %s", cause, backupDir)
	}

	for _, p := range accepted {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		source, err := os.ReadFile(p.SourcePath)
		if err != nil {
			return rollback(err)
		}
		target, err := os.ReadFile(p.TargetPath)
		if err != nil {
			return rollback(err)
		}
		body := stripTitle(source)
		annotation := fmt.Sprintf("\n## %s - Historical migration from `%s`\n\n%s\n\n*Merged from historical topic `%s` - cosine %.4f*\n",
			s.now().UTC().Format("2006-01-02"), p.SourceSlug, strings.TrimSpace(string(body)), p.SourceSlug, p.Score)
		if err := writeAtomic(p.TargetPath, append(target, []byte(annotation)...)); err != nil {
			return rollback(err)
		}
		if err := os.Remove(p.SourcePath); err != nil {
			return rollback(err)
		}
	}

	if err := rebuildMarkdownIndex(s.LibraryRoot); err != nil {
		return rollback(err)
	}
	if s.RebuildDerived != nil {
		if err := s.RebuildDerived(ctx); err != nil {
			return rollback(fmt.Errorf("rebuild derived indexes: %w", err))
		}
	}
	return Result{Merged: len(accepted), Removed: len(accepted), FilesAffected: len(touched), BackupDir: backupDir}, nil
}

func proposalKey(p Proposal) string {
	return p.SourceSlug + "\x00" + p.SourceHash + "\x00" + p.TargetSlug + "\x00" + p.TargetHash
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func betterTarget(a, b topic) bool {
	if len(a.data) != len(b.data) {
		return len(a.data) > len(b.data)
	}
	return a.slug < b.slug
}

func contentHash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func topicTitle(data []byte, fallback string) string {
	line := string(data)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if strings.HasPrefix(line, "# ") {
		return strings.TrimSpace(line[2:])
	}
	return fallback
}

func stripTitle(data []byte) []byte {
	if i := strings.IndexByte(string(data), '\n'); i >= 0 && strings.HasPrefix(string(data[:i]), "# ") {
		return data[i+1:]
	}
	return data
}

func validateIdentity(path, want string) error {
	data, err := os.ReadFile(path)
	if err != nil || contentHash(data) != want {
		return fmt.Errorf("%w: %s changed since preview", ErrStalePlan, path)
	}
	return nil
}
