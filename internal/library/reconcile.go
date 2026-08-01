// Reconciliation seam: deterministic, traceable merge-vs-new decisions at
// meeting ingestion, replacing AppendTopicSection's exact-slug-only
// matching (design D1/D2/D10, spec "Threshold-based reconciliation").
//
// Library.Reconciler is an optional interface (nil ⇒ today's exact-slug
// behavior, unchanged — design D1). SemanticReconciler is the default
// implementation: it embeds the candidate topic, queries a vector store for
// the closest existing topic, and applies a 3-band decision:
//
//	score >= MergeThreshold    -> auto-merge into the closest topic
//	score <  NewTopicThreshold -> new topic, LLM-proposed slug used as-is
//	otherwise (gray zone)      -> exactly one reconciliation LLM call
//
// Every path that could otherwise risk a wrong merge — a failed embed, a
// store error (including vectors.ErrRebuilding during a background
// rebuild), or a gray-zone LLM call that errors or times out — safe-fails
// toward a new, flagged topic instead (spec "Safe-fail toward new topic").
// Merges are never silent: every merge is recorded both as a markdown
// annotation (library.go's AppendTopicSectionAnnotated) and as an entry in
// the derived, deletable ledger at .state/reconciliation.json (design D4).
package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/types"
	"github.com/fernando143/patro/internal/vectors"
)

// Resolution is the outcome of reconciling one LLM-proposed candidate topic
// against the existing knowledge library.
type Resolution struct {
	// Slug/Name are what the caller should actually write to: the merged-
	// into existing topic's slug/name, or the candidate's own slug/name for
	// a new topic.
	Slug string
	Name string
	// Merged reports whether this candidate was merged into an existing
	// topic rather than becoming a new one.
	Merged bool
	// ProposedSlug is the original LLM-proposed slug, always populated —
	// equal to Slug except when Merged.
	ProposedSlug string
	// Score is the cosine similarity against the matched existing topic (0
	// when there was nothing to compare against, e.g. an empty library, or
	// a store/embed failure).
	Score float64
	// Flagged reports whether this candidate needs human reconciliation
	// review — set on any safe-fail path (gray-zone LLM error/timeout,
	// vectors.ErrRebuilding, embed failure). Flagged is never true together
	// with Merged.
	Flagged bool
}

// Reconciler decides, for one LLM-proposed candidate topic, whether it
// belongs in an existing topic (merge) or should become a new topic. A nil
// Reconciler on Library preserves today's exact-slug-only behavior (design
// D1) — no embedding or reconciliation call ever occurs in that case.
type Reconciler interface {
	Reconcile(ctx context.Context, candidate types.Topic, existing []types.TopicRef) (Resolution, error)
}

// Embedder is the narrow subset of internal/embed's Embedder this package
// needs. Declared locally (rather than importing internal/embed) so this
// package only depends on the shape it actually uses; any embed.Embedder
// implementation satisfies it structurally.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// NearestFinder is the narrow subset of *vectors.Store this package needs,
// narrowed for testability: fakes can synchronously return
// vectors.ErrRebuilding without spinning a real concurrent rebuild.
type NearestFinder interface {
	Nearest(vec []float32, k int) ([]vectors.Result, error)
}

// GrayZoneDecider answers "is candidate the same topic as nearest?" for a
// gray-zone score. SemanticReconciler calls it exactly once per Reconcile
// invocation, only inside the gray zone. Any error it returns (including a
// timeout) is treated as "no match, flag for review" by the caller — it is
// never treated as an implicit merge (spec "Safe-fail toward new topic").
type GrayZoneDecider func(ctx context.Context, candidate types.Topic, nearest types.TopicRef) (bool, error)

// SemanticReconciler is the default Reconciler: cosine similarity via an
// embedding backend + vector store, with a single gray-zone LLM call as a
// tie-breaker (design D1/D2/D7).
type SemanticReconciler struct {
	Embedder          Embedder
	Store             NearestFinder
	MergeThreshold    float64
	NewTopicThreshold float64
	// Decide resolves the gray zone. A nil Decide always safe-fails a
	// gray-zone candidate to a new, flagged topic (no LLM configured).
	Decide GrayZoneDecider
	// LedgerPath is .state/reconciliation.json. Empty disables ledger
	// writes (e.g. in tests that don't care about the audit trail).
	LedgerPath string
}

// Reconcile implements Reconciler. It never returns a non-nil error for the
// failure modes this package is designed to absorb (embed failure, store
// failure, gray-zone LLM failure) — those all safe-fail into a flagged
// Resolution instead, so a candidate topic is never lost.
func (r *SemanticReconciler) Reconcile(ctx context.Context, candidate types.Topic, existing []types.TopicRef) (Resolution, error) {
	proposed := Resolution{Slug: candidate.Slug, Name: candidate.Name, ProposedSlug: candidate.Slug}

	vec, err := r.Embedder.Embed(ctx, candidate.Name+"\n"+candidate.Content)
	if err != nil {
		res := r.failSafe(candidate, 0)
		r.writeLedger(res)
		return res, nil
	}

	results, err := r.Store.Nearest(vec, 1)
	if err != nil {
		// vectors.ErrRebuilding (or any other store error) must never risk
		// a wrong merge answered from a half-rebuilt/unavailable store —
		// safe-fail toward a new, flagged topic (spec "No lost meetings
		// during rebuild" / "Safe-fail toward new topic").
		res := r.failSafe(candidate, 0)
		r.writeLedger(res)
		return res, nil
	}
	if len(results) == 0 {
		return proposed, nil // nothing to compare against: new topic, unflagged
	}

	top := results[0]
	nearest := types.TopicRef{Slug: top.ID, Name: nameFor(existing, top.ID)}

	switch {
	case top.Score >= r.MergeThreshold:
		res := r.merged(candidate, nearest, top.Score)
		r.writeLedger(res)
		return res, nil

	case top.Score < r.NewTopicThreshold:
		proposed.Score = top.Score
		return proposed, nil

	default:
		// Gray zone: exactly one reconciliation LLM call.
		var same bool
		if r.Decide != nil {
			same, err = r.Decide(ctx, candidate, nearest)
		} else {
			err = errors.New("library: gray-zone score with no GrayZoneDecider configured")
		}
		if err != nil {
			res := r.failSafe(candidate, top.Score)
			r.writeLedger(res)
			return res, nil
		}
		if same {
			res := r.merged(candidate, nearest, top.Score)
			r.writeLedger(res)
			return res, nil
		}
		proposed.Score = top.Score
		return proposed, nil
	}
}

func (r *SemanticReconciler) merged(candidate types.Topic, nearest types.TopicRef, score float64) Resolution {
	return Resolution{
		Slug:         nearest.Slug,
		Name:         nearest.Name,
		Merged:       true,
		ProposedSlug: candidate.Slug,
		Score:        score,
	}
}

func (r *SemanticReconciler) failSafe(candidate types.Topic, score float64) Resolution {
	return Resolution{
		Slug:         candidate.Slug,
		Name:         candidate.Name,
		ProposedSlug: candidate.Slug,
		Score:        score,
		Flagged:      true,
	}
}

// nameFor looks up slug's display name among existing, falling back to the
// slug itself when not found (e.g. a fake/incomplete existing list in
// tests).
func nameFor(existing []types.TopicRef, slug string) string {
	for _, t := range existing {
		if t.Slug == slug {
			return t.Name
		}
	}
	return slug
}

// --- Audit trail: .state/reconciliation.json (design D4) ---

// LedgerEntry is one record in the derived, deletable reconciliation
// ledger. It mirrors the markdown annotation written alongside every merge,
// plus flagged safe-fail outcomes, so a later TUI can surface "work
// pending" (design D4/D8).
type LedgerEntry struct {
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	ProposedSlug string    `json:"proposed_slug"`
	Score        float64   `json:"score"`
	Merged       bool      `json:"merged"`
	Flagged      bool      `json:"flagged"`
	Timestamp    time.Time `json:"timestamp"`
}

type ledgerFile struct {
	Entries []LedgerEntry `json:"entries"`
}

// ledgerMu serializes ledger read-modify-write cycles across reconcilers
// sharing the same path within one process.
var ledgerMu sync.Mutex

// writeLedger appends one entry to r.LedgerPath, best-effort: a failure is
// logged, never propagated, since losing the audit trail must not lose the
// meeting itself.
func (r *SemanticReconciler) writeLedger(res Resolution) {
	if r.LedgerPath == "" || (!res.Merged && !res.Flagged) {
		return
	}
	entry := LedgerEntry{
		Slug:         res.Slug,
		Name:         res.Name,
		ProposedSlug: res.ProposedSlug,
		Score:        res.Score,
		Merged:       res.Merged,
		Flagged:      res.Flagged,
		Timestamp:    time.Now().UTC(),
	}
	if err := appendLedger(r.LedgerPath, entry); err != nil {
		logging.Warnf("reconciliation ledger write failed: %v", err)
	}
}

// appendLedger reads path (if present), appends entry, and writes the
// result back atomically (temp file + rename), mirroring internal/vectors'
// flush pattern.
func appendLedger(path string, entry LedgerEntry) error {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()

	var lf ledgerFile
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &lf) // best-effort; a corrupt ledger starts fresh
	} else if !os.IsNotExist(err) {
		return err
	}
	lf.Entries = append(lf.Entries, entry)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".reconciliation-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(lf); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// --- GrayZoneCLI: the concrete, subprocess-based gray-zone decider ---

// GrayZoneCLI builds a GrayZoneDecider that shells out to a local CLI
// backend (mirrors internal/analyzer's kimi/claude subprocess pattern) and
// asks it whether candidate is the same topic as nearest.
//
// Threat matrix (subprocess arg composition): binaryPath is invoked with an
// explicit argv slice via exec.CommandContext — never a shell string — so
// nothing in candidate's name/content can be interpreted as shell syntax.
// The call is bounded by timeout; both a non-zero exit and a timeout
// surface as a returned error, which the caller (SemanticReconciler.Decide)
// treats as "no match, flag for review", never an implicit merge.
func GrayZoneCLI(binaryPath string, timeout time.Duration) GrayZoneDecider {
	return func(ctx context.Context, candidate types.Topic, nearest types.TopicRef) (bool, error) {
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		prompt := fmt.Sprintf(
			"Existing topic: %q (slug %q).\n"+
				"Candidate topic: %q.\n%s\n"+
				"Is the candidate the SAME topic as the existing one? "+
				"Answer with exactly one word: yes or no.",
			nearest.Name, nearest.Slug, candidate.Name, strings.TrimSpace(candidate.Content),
		)

		cmd := exec.CommandContext(runCtx, binaryPath, "-p", prompt)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out

		err := cmd.Run()
		if runCtx.Err() == context.DeadlineExceeded {
			return false, fmt.Errorf("library: reconciliation LLM call timed out after %s", timeout)
		}
		if err != nil {
			return false, fmt.Errorf("library: reconciliation LLM call failed: %w", err)
		}

		answer := strings.ToLower(strings.TrimSpace(out.String()))
		return strings.HasPrefix(answer, "yes"), nil
	}
}
