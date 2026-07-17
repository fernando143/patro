"""Meeting analysis with AssemblyAI LeMUR.

LeMUR receives the transcript plus a prompt asking for a strict JSON
payload: meeting metadata (title, summary, key points, decisions, action
items) and a list of knowledge topics with distilled Markdown content.

The prompt includes the topics already present in the library so the model
reuses existing slugs when the subject matches and only creates new slugs
for genuinely new subjects.

``build_prompt`` and ``parse_analysis`` are shared with the Kimi backend
(``kimi_analyzer.py``), which produces the same JSON contract through a
local Kimi Code CLI subprocess instead of LeMUR.

Parsing is defensive: the JSON block is extracted even when wrapped in
prose or ``` fences; on total failure we fall back to a minimal result with
a single "general" topic holding the summary.
"""

from __future__ import annotations

import json
import logging
import re
from dataclasses import dataclass, field

log = logging.getLogger(__name__)

_JSON_SCHEMA_EXAMPLE = """{
  "meeting": {
    "title": "short descriptive title",
    "summary": "2-4 sentence summary of the meeting",
    "key_points": ["point 1", "point 2"],
    "decisions": ["decision 1"],
    "action_items": [{"owner": "person name or 'unassigned'", "task": "what must be done"}]
  },
  "topics": [
    {
      "slug": "kebab-case-slug",
      "name": "Human readable name",
      "content": "Markdown with what was newly learned about this topic in this meeting"
    }
  ]
}"""


@dataclass
class Topic:
    slug: str
    name: str
    content: str


@dataclass
class AnalysisResult:
    title: str
    summary: str
    key_points: list[str] = field(default_factory=list)
    decisions: list[str] = field(default_factory=list)
    action_items: list[dict] = field(default_factory=list)  # [{"owner": ..., "task": ...}]
    topics: list[Topic] = field(default_factory=list)


def build_prompt(
    existing_topics: list[dict], language: str, transcript_path: str | None = None
) -> str:
    """Build the analyzer prompt. ``existing_topics`` is a list of {slug, name}.

    When ``transcript_path`` is given, the prompt instructs the model to read
    the transcript from that file (used by the Kimi backend, where embedding
    the whole transcript in the prompt would risk ARG_MAX on long meetings).
    """
    if existing_topics:
        topic_lines = "\n".join(f"- {t['slug']}: {t['name']}" for t in existing_topics)
        topics_block = (
            "The knowledge library already contains these topics (slug: name):\n"
            f"{topic_lines}\n"
            "REUSE an existing slug whenever the subject of a topic matches. "
            "Only create a new slug for genuinely new subjects."
        )
    else:
        topics_block = (
            "The knowledge library is empty yet. Create slugs for every subject discussed."
        )

    if transcript_path:
        source_block = (
            f"First, read the meeting transcript from this file using your file reading tool: "
            f"{transcript_path}\n"
            "Base your entire analysis on its contents."
        )
    else:
        source_block = "Analyze the meeting transcript provided with this request."

    language_rule = (
        f'Write ALL output (title, summary, key points, decisions, action items, topic names '
        f'and content) in the same language as the transcript ("{language}").'
        if language and language != "unknown"
        else "Write ALL output (title, summary, key points, decisions, action items, topic "
        "names and content) in the same language the transcript is written in."
    )

    return f"""You are analyzing a meeting transcript to distill it into a personal knowledge library.

{source_block}

{topics_block}

Respond with ONLY a valid JSON object (no prose, no markdown fences) matching exactly this schema:
{_JSON_SCHEMA_EXAMPLE}

Rules:
- Slugs must be lowercase kebab-case, ASCII, no accents.
- "content" of each topic is Markdown: what was newly learned about that topic in THIS meeting (facts, decisions, context), not a meeting summary.
- 1 to 5 topics, each focused on a single subject.
- {language_rule}
- If a list would be empty, use an empty array.
- Your final message must contain ONLY the JSON object, nothing else.
"""


def _extract_json(text: str) -> dict:
    """Extract the first JSON object from ``text``, tolerating fences/prose."""
    cleaned = text.strip()
    # Strip a wrapping ```json ... ``` fence if present.
    fence = re.search(r"```(?:json)?\s*(.*?)```", cleaned, re.DOTALL)
    if fence:
        cleaned = fence.group(1).strip()
    try:
        return json.loads(cleaned)
    except json.JSONDecodeError:
        pass
    # Fall back to the first balanced {...} block in the text.
    start = cleaned.find("{")
    end = cleaned.rfind("}")
    if start != -1 and end > start:
        return json.loads(cleaned[start : end + 1])
    raise ValueError("no JSON object found in analyzer response")


def _as_str_list(value) -> list[str]:
    if not isinstance(value, list):
        return []
    return [str(item) for item in value]


def _parse_response(payload: dict) -> AnalysisResult:
    meeting = payload.get("meeting")
    if not isinstance(meeting, dict):
        raise ValueError("missing 'meeting' object in LeMUR response")

    action_items = []
    for item in meeting.get("action_items") or []:
        if isinstance(item, dict):
            action_items.append(
                {"owner": str(item.get("owner", "unassigned")), "task": str(item.get("task", ""))}
            )
        elif item:
            action_items.append({"owner": "unassigned", "task": str(item)})

    topics = []
    for t in payload.get("topics") or []:
        if not isinstance(t, dict) or not t.get("slug"):
            continue
        topics.append(
            Topic(
                slug=str(t["slug"]),
                name=str(t.get("name") or t["slug"]),
                content=str(t.get("content") or ""),
            )
        )

    return AnalysisResult(
        title=str(meeting.get("title") or "Untitled meeting"),
        summary=str(meeting.get("summary") or ""),
        key_points=_as_str_list(meeting.get("key_points")),
        decisions=_as_str_list(meeting.get("decisions")),
        action_items=action_items,
        topics=topics,
    )


def parse_analysis(raw: str, backend_name: str = "analyzer") -> AnalysisResult:
    """Parse a raw analyzer text response into an AnalysisResult.

    Shared by every backend. Never raises on malformed input: falls back to a
    minimal result with a single "general" topic holding the raw response.
    """
    try:
        return _parse_response(_extract_json(raw))
    except (ValueError, KeyError, TypeError) as exc:
        log.warning(
            "%s response could not be parsed (%s); using minimal fallback",
            backend_name, exc,
        )
        summary = raw.strip()[:2000] or "Analysis failed to produce a parseable result."
        return AnalysisResult(
            title="Untitled meeting",
            summary=summary,
            topics=[Topic(slug="general", name="General", content=summary)],
        )


def analyze(transcript_result, existing_topics: list[dict], language: str) -> AnalysisResult:
    """Run a LeMUR task over a TranscriptResult and parse its JSON answer."""
    import assemblyai as aai

    sdk_transcript = transcript_result.sdk_transcript
    if sdk_transcript is None:
        raise RuntimeError("analyze() requires the SDK transcript object (missing in mock mode)")

    prompt = build_prompt(existing_topics, language)
    log.info("Running LeMUR analysis over transcript %s ...", transcript_result.id)
    response = sdk_transcript.lemur.task(prompt, final_model=aai.LemurModel.claude_sonnet_4_20250514)
    return parse_analysis(response.response or "", backend_name="LeMUR")
