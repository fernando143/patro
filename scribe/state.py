"""Persistent record of processed videos.

State lives in ``.state/processed.json`` inside the project directory::

    {"<filename>": {"size": int, "transcript_id": str, "processed_at": iso}}

A video is considered processed when both the file name and its size match
the recorded entry (same name with different size means a new recording).
Writes are atomic: data goes to a temp file and is moved with os.replace.
"""

from __future__ import annotations

import json
import os
import tempfile
from datetime import datetime, timezone
from pathlib import Path

from .config import STATE_DIR


class State:
    def __init__(self, state_dir: Path | None = None) -> None:
        self.state_dir = Path(state_dir) if state_dir else STATE_DIR
        self.state_file = self.state_dir / "processed.json"
        self._data: dict[str, dict] = self._load()

    def _load(self) -> dict[str, dict]:
        if not self.state_file.exists():
            return {}
        try:
            with open(self.state_file, "r", encoding="utf-8") as fh:
                data = json.load(fh)
            return data if isinstance(data, dict) else {}
        except (json.JSONDecodeError, OSError):
            return {}

    def _save(self) -> None:
        self.state_dir.mkdir(parents=True, exist_ok=True)
        fd, tmp_name = tempfile.mkstemp(dir=self.state_dir, prefix=".processed-", suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as fh:
                json.dump(self._data, fh, indent=2, ensure_ascii=False)
                fh.write("\n")
            os.replace(tmp_name, self.state_file)
        except BaseException:
            try:
                os.unlink(tmp_name)
            except OSError:
                pass
            raise

    def is_processed(self, path: Path) -> bool:
        """True when an entry exists for this file name and the size matches."""
        entry = self._data.get(path.name)
        if entry is None:
            return False
        try:
            size = path.stat().st_size
        except OSError:
            return False
        return entry.get("size") == size

    def mark_processed(self, path: Path, transcript_id: str) -> None:
        self._data[path.name] = {
            "size": path.stat().st_size,
            "transcript_id": transcript_id,
            "processed_at": datetime.now(timezone.utc).isoformat(),
        }
        self._save()
