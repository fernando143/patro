// Reconciliation seam: deterministic, traceable merge-vs-new decisions at
// meeting ingestion, replacing AppendTopicSection's exact-slug-only
// matching (design D1/D2/D10, spec "Threshold-based reconciliation").
//
// Library.Reconciler is an optional interface (nil ⇒ today's exact-slug
// behavior, unchanged — design D1). SemanticReconciler is the default
// implementation: it represents the candidate topic as multiple vectors,
// queries a representation store for the closest existing topic, and applies
// a 3-band decision:
//
//	score >= MergeThreshold    -> auto-merge into the closest topic
//	score <  NewTopicThreshold -> new topic, LLM-proposed slug used as-is
//	otherwise (gray zone)      -> exactly one reconciliation LLM call
//
// Every path that could otherwise risk a wrong merge — a failed
// representation, a store error (including a v2 rebuild in progress during a
// background rebuild), or a gray-zone LLM call that errors or times out — safe-fails
// toward a new, flagged topic instead (spec "Safe-fail toward new topic").
// Merges are never silent: every merge is recorded both as a markdown
// annotation (library.go's AppendTopicSectionAnnotated) and as an entry in
// the derived, deletable ledger at .state/reconciliation.json (design D4).
package knowledge

import (
	"context"
	"errors"
	"time"

	"github.com/fernando143/patro/internal/domain/meeting"
	"github.com/fernando143/patro/internal/platform/logging"

	"github.com/fernando143/patro/internal/adapter/ledger"
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
	// a representation/store failure).
	Score float64
	// Flagged reports whether this candidate needs human reconciliation
	// review — set on any safe-fail path (gray-zone LLM error/timeout,
	// representation store rebuild/error). Flagged is never true together
	// with Merged.
	Flagged bool
}

// Reconciler decides, for one LLM-proposed candidate topic, whether it
// belongs in an existing topic (merge) or should become a new topic. A nil
// Reconciler on Library preserves today's exact-slug-only behavior (design
// D1) — no embedding or reconciliation call ever occurs in that case.
type Reconciler interface {
	Reconcile(ctx context.Context, candidate meeting.Topic, existing []meeting.TopicRef) (Resolution, error)
}

// NearestTopic is the existing topic closest to a candidate, with the
// similarity that ranked it. An empty Slug means there was nothing to
// compare against — an empty library, or a store with no entries yet.
type NearestTopic struct {
	Slug  string
	Score float64
}

// TopicSimilarity answers "which existing topic is this candidate most
// like, and how much?".
//
// The domain states the question in its own vocabulary — topics and scores.
// How an implementation answers it (embeddings, multi-vector chunking,
// cosine similarity, some future index) is deliberately not visible from
// here: this package decides what a topic *is*, and must stay readable
// without knowing what a chunk vector is.
type TopicSimilarity interface {
	Nearest(ctx context.Context, candidate meeting.Topic) (NearestTopic, error)
}

// GrayZoneDecider answers "is candidate the same topic as nearest?" for a
// gray-zone score. SemanticReconciler calls it exactly once per Reconcile
// invocation, only inside the gray zone. Any error it returns (including a
// timeout) is treated as "no match, flag for review" by the caller — it is
// never treated as an implicit merge (spec "Safe-fail toward new topic").
type GrayZoneDecider func(ctx context.Context, candidate meeting.Topic, nearest meeting.TopicRef) (bool, error)

// SemanticReconciler is the default Reconciler: complete document
// representations via a multi-vector backend + representation store, with a
// single gray-zone LLM call as a tie-breaker (design D1/D2/D7).
type SemanticReconciler struct {
	Similarity        TopicSimilarity
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
// failure modes this package is designed to absorb (representation failure,
// store failure, gray-zone LLM failure) — those all safe-fail into a flagged
// Resolution instead, so a candidate topic is never lost.
func (r *SemanticReconciler) Reconcile(ctx context.Context, candidate meeting.Topic, existing []meeting.TopicRef) (Resolution, error) {
	proposed := Resolution{Slug: candidate.Slug, Name: candidate.Name, ProposedSlug: candidate.Slug}

	if r.Similarity == nil {
		res := r.failSafe(candidate, 0)
		r.writeLedger(res)
		return res, nil
	}

	nearestTopic, err := r.Similarity.Nearest(ctx, candidate)
	if err != nil {
		// An unavailable or rebuilding representation store must never risk a
		// wrong merge answered from a half-rebuilt/inconsistent snapshot —
		// safe-fail toward a new, flagged topic (spec "No lost meetings
		// during rebuild" / "Safe-fail toward new topic").
		res := r.failSafe(candidate, 0)
		r.writeLedger(res)
		return res, nil
	}
	if nearestTopic.Slug == "" {
		return proposed, nil // nothing to compare against: new topic, unflagged
	}

	topScore := nearestTopic.Score
	nearest := meeting.TopicRef{Slug: nearestTopic.Slug, Name: nameFor(existing, nearestTopic.Slug)}

	switch {
	case topScore >= r.MergeThreshold:
		res := r.merged(candidate, nearest, topScore)
		r.writeLedger(res)
		return res, nil

	case topScore < r.NewTopicThreshold:
		proposed.Score = topScore
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
			res := r.failSafe(candidate, topScore)
			r.writeLedger(res)
			return res, nil
		}
		if same {
			res := r.merged(candidate, nearest, topScore)
			r.writeLedger(res)
			return res, nil
		}
		proposed.Score = topScore
		return proposed, nil
	}
}

func (r *SemanticReconciler) merged(candidate meeting.Topic, nearest meeting.TopicRef, score float64) Resolution {
	return Resolution{
		Slug:         nearest.Slug,
		Name:         nearest.Name,
		Merged:       true,
		ProposedSlug: candidate.Slug,
		Score:        score,
	}
}

func (r *SemanticReconciler) failSafe(candidate meeting.Topic, score float64) Resolution {
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
func nameFor(existing []meeting.TopicRef, slug string) string {
	for _, t := range existing {
		if t.Slug == slug {
			return t.Name
		}
	}
	return slug
}

// --- GrayZoneCLI: the concrete, subprocess-based gray-zone decider ---

func (r *SemanticReconciler) writeLedger(res Resolution) {
	if r.LedgerPath == "" || (!res.Merged && !res.Flagged) {
		return
	}
	entry := ledger.Entry{
		Slug:         res.Slug,
		Name:         res.Name,
		ProposedSlug: res.ProposedSlug,
		Score:        res.Score,
		Merged:       res.Merged,
		Flagged:      res.Flagged,
		Timestamp:    time.Now().UTC(),
	}
	if err := ledger.Append(r.LedgerPath, entry); err != nil {
		logging.Warnf("reconciliation ledger write failed: %v", err)
	}
}
