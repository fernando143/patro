"""Command line interface.

Usage:
    python -m scribe install
    python -m scribe serve [--mock] [--config PATH]
    python -m scribe process <file> [--mock] [--config PATH]

``install`` runs the interactive setup wizard. ``serve`` watches the configured
inbox forever; ``process`` handles a single file. ``--mock`` skips all
AssemblyAI calls and uses deterministic fakes so the whole pipeline can be
verified without an API key.
"""

from __future__ import annotations

import argparse
import logging
import sys
from pathlib import Path

from .config import LOG_FILE, load_config
from .pipeline import make_analyze_fn, mock_analyze, mock_transcribe, process_video, real_transcribe
from .state import State


def _setup_logging() -> None:
    root = logging.getLogger()
    root.setLevel(logging.INFO)
    fmt = logging.Formatter("%(asctime)s %(levelname)s %(name)s: %(message)s")

    console = logging.StreamHandler(sys.stdout)
    console.setFormatter(fmt)
    root.addHandler(console)

    LOG_FILE.parent.mkdir(parents=True, exist_ok=True)
    file_handler = logging.FileHandler(LOG_FILE, encoding="utf-8")
    file_handler.setFormatter(fmt)
    root.addHandler(file_handler)


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="scribe",
        description="Watch an OBS recordings folder, transcribe with AssemblyAI "
                    "and build a Markdown knowledge library.",
    )
    parser.add_argument(
        "--config", type=Path, default=None,
        help="Path to config.yaml (default: the project's config.yaml)",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    for name, help_text in (("install", "Run the interactive installation wizard"),
                            ("serve", "Watch the inbox and process new recordings forever"),
                            ("process", "Process a single video file")):
        p = sub.add_parser(name, help=help_text)
        if name in ("serve", "process"):
            p.add_argument(
                "--mock", action="store_true",
                help="Do not call AssemblyAI; use deterministic fake transcripts/analysis",
            )
        if name == "process":
            p.add_argument("file", type=Path, help="Video file to process")

    return parser


def main(argv: list[str] | None = None) -> int:
    args = _build_parser().parse_args(argv)

    if args.command == "install":
        from .install_wizard import run
        return run()

    _setup_logging()
    log = logging.getLogger("scribe")

    config = load_config(args.config)
    state = State()

    if args.mock:
        transcribe_fn, analyze_fn = mock_transcribe, mock_analyze
        log.info("Mock mode: AssemblyAI will NOT be called")
    else:
        # Fail fast with a clear message when the key is missing.
        try:
            config.api_key
        except RuntimeError as exc:
            log.error("%s", exc)
            return 2
        transcribe_fn = real_transcribe
        analyze_fn = make_analyze_fn(config)

    if args.command == "process":
        video = args.file.expanduser().resolve()
        if not video.is_file():
            log.error("File not found: %s", video)
            return 1
        process_video(video, config, state, transcribe_fn, analyze_fn)
        return 0

    # serve
    from .watcher import Watcher

    def _process(path: Path) -> None:
        process_video(path, config, state, transcribe_fn, analyze_fn)

    Watcher(config, _process).run()
    return 0


if __name__ == "__main__":
    sys.exit(main())
