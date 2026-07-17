"""Pipeline orchestration: video -> transcript -> analysis -> library.

Real work is delegated to two callables — a transcriber and an analyzer —
injected into ``process_video``. The ``--mock`` CLI flag swaps in the
deterministic fakes defined here instead of sprinkling conditionals through
the pipeline.
"""

from __future__ import annotations

import hashlib
import logging
from functools import partial
from pathlib import Path
from typing import Callable

from .analyzer import AnalysisResult, Topic
from .config import Config
from .library import Library
from .state import State
from .transcriber import Chapter, TranscriptResult, Utterance

log = logging.getLogger(__name__)

# Transcriber: (video_path, config) -> TranscriptResult
TranscriberFn = Callable[[Path, Config], TranscriptResult]
# Analyzer: (transcript, existing_topics) -> AnalysisResult
AnalyzerFn = Callable[[TranscriptResult, list[dict]], AnalysisResult]


# ------------------------------------------------------------------- real fns


def real_transcribe(video_path: Path, config: Config) -> TranscriptResult:
    from .transcriber import transcribe

    return transcribe(video_path, config.api_key)


def _lemur_analyze(transcript: TranscriptResult, existing_topics: list[dict]) -> AnalysisResult:
    from .analyzer import analyze

    return analyze(transcript, existing_topics, transcript.language)


def _kimi_analyze(transcript: TranscriptResult, existing_topics: list[dict], config: Config) -> AnalysisResult:
    from .kimi_analyzer import analyze as kimi_analyze

    return kimi_analyze(transcript, existing_topics, config)


def _claude_analyze(transcript: TranscriptResult, existing_topics: list[dict], config: Config) -> AnalysisResult:
    from .claude_analyzer import analyze as claude_analyze

    return claude_analyze(transcript, existing_topics, config)


def make_analyze_fn(config: Config) -> AnalyzerFn:
    """Return the real analyzer callable selected by ``config.analyzer_backend``."""
    if config.analyzer_backend == "kimi":
        return partial(_kimi_analyze, config=config)
    if config.analyzer_backend == "claude":
        return partial(_claude_analyze, config=config)
    return _lemur_analyze


# ------------------------------------------------------------------- mock fns


def mock_transcribe(video_path: Path, config: Config) -> TranscriptResult:
    """Deterministic fake transcript derived from the file name (no API calls)."""
    stem = video_path.stem
    digest = hashlib.sha1(video_path.name.encode("utf-8")).hexdigest()[:12]
    text = (
        f"This is a mock transcript for the recording '{stem}'. "
        "The team discussed the product roadmap and the budget review. "
        "No audio was actually processed; AssemblyAI was not called."
    )
    return TranscriptResult(
        id=f"mock-{digest}",
        text=text,
        chapters=[
            Chapter(headline="Roadmap discussion", gist="roadmap", start=0, end=90000),
            Chapter(headline="Budget review", gist="budget", start=90000, end=210000),
        ],
        utterances=[
            Utterance(speaker="A", text=f"Welcome to the mock meeting about {stem}."),
            Utterance(speaker="B", text="Let's review the roadmap and then the budget."),
            Utterance(speaker="A", text="Agreed, the roadmap comes first."),
        ],
        language="en",
    )


def mock_analyze(transcript: TranscriptResult, existing_topics: list[dict]) -> AnalysisResult:
    """Deterministic fake analysis with two sample topics (no API calls)."""
    return AnalysisResult(
        title=f"Mock analysis of {transcript.id}",
        summary=(
            "Mock summary: this analysis was generated locally without calling AssemblyAI. "
            "It stands in for the LeMUR output so the full pipeline can be verified offline."
        ),
        key_points=["The pipeline ran end to end in mock mode", "No API key was required"],
        decisions=["Ship the mock mode as the default verification path"],
        action_items=[
            {"owner": "unassigned", "task": "Set ASSEMBLYAI_API_KEY and run a real transcription"}
        ],
        topics=[
            Topic(
                slug="product-roadmap",
                name="Product roadmap",
                content=(
                    "- The roadmap was reviewed during this mock meeting.\n"
                    "- Priorities were reaffirmed for the next quarter."
                ),
            ),
            Topic(
                slug="budget-review",
                name="Budget review",
                content=(
                    "- The budget was reviewed with no major deviations.\n"
                    "- A follow-up review was scheduled."
                ),
            ),
        ],
    )


# ------------------------------------------------------------------ pipeline


def process_video(
    video_path: Path,
    config: Config,
    state: State,
    transcribe_fn: TranscriberFn = real_transcribe,
    analyze_fn: AnalyzerFn = _lemur_analyze,
) -> Path | None:
    """Process one video end to end. Returns the meeting note path, or None if skipped."""
    video_path = Path(video_path)
    if state.is_processed(video_path):
        log.info("Skipping %s (already processed)", video_path.name)
        return None

    log.info("Processing %s ...", video_path.name)
    library = Library(config.library)

    transcript = transcribe_fn(video_path, config)
    analysis = analyze_fn(transcript, library.existing_topics())
    note_path = library.add_meeting(transcript, analysis, video_path)

    state.mark_processed(video_path, transcript.id)
    log.info("Done: %s -> %s", video_path.name, note_path)
    return note_path
