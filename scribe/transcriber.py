"""Upload and transcription via the AssemblyAI SDK.

The SDK accepts a local file path directly (it handles the upload). We poll
until the transcript reaches ``completed`` or ``error`` and normalize the
result into a TranscriptResult dataclass used by the rest of the pipeline.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from pathlib import Path

log = logging.getLogger(__name__)

POLL_INTERVAL_SECONDS = 3


@dataclass
class Chapter:
    headline: str
    gist: str
    start: int  # milliseconds
    end: int  # milliseconds


@dataclass
class Utterance:
    speaker: str
    text: str


@dataclass
class TranscriptResult:
    id: str
    text: str
    chapters: list[Chapter] = field(default_factory=list)
    utterances: list[Utterance] = field(default_factory=list)
    language: str = "en"
    # The underlying aai.Transcript object, kept for LeMUR calls (analyzer).
    # None in mock mode.
    sdk_transcript: object | None = field(default=None, repr=False)


def transcribe(video_path: Path, api_key: str) -> TranscriptResult:
    """Upload ``video_path`` to AssemblyAI and wait for the transcript."""
    import assemblyai as aai

    aai.settings.api_key = api_key
    aai.settings.polling_interval = POLL_INTERVAL_SECONDS

    config = aai.TranscriptionConfig(
        speaker_labels=True,
        auto_chapters=True,
        language_detection=True,
    )
    transcriber = aai.Transcriber(config=config)

    log.info("Uploading and transcribing %s ...", video_path.name)
    # transcriber.transcribe() blocks, polling internally, until the
    # transcript reaches a terminal status (completed or error).
    transcript = transcriber.transcribe(str(video_path))

    if transcript.status == aai.TranscriptStatus.error:
        raise RuntimeError(f"Transcription failed: {transcript.error}")

    chapters = [
        Chapter(
            headline=c.headline or "",
            gist=c.gist or "",
            start=int(c.start),
            end=int(c.end),
        )
        for c in (transcript.chapters or [])
    ]
    utterances = [
        Utterance(speaker=u.speaker or "?", text=u.text or "")
        for u in (transcript.utterances or [])
    ]

    result = TranscriptResult(
        id=transcript.id,
        text=transcript.text or "",
        chapters=chapters,
        utterances=utterances,
        language=getattr(transcript, "language_code", None) or "en",
        sdk_transcript=transcript,
    )
    log.info(
        "Transcription completed: id=%s, %d chars, %d chapters, %d utterances, lang=%s",
        result.id, len(result.text), len(result.chapters), len(result.utterances), result.language,
    )
    return result
