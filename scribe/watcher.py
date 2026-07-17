"""Filesystem watcher built on watchdog.

OBS writes recordings progressively, so a file is only enqueued once its
size stays identical across ``stability_checks`` consecutive probes spaced
``stability_interval_seconds`` apart. Events land in a queue consumed by a
daemon worker thread that runs the pipeline sequentially. On startup the
inbox is scanned for existing, not-yet-processed videos, which are enqueued
too.
"""

from __future__ import annotations

import logging
import queue
import threading
import time
from pathlib import Path
from typing import Callable

from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer

from .config import Config

log = logging.getLogger(__name__)


class _RecordingHandler(FileSystemEventHandler):
    """Forwards created/moved video files to the stability-checking enqueue callback."""

    def __init__(self, config: Config, on_candidate: Callable[[Path], None]) -> None:
        super().__init__()
        self._config = config
        self._on_candidate = on_candidate

    def _maybe_enqueue(self, path_str: str) -> None:
        path = Path(path_str)
        if self._config.is_video(path):
            self._on_candidate(path)

    def on_created(self, event) -> None:
        if not event.is_directory:
            self._maybe_enqueue(event.src_path)

    def on_moved(self, event) -> None:
        if not event.is_directory:
            self._maybe_enqueue(event.dest_path)


def wait_until_stable(path: Path, checks: int, interval: float) -> bool:
    """True when the file size is identical across ``checks`` probes.

    ``checks`` identical consecutive reads (each ``interval`` seconds after
    the previous one) mean OBS finished writing. Returns False if the file
    disappears mid-wait.
    """
    try:
        last_size = path.stat().st_size
    except OSError:
        return False
    stable = 0
    while stable < checks:
        time.sleep(interval)
        try:
            size = path.stat().st_size
        except OSError:
            return False
        if size == last_size:
            stable += 1
        else:
            stable = 0
            last_size = size
    return True


class Watcher:
    def __init__(self, config: Config, process_fn: Callable[[Path], None]) -> None:
        self._config = config
        self._process_fn = process_fn
        self._queue: queue.Queue[Path] = queue.Queue()
        self._pending: set[str] = set()
        self._pending_lock = threading.Lock()
        self._observer = Observer()

    # ------------------------------------------------------------- enqueueing

    def _submit(self, path: Path) -> None:
        """Enqueue a candidate after its size stabilizes (deduplicated)."""
        with self._pending_lock:
            if str(path) in self._pending:
                return
            self._pending.add(str(path))
        try:
            log.info("New recording detected: %s (waiting for it to finish writing)", path.name)
            if wait_until_stable(path, self._config.stability_checks,
                                 self._config.stability_interval_seconds):
                log.info("File stable, enqueueing: %s", path.name)
                self._queue.put(path)
            else:
                log.warning("File vanished before stabilizing: %s", path.name)
        finally:
            with self._pending_lock:
                self._pending.discard(str(path))

    def submit_async(self, path: Path) -> None:
        """Stability check runs in its own thread so watchdog events never block."""
        threading.Thread(target=self._submit, args=(path,), daemon=True).start()

    def scan_existing(self) -> None:
        """Enqueue unprocessed videos already sitting in the inbox."""
        if not self._config.inbox.is_dir():
            return
        for path in sorted(self._config.inbox.iterdir()):
            if path.is_file() and self._config.is_video(path):
                self.submit_async(path)

    # ---------------------------------------------------------------- workers

    def _worker(self) -> None:
        while True:
            path = self._queue.get()
            try:
                self._process_fn(path)
            except Exception:
                log.exception("Failed to process %s", path)
            finally:
                self._queue.task_done()

    # ------------------------------------------------------------------- run

    def run(self) -> None:
        self._config.inbox.mkdir(parents=True, exist_ok=True)
        handler = _RecordingHandler(self._config, self.submit_async)
        self._observer.schedule(handler, str(self._config.inbox), recursive=False)

        threading.Thread(target=self._worker, daemon=True).start()

        log.info("Watching %s for %s files ...",
                 self._config.inbox, ", ".join(self._config.video_extensions))
        self.scan_existing()

        self._observer.start()
        try:
            while True:
                time.sleep(1)
        except KeyboardInterrupt:
            log.info("Shutting down ...")
        finally:
            self._observer.stop()
            self._observer.join()
