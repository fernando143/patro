# Agent Guide for meeting-scribe

This file is written for AI coding agents working on the `meeting-scribe` project. It describes the project's purpose, technology stack, code organization, runtime behavior, development conventions, and deployment process. Read this before making changes.

## Project overview

`meeting-scribe` is a local Python service that watches a folder for new video recordings (typically OBS Studio output), uploads them to AssemblyAI for transcription, analyzes the transcript with an LLM, and appends the results to a Markdown knowledge library organized by topic.

The analyzer backend is pluggable:

- **Kimi (default)** — runs the already-installed local `kimi -p` CLI.
- **LeMUR** — uses AssemblyAI's hosted LLM.

The project is intentionally small and self-contained. It is packaged as an editable `setuptools` project and run as a Python module.

## Technology stack

- **Language**: Python 3.11+ (the environment currently runs Python 3.14)
- **Build backend**: `setuptools` declared in `pyproject.toml`
- **Dependencies** (production only):
  - `watchdog` — filesystem watcher for the `serve` command
  - `assemblyai` — transcription and LeMUR analysis
  - `pyyaml` — configuration parsing
- **No test framework is currently installed or configured.**
- **No linting/formatting tools are configured** (no `ruff`, `black`, `mypy`, `pytest`, etc. in `pyproject.toml`).

## Project structure

```
meeting-scribe/
├── pyproject.toml              # setuptools project metadata and deps
├── config.yaml                 # user-facing configuration
├── README.md                   # human-readable setup and usage
├── scribe.log                  # runtime log (created by the app)
├── scribe/                     # main Python package
│   ├── __init__.py
│   ├── __main__.py             # CLI entry point: serve, process
│   ├── config.py               # Config dataclass + load_config()
│   ├── pipeline.py             # orchestration: process_video(), mock fns
│   ├── watcher.py              # watchdog-based inbox watcher
│   ├── transcriber.py          # AssemblyAI transcription
│   ├── analyzer.py             # AssemblyAI LeMUR analysis + shared parsing
│   ├── kimi_analyzer.py        # Kimi Code CLI analyzer backend
│   ├── library.py              # Markdown knowledge library writer
│   └── state.py                # persistent processed-files state
├── install/                    # service install helpers
│   ├── install.sh              # user-level systemd/LaunchAgent installer
│   ├── macos/com.scribe.plist
│   └── systemd/scribe.service
├── knowledge/                  # generated knowledge library
│   ├── index.md
│   ├── topics/<slug>.md
│   ├── meetings/<YYYY-MM-DD>-<slug>.md
│   └── transcripts/<id>.txt
└── .state/
    └── processed.json          # deduplication state
```

## Configuration

Configuration is read from `config.yaml` by default (override with `--config PATH`). `Config` in `scribe/config.py` expands `~` and resolves relative paths against the project directory.

Important keys:

- `inbox` — watched folder for new recordings (default `~/Videos/obs`)
- `library` — knowledge library root (default `./knowledge`)
- `video_extensions` — list of extensions that trigger processing
- `stability_checks` / `stability_interval_seconds` — how long to wait for OBS to finish writing a file
- `analyzer_backend` — `"kimi"` or `"lemur"`
- `kimi_path` — path to the `kimi` binary (default `"kimi"`)

The AssemblyAI API key is **not** stored in `config.yaml`; it is read from the environment variable `ASSEMBLYAI_API_KEY`.

## Runtime architecture

The application has two CLI commands:

```bash
python -m scribe serve [--mock] [--config PATH]
python -m scribe process <file> [--mock] [--config PATH]
```

### `serve`

- Starts a `watchdog` observer on `config.inbox`.
- New or moved video files are submitted to a stability checker that waits until the file size is unchanged across `stability_checks` probes.
- Stable files are queued and processed sequentially by a daemon worker thread.
- On startup, existing unprocessed videos in the inbox are scanned and enqueued.
- Logs to `scribe.log` and stdout.

### `process`

- Processes a single file end to end and exits.
- Skips the file if it is already recorded in `.state/processed.json` with the same size.

### Pipeline (`process_video`)

1. Transcribe the video using the injected transcriber (`real_transcribe` or `mock_transcribe`).
2. Analyze the transcript using the injected analyzer (`make_analyze_fn(config)` or `mock_analyze`).
3. Write the results via `Library`:
   - raw transcript under `knowledge/transcripts/<id>.txt`
   - meeting note under `knowledge/meetings/<YYYY-MM-DD>-<slug>.md`
   - topic updates under `knowledge/topics/<slug>.md`
   - regenerated `knowledge/index.md`
4. Record the file in `.state/processed.json` so it is not reprocessed.

### Analyzer backends

Both backends use the same prompt schema and the shared parser in `analyzer.py`:

- `analyzer.build_prompt()` builds the prompt, optionally pointing to a transcript file on disk.
- `analyzer.parse_analysis()` extracts a JSON object from the model response and falls back to a minimal `general` topic if parsing fails.
- `kimi_analyzer.py` writes the transcript to `.state/tmp/transcript-<id>.txt`, shells out to `kimi -p --output-format stream-json`, concatenates assistant text from the JSONL stream, and parses it.
- `analyzer.analyze()` calls LeMUR via the AssemblyAI SDK transcript object.

## Code style and conventions

- Use `from __future__ import annotations` at the top of every module.
- Follow PEP 8. The existing code uses double quotes for strings and Google-style docstrings.
- Prefer type hints on function signatures and module-level callables.
- Keep the analyzer JSON contract stable; both backends depend on it.
- Keep `process_video` injectable: it receives `transcribe_fn` and `analyze_fn` so `--mock` does not require conditional logic inside the pipeline.
- Do not store secrets in `config.yaml`; use environment variables.
- When writing files, use `encoding="utf-8"` and create parent directories with `mkdir(parents=True, exist_ok=True)`.

## Build and run commands

Create and activate a virtual environment, then install in editable mode:

```bash
cd /home/fernando/projects/meeting-scribe
python3 -m venv .venv
.venv/bin/pip install -e .
```

Set the API key for real runs:

```bash
export ASSEMBLYAI_API_KEY=<your-key>
```

Run the watcher:

```bash
.venv/bin/python -m scribe serve
```

Process one recording:

```bash
.venv/bin/python -m scribe process ~/Videos/obs/meeting.mkv
```

Run without any API calls (deterministic fake pipeline):

```bash
.venv/bin/python -m scribe process --mock <file>
.venv/bin/python -m scribe serve --mock
```

## Testing instructions

There is no automated test suite. Use `--mock` mode as the manual smoke test:

1. Create or copy a small video file into the configured inbox.
2. Run `.venv/bin/python -m scribe process --mock <path>`.
3. Verify that `knowledge/meetings/`, `knowledge/topics/`, and `knowledge/index.md` are updated.

If you add automated tests, prefer `pytest` and place them under a `tests/` directory. Update this section.

## Deployment

The project is designed to run as a **user-level background service** with no sudo required.

Run the installer:

```bash
install/install.sh
```

- **Linux**: installs `~/.config/systemd/user/scribe.service`, enables and starts it. Set the API key with `systemctl --user edit scribe` and add `[Service] Environment=ASSEMBLYAI_API_KEY=...`. View logs with `journalctl --user -u scribe -f`.
- **macOS**: installs `~/Library/LaunchAgents/com.scribe.plist` and bootstraps it. Add an `EnvironmentVariables` dict containing `ASSEMBLYAI_API_KEY` to the plist. Logs go to `/tmp/scribe.out.log` and `/tmp/scribe.err.log`.

The service runs `.venv/bin/python -m scribe serve` from the project directory and restarts on failure.

## Security considerations

- The AssemblyAI API key is read only from the `ASSEMBLYAI_API_KEY` environment variable. Do not commit it.
- The Kimi backend shells out to the binary configured in `kimi_path` with `-p` (non-interactive, auto-approved permissions). Ensure the binary path is trustworthy.
- Service templates do not contain the API key; it must be supplied after installation.
- The application writes files under `knowledge/` and `.state/` within the project directory. It does not require elevated privileges.
