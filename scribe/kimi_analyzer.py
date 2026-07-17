"""Meeting analysis through a local Kimi Code CLI subprocess.

Instead of calling a hosted LLM, this backend shells out to ``kimi -p``
(non-interactive, auto-approved permissions) with
``--output-format stream-json``. The prompt tells Kimi to READ the
transcript from a file on disk — embedding a long transcript in the
argument would risk ARG_MAX — and to answer with the same strict JSON
schema as the LeMUR backend (see ``analyzer.build_prompt``).

Stdout is JSONL: assistant messages (text and/or tool calls), tool results
and meta lines. We concatenate the text of all assistant messages and feed
it to the same defensive parser every backend shares
(``analyzer.parse_analysis``).

Errors are explicit: missing ``kimi`` binary, non-zero exit (with truncated
stderr) and timeout each raise a clear RuntimeError.
"""

from __future__ import annotations

import json
import logging
import subprocess
from pathlib import Path

from .analyzer import AnalysisResult, build_prompt, parse_analysis
from .config import PROJECT_DIR, Config
from .transcriber import TranscriptResult

log = logging.getLogger(__name__)

KIMI_TIMEOUT_SECONDS = 600
_TMP_DIR = PROJECT_DIR / ".state" / "tmp"


def _write_transcript_file(transcript: TranscriptResult) -> Path:
    """Write the transcript (with speaker labels) to .state/tmp/."""
    _TMP_DIR.mkdir(parents=True, exist_ok=True)
    path = _TMP_DIR / f"transcript-{transcript.id}.txt"
    if transcript.utterances:
        body = "\n\n".join(f"Speaker {u.speaker}: {u.text}" for u in transcript.utterances)
    else:
        body = transcript.text
    path.write_text(body + "\n", encoding="utf-8")
    return path


def _assistant_text(stream_json_stdout: str) -> str:
    """Concatenate the text of every assistant message in the JSONL stream."""
    chunks: list[str] = []
    for line in stream_json_stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(obj, dict) or obj.get("role") != "assistant":
            continue
        content = obj.get("content")
        if isinstance(content, str):
            chunks.append(content)
        elif isinstance(content, list):  # content blocks, e.g. [{"type": "text", ...}]
            chunks.extend(
                str(block["text"])
                for block in content
                if isinstance(block, dict) and block.get("type") == "text" and block.get("text")
            )
    return "\n".join(chunks)


def _run_kimi(prompt: str, kimi_path: str) -> str:
    """Run ``kimi -p`` and return its stdout. Raises RuntimeError on failure."""
    cmd = [kimi_path, "-p", prompt, "--output-format", "stream-json"]
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=KIMI_TIMEOUT_SECONDS,
            cwd=PROJECT_DIR,
        )
    except FileNotFoundError:
        raise RuntimeError(
            f"'{kimi_path}' executable not found. Install Kimi Code CLI "
            "(https://www.kimi.com/code), adjust kimi_path in config.yaml, "
            "or set analyzer_backend: lemur in config.yaml."
        ) from None
    except subprocess.TimeoutExpired:
        raise RuntimeError(
            f"kimi did not finish within {KIMI_TIMEOUT_SECONDS}s; aborting analysis."
        ) from None

    if proc.returncode != 0:
        stderr = (proc.stderr or "").strip()[:1000]
        raise RuntimeError(f"kimi exited with code {proc.returncode}: {stderr}")
    return proc.stdout


def analyze(
    transcript_result: TranscriptResult, existing_topics: list[dict], config: Config
) -> AnalysisResult:
    """Analyze a transcript with the local Kimi Code CLI."""
    transcript_file = _write_transcript_file(transcript_result)
    try:
        prompt = build_prompt(
            existing_topics, transcript_result.language, transcript_path=str(transcript_file)
        )
        log.info(
            "Running Kimi analysis over transcript %s (%s) ...",
            transcript_result.id, transcript_file,
        )
        stdout = _run_kimi(prompt, config.kimi_path)
        raw = _assistant_text(stdout)
        if not raw.strip():
            raise RuntimeError("kimi produced no assistant text in its stream-json output")
        return parse_analysis(raw, backend_name="Kimi")
    finally:
        transcript_file.unlink(missing_ok=True)
