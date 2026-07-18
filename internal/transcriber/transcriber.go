// Package transcriber uploads video files to AssemblyAI and waits for the
// resulting transcript. It is a port of scribe/transcriber.py using the
// official AssemblyAI Go SDK.
package transcriber

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AssemblyAI/assemblyai-go-sdk"

	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/types"
)

// Transcribe uploads videoPath to AssemblyAI and waits for the transcript.
//
// Speaker labels, auto chapters, and language detection are enabled, matching
// the Python pipeline. The call blocks until the transcript reaches a
// terminal status (completed or error) or ctx is cancelled.
func Transcribe(ctx context.Context, videoPath, apiKey string) (*types.TranscriptResult, error) {
	client := assemblyai.NewClient(apiKey)

	params := &assemblyai.TranscriptOptionalParams{
		SpeakerLabels:     assemblyai.Bool(true),
		AutoChapters:      assemblyai.Bool(true),
		LanguageDetection: assemblyai.Bool(true),
	}

	logging.Infof("Uploading and transcribing %s ...", filepath.Base(videoPath))

	file, err := os.Open(videoPath)
	if err != nil {
		return nil, fmt.Errorf("open video file: %w", err)
	}
	defer file.Close()

	submitted, err := client.Transcripts.SubmitFromReader(ctx, file, params)
	if err != nil {
		return nil, fmt.Errorf("submit transcription: %w", err)
	}

	// Wait polls with a 3s initial backoff interval, matching the Python
	// SDK's polling interval, and returns on completed or error status.
	transcript, err := client.Transcripts.Wait(ctx, derefStr(submitted.ID))
	if err != nil {
		return nil, fmt.Errorf("wait for transcript: %w", err)
	}

	if transcript.Status == assemblyai.TranscriptStatusError {
		return nil, fmt.Errorf("Transcription failed: %s", derefStr(transcript.Error))
	}

	chapters := make([]types.Chapter, 0, len(transcript.Chapters))
	for _, c := range transcript.Chapters {
		chapters = append(chapters, types.Chapter{
			Headline: derefStr(c.Headline),
			Gist:     derefStr(c.Gist),
			Start:    derefInt64(c.Start),
			End:      derefInt64(c.End),
		})
	}

	utterances := make([]types.Utterance, 0, len(transcript.Utterances))
	for _, u := range transcript.Utterances {
		speaker := derefStr(u.Speaker)
		if speaker == "" {
			speaker = "?"
		}
		utterances = append(utterances, types.Utterance{
			Speaker: speaker,
			Text:    derefStr(u.Text),
		})
	}

	language := string(transcript.LanguageCode)
	if language == "" {
		language = "en"
	}

	result := &types.TranscriptResult{
		ID:         derefStr(transcript.ID),
		Text:       derefStr(transcript.Text),
		Chapters:   chapters,
		Utterances: utterances,
		Language:   language,
	}
	logging.Infof("Transcription completed: id=%s, %d chars, %d chapters, %d utterances, lang=%s",
		result.ID, len(result.Text), len(result.Chapters), len(result.Utterances), result.Language)
	return result, nil
}

// derefStr returns the string pointed to by p, or "" when p is nil.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefInt64 returns the int64 pointed to by p, or 0 when p is nil.
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
