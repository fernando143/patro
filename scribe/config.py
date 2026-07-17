"""Configuration loading for meeting-scribe.

Reads config.yaml from the project directory (the parent of the ``scribe``
package), expands ``~`` in paths and resolves relative paths against the
project directory. The AssemblyAI API key is read exclusively from the
``ASSEMBLYAI_API_KEY`` environment variable.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path

import yaml

PROJECT_DIR = Path(__file__).resolve().parent.parent
DEFAULT_CONFIG_PATH = PROJECT_DIR / "config.yaml"
STATE_DIR = PROJECT_DIR / ".state"
LOG_FILE = PROJECT_DIR / "scribe.log"

API_KEY_ENV_VAR = "ASSEMBLYAI_API_KEY"

_DEFAULTS = {
    "inbox": "~/Videos/obs",
    "library": "./knowledge",
    "video_extensions": [".mkv", ".mp4", ".mov", ".webm"],
    "stability_checks": 2,
    "stability_interval_seconds": 5,
    "analyzer_backend": "kimi",
    "kimi_path": "kimi",
    "claude_path": "claude",
}

VALID_ANALYZER_BACKENDS = ("kimi", "lemur", "claude")


@dataclass
class Config:
    """Runtime configuration with all paths resolved."""

    inbox: Path
    library: Path
    video_extensions: list[str] = field(default_factory=list)
    stability_checks: int = 2
    stability_interval_seconds: int = 5
    analyzer_backend: str = "kimi"
    kimi_path: str = "kimi"
    claude_path: str = "claude"
    project_dir: Path = PROJECT_DIR

    @property
    def api_key(self) -> str:
        """Return the API key from the environment or raise a clear error."""
        key = os.environ.get(API_KEY_ENV_VAR, "").strip()
        if not key:
            raise RuntimeError(
                f"{API_KEY_ENV_VAR} is not set. Export your AssemblyAI API key, "
                f"e.g.: export {API_KEY_ENV_VAR}=<your-key> "
                "(or use --mock to run without the API)."
            )
        return key

    def is_video(self, path: Path) -> bool:
        return path.suffix.lower() in self.video_extensions


def _resolve_path(value: str, base: Path) -> Path:
    p = Path(value).expanduser()
    if not p.is_absolute():
        p = base / p
    return p.resolve()


def load_config(path: Path | None = None) -> Config:
    """Load config.yaml, falling back to built-in defaults for missing keys."""
    config_path = Path(path) if path else DEFAULT_CONFIG_PATH
    raw: dict = {}
    if config_path.exists():
        with open(config_path, "r", encoding="utf-8") as fh:
            raw = yaml.safe_load(fh) or {}

    merged = {**_DEFAULTS, **{k: v for k, v in raw.items() if v is not None}}

    extensions = [ext if ext.startswith(".") else f".{ext}" for ext in merged["video_extensions"]]

    backend = str(merged["analyzer_backend"]).strip().lower()
    if backend not in VALID_ANALYZER_BACKENDS:
        raise ValueError(
            f"Invalid analyzer_backend {backend!r} in {config_path.name}; "
            f"valid values: {', '.join(VALID_ANALYZER_BACKENDS)}"
        )

    return Config(
        inbox=_resolve_path(str(merged["inbox"]), PROJECT_DIR),
        library=_resolve_path(str(merged["library"]), PROJECT_DIR),
        video_extensions=[ext.lower() for ext in extensions],
        stability_checks=int(merged["stability_checks"]),
        stability_interval_seconds=int(merged["stability_interval_seconds"]),
        analyzer_backend=backend,
        kimi_path=str(merged["kimi_path"]).strip() or "kimi",
        claude_path=str(merged["claude_path"]).strip() or "claude",
    )
