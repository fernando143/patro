"""Markdown knowledge library writer.

Layout under the configured library root::

    knowledge/
    ├── topics/<slug>.md                  # one file per topic, appended over time
    ├── meetings/<YYYY-MM-DD>-<slug>.md   # full note per processed meeting
    ├── transcripts/<transcript_id>.txt   # raw transcript with speakers
    └── index.md                          # regenerated on every run

Topic files grow by appending a dated section per meeting; meeting notes and
raw transcripts are written once; the index is rebuilt from scratch each run.
"""

from __future__ import annotations

import re
import unicodedata
from datetime import datetime, timezone
from pathlib import Path

from .analyzer import AnalysisResult, Topic
from .transcriber import TranscriptResult


def slugify(text: str) -> str:
    """Lowercase kebab-case slug, accents stripped (ASCII only)."""
    normalized = unicodedata.normalize("NFKD", text)
    ascii_text = normalized.encode("ascii", "ignore").decode("ascii")
    ascii_text = ascii_text.lower()
    ascii_text = re.sub(r"[^a-z0-9]+", "-", ascii_text)
    return ascii_text.strip("-") or "untitled"


def _fmt_timestamp(ms: int) -> str:
    total_seconds = int(ms) // 1000
    minutes, seconds = divmod(total_seconds, 60)
    hours, minutes = divmod(minutes, 60)
    if hours:
        return f"{hours}:{minutes:02d}:{seconds:02d}"
    return f"{minutes:02d}:{seconds:02d}"


class Library:
    def __init__(self, root: Path) -> None:
        self.root = Path(root)
        self.topics_dir = self.root / "topics"
        self.meetings_dir = self.root / "meetings"
        self.transcripts_dir = self.root / "transcripts"
        for d in (self.topics_dir, self.meetings_dir, self.transcripts_dir):
            d.mkdir(parents=True, exist_ok=True)

    # ------------------------------------------------------------------ read

    def existing_topics(self) -> list[dict]:
        """List of {"slug", "name"} for every topic file, used by the analyzer."""
        topics = []
        for path in sorted(self.topics_dir.glob("*.md")):
            name = path.stem
            try:
                first_line = path.read_text(encoding="utf-8").splitlines()[0]
                if first_line.startswith("# "):
                    name = first_line[2:].strip()
            except (OSError, IndexError):
                pass
            topics.append({"slug": path.stem, "name": name})
        return topics

    # ----------------------------------------------------------------- write

    def write_transcript(self, transcript: TranscriptResult) -> Path:
        """Raw transcript with speaker labels, one utterance per line."""
        path = self.transcripts_dir / f"{transcript.id}.txt"
        lines = []
        if transcript.utterances:
            lines = [f"Speaker {u.speaker}: {u.text}" for u in transcript.utterances]
        elif transcript.text:
            lines = [transcript.text]
        path.write_text("\n\n".join(lines) + "\n", encoding="utf-8")
        return path

    def write_meeting_note(
        self,
        transcript: TranscriptResult,
        analysis: AnalysisResult,
        video_path: Path,
        date: str,
    ) -> Path:
        """Full meeting note; returns the path of the created file."""
        meeting_slug = slugify(analysis.title)
        path = self.meetings_dir / f"{date}-{meeting_slug}.md"

        lines = [
            f"# {analysis.title}",
            "",
            f"- **Date:** {date}",
            f"- **Source video:** `{video_path.name}`",
            f"- **Language:** {transcript.language}",
            f"- **Raw transcript:** [transcript](../transcripts/{transcript.id}.txt)",
            "",
            "## Summary",
            "",
            analysis.summary or "(no summary)",
            "",
        ]

        if analysis.key_points:
            lines += ["## Key points", ""]
            lines += [f"- {point}" for point in analysis.key_points]
            lines += [""]

        if analysis.decisions:
            lines += ["## Decisions", ""]
            lines += [f"- {decision}" for decision in analysis.decisions]
            lines += [""]

        if analysis.action_items:
            lines += ["## Action items", ""]
            lines += [
                f"- [ ] **{item['owner']}**: {item['task']}" for item in analysis.action_items
            ]
            lines += [""]

        if transcript.chapters:
            lines += ["## Chapters", ""]
            for ch in transcript.chapters:
                start = _fmt_timestamp(ch.start)
                end = _fmt_timestamp(ch.end)
                label = ch.headline or ch.gist or "Chapter"
                lines.append(f"- `{start}–{end}` {label}")
            lines += [""]

        path.write_text("\n".join(lines), encoding="utf-8")
        return path

    def append_topic_section(
        self, topic: Topic, date: str, meeting_title: str, meeting_file: Path
    ) -> Path:
        """Append a dated section to a topic file, creating it if needed."""
        path = self.topics_dir / f"{topic.slug}.md"
        if not path.exists():
            path.write_text(f"# {topic.name}\n", encoding="utf-8")

        link = f"../meetings/{meeting_file.name}"
        section = (
            f"\n## {date} — {meeting_title}\n\n"
            f"{topic.content.strip()}\n\n"
            f"*Source: [{meeting_title}]({link})*\n"
        )
        with open(path, "a", encoding="utf-8") as fh:
            fh.write(section)
        return path

    def rebuild_index(self) -> Path:
        """Regenerate index.md: topics with last update + meetings, newest first."""
        topics = []
        for path in sorted(self.topics_dir.glob("*.md")):
            name = path.stem
            last_update = ""
            try:
                text = path.read_text(encoding="utf-8")
                first_line = text.splitlines()[0]
                if first_line.startswith("# "):
                    name = first_line[2:].strip()
                dates = re.findall(r"^## (\d{4}-\d{2}-\d{2})", text, re.MULTILINE)
                if dates:
                    last_update = max(dates)
            except (OSError, IndexError):
                pass
            topics.append((path.stem, name, last_update))

        meetings = sorted(self.meetings_dir.glob("*.md"), reverse=True)

        lines = ["# Knowledge library", ""]
        lines += ["## Topics", ""]
        if topics:
            for slug, name, last_update in topics:
                suffix = f" — last updated {last_update}" if last_update else ""
                lines.append(f"- [{name}](topics/{slug}.md){suffix}")
        else:
            lines.append("(no topics yet)")
        lines += ["", "## Meetings", ""]
        if meetings:
            for path in meetings:
                try:
                    first_line = path.read_text(encoding="utf-8").splitlines()[0]
                    title = first_line[2:].strip() if first_line.startswith("# ") else path.stem
                except (OSError, IndexError):
                    title = path.stem
                lines.append(f"- [{path.stem}](meetings/{path.name}) — {title}")
        else:
            lines.append("(no meetings yet)")
        lines.append("")

        index_path = self.root / "index.md"
        index_path.write_text("\n".join(lines), encoding="utf-8")
        return index_path

    # ------------------------------------------------------------- high level

    def add_meeting(
        self, transcript: TranscriptResult, analysis: AnalysisResult, video_path: Path
    ) -> Path:
        """Persist everything for one processed meeting; returns the note path."""
        date = datetime.now(timezone.utc).strftime("%Y-%m-%d")
        self.write_transcript(transcript)
        note_path = self.write_meeting_note(transcript, analysis, video_path, date)
        for topic in analysis.topics:
            self.append_topic_section(topic, date, analysis.title, note_path)
        self.rebuild_index()
        return note_path
