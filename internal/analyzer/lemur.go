// LeMUR analyzer backend: hosted LLM analysis via the AssemblyAI API.
//
// LeMUR receives the transcript (referenced by its AssemblyAI transcript
// ID) plus the shared prompt (BuildPrompt) and answers with the same
// strict JSON schema every backend produces; the raw answer is parsed by
// ParseAnalysis.
//
// This is a port of scribe/analyzer.py's analyze().
package analyzer

import (
	"context"

	assemblyai "github.com/AssemblyAI/assemblyai-go-sdk"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/types"
)

// lemurFinalModel is the LeMUR model used for analysis. It matches the
// Python backend's aai.LemurModel.claude_sonnet_4_20250514; the Go SDK has
// no constant for it, so the ID is hardcoded here.
const lemurFinalModel = "anthropic/claude-sonnet-4-20250514"

// AnalyzeLeMUR runs a LeMUR task over the AssemblyAI transcript
// transcriptID and parses its JSON answer. apiKey is the AssemblyAI API
// key; existing and language shape the prompt as in BuildPrompt.
func AnalyzeLeMUR(ctx context.Context, transcriptID, apiKey string, existing []types.TopicRef, language string) (*types.AnalysisResult, error) {
	client := assemblyai.NewClient(apiKey)
	logging.Infof("Running LeMUR analysis over transcript %s ...", transcriptID)
	response, err := client.LeMUR.Task(ctx, assemblyai.LeMURTaskParams{
		Prompt: assemblyai.String(BuildPrompt(existing, language, "")),
		LeMURBaseParams: assemblyai.LeMURBaseParams{
			TranscriptIDs: []string{transcriptID},
			FinalModel:    assemblyai.LeMURModel(lemurFinalModel),
		},
	})
	if err != nil {
		return nil, err
	}
	return ParseAnalysis(assemblyai.ToString(response.Response), "LeMUR"), nil
}
