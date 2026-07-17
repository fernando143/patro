"""Meeting analysis through a local Claude Code CLI subprocess.

Mirrors ``kimi_analyzer.py``: shells out to ``claude -p`` (non-interactive,
auto-approved permissions) with ``--output-format stream-json``. The prompt
tells Claude to READ the transcript from a file on disk and answer with the
same strict JSON schema as the LeMUR backend.

Stdout is JSONL: assistant messages (text and/or tool calls), tool results
and meta lines. We concatenate the text of all assistant messages and feed
it to the same defensive parser every backend shares
(``analyzer.parse_analysis``).
"""

from __future__ import annotations

import logging
import subprocess
from pathlib import Path

from .analyzer import AnalysisResult, build_prompt, parse_analysis
from .config import PROJECT_DIR, Config
from .kimi_analyzer import _assistant_text, _write_transcript_file
from .transcriber import TranscriptResult

log = logging.getLogger(__name__)

CLAUDE_TIMEOUT_SECONDS = 600


def _run_claude(prompt: str, claude_path: str) -> str:
    """Run ``claude -p`` and return its stdout. Raises RuntimeError on failure."""
    cmd = [claude_path, "-p", prompt, "--output-format", "stream-json"]
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=CLAUDE_TIMEOUT_SECONDS,
            cwd=PROJECT_DIR,
        )
    except FileNotFoundError:
        raise RuntimeError(
            f"'{claude_path}' executable not found. Install Claude Code CLI, "
            "adjust claude_path in config.yaml, or switch analyzer_backend to kimi/lemur."
        ) from None
    except subprocess.TimeoutExpired:
        raise RuntimeError(
            f"claude did not finish within {CLAUDE_TIMEOUT_SECONDS}s; aborting analysis."
        ) from None

    if proc.returncode != 0:
        stderr = (proc.stderr or "").strip()[:1000]
        raise RuntimeError(f"claude exited with code {proc.returncode}: {stderr}")
    return proc.stdout


def analyze(
    transcript_result: TranscriptResult, existing_topics: list[dict], config: Config
) -> AnalysisResult:
    """Analyze a transcript with the local Claude Code CLI."""
    transcript_file = _write_transcript_file(transcript_result)
    try:
        prompt = build_prompt(
            existing_topics, transcript_result.language, transcript_path=str(transcript_file)
        )
        log.info(
            "Running Claude analysis over transcript %s (%s) ...",
            transcript_result.id, transcript_file,
        )
        stdout = _run_claude(prompt, config.claude_path)
        raw = _assistant_text(stdout)
        if not raw.strip():
            raise RuntimeError("claude produced no assistant text in its stream-json output")
        return parse_analysis(raw, backend_name="Claude")
    finally:
        transcript_file.unlink(missing_ok=True)
