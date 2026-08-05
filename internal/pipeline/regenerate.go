package pipeline

import (
	"context"
	"fmt"
	"os"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/library"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/types"
)

// Regenerate re-runs the analyzer over an already-transcribed file and
// (re)writes exactly one meeting note, deliberately bypassing every
// topic/index/state side effect ProcessVideo performs (design invariants):
// no AddMeetingCtx, AppendTopicSectionAnnotated, RebuildIndex,
// ExistingTopics/ExistingTopicsRecent call ever happens, existing topic
// context passed to af is always literal nil, and — structurally, by taking
// no *state.State/*status.Tracker parameter — .state/processed.json and the
// live status tracker are never touched.
//
// It returns the path of the (over)written meeting note.
func Regenerate(ctx context.Context, transcriptPath, date string, cfg *config.Config, af AnalyzeFunc) (string, error) {
	lib, err := library.NewLibrary(cfg.Library)
	if err != nil {
		return "", err
	}

	id, external, err := lib.ResolveTranscriptID(transcriptPath)
	if err != nil {
		return "", err
	}

	prior, err := lib.FindMeetingNoteByTranscriptID(id)
	if err != nil {
		return "", err
	}

	resolvedDate, err := resolveRegenerateDate(id, prior, date)
	if err != nil {
		return "", err
	}

	raw, err := os.ReadFile(transcriptPath)
	if err != nil {
		return "", err
	}
	// A transcript resolved here is always bare: no chapters, no detected
	// language, no diarized utterances. That is accepted, documented
	// degradation (spec "Accepted metadata degradation for bare
	// transcripts") — WriteMeetingNote/WriteMeetingNoteAt already emit an
	// empty Language line and omit the Chapters section when absent.
	transcript := &types.TranscriptResult{ID: id, Text: string(raw)}

	// Copy placement (design D5): after date resolution (so a missing
	// --date fails before any write), before analysis (fail fast on an
	// unwritable transcripts/ dir before an expensive LLM call).
	if external {
		if _, err := lib.WriteTranscript(transcript); err != nil {
			return "", err
		}
	}

	logging.Infof("Regenerating meeting note for transcript %s ...", id)
	analysis, err := af(ctx, transcript, nil)
	if err != nil {
		return "", err
	}

	videoPath := transcriptPath
	if prior != nil && prior.SourceVideo != "" {
		videoPath = prior.SourceVideo
	}

	if prior != nil {
		return lib.WriteMeetingNoteAt(prior.Path, transcript, analysis, videoPath, resolvedDate)
	}
	return lib.WriteMeetingNote(transcript, analysis, videoPath, resolvedDate)
}

// resolveRegenerateDate implements design D2/D3's date resolution: a prior
// note's own date always wins (so overwriting one never changes it out from
// under the user); otherwise an explicit --date is required. A prior note
// found but with no parseable "- **Date:** " line is a distinct failure
// from "no prior note at all", each with its own actionable message.
func resolveRegenerateDate(id string, prior *library.PriorNote, date string) (string, error) {
	if prior != nil && prior.Date != "" {
		return prior.Date, nil
	}
	if date != "" {
		return date, nil
	}
	if prior != nil {
		return "", fmt.Errorf(
			"cannot determine date for transcript %q: prior note %s has no valid \"- **Date:**\" line; pass --date YYYY-MM-DD",
			id, prior.Path,
		)
	}
	return "", fmt.Errorf("no prior meeting note found for transcript %q; --date YYYY-MM-DD is required", id)
}
