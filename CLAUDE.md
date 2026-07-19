# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

`patro` is a local Go service that watches a folder for new video recordings (typically OBS Studio output), uploads them to AssemblyAI for transcription, analyzes the transcript with an LLM, and appends the results to a Markdown knowledge library organized by topic.

The analyzer backend is pluggable:

- **Kimi (default)** — runs the already-installed local `kimi -p` CLI.
- **Claude** — runs the local `claude -p` CLI.
- **LeMUR** — uses AssemblyAI's hosted LLM.

The project is intentionally small and self-contained: a single static binary built from the Go module `github.com/fernando143/patro`, distributed via Homebrew from the GitHub repo `fernando143/patro`.

## Technology stack

- **Language**: Go 1.26+
- **Dependencies** (production only): `fsnotify` (inbox watcher), `gopkg.in/yaml.v3` (config parsing), `assemblyai-go-sdk` (transcription + LeMUR), `golang.org/x/text`, `yuin/goldmark` (web viewer Markdown rendering), and the Charm stack — `bubbletea` + `lipgloss` + `bubbles` (dashboard TUI) and `huh` (setup wizard TUI) — plus `golang.org/x/term`.
- **Command/flag parsing** is hand-rolled in `cmd/patro/main.go` (`parseArgs`), not a CLI framework. The Charm stack is used **only** for the interactive TUIs (`run dashboard`, `init`); the core pipeline stays dependency-light.
- **Tests**: stdlib `testing`, table-driven style. No other test or lint tooling beyond `gofmt` and `go vet`.

## Build, test, and run commands

```bash
go build ./...
go vet ./...
go test ./...

# Run a single package's tests
go test ./internal/analyzer/...

# Run a single test by name
go test ./internal/analyzer/ -run TestParseResponse -v

# Build the binary
go build -o patro ./cmd/patro

# Build a local release snapshot (requires GoReleaser)
goreleaser release --snapshot --clean
```

Manual smoke test (no API calls, no API key needed):

```bash
export ASSEMBLYAI_API_KEY=<your-key>   # only needed for real runs
./patro process --mock /path/to/any-video.mkv
./patro serve --mock
```

Verify `knowledge/meetings/`, `knowledge/topics/`, and `knowledge/index.md` are updated after a mock run.

## Project structure

```
patro/
├── cmd/patro/          # main package: CLI entry point + init wizard
├── internal/
│   ├── types/          # shared data types
│   ├── config/         # Config struct + config loading/search
│   ├── state/          # persistent processed-files state
│   ├── logging/        # shared logger
│   ├── library/        # Markdown knowledge library writer
│   ├── analyzer/       # prompt + shared parser; cli.go (kimi/claude subprocess); lemur.go
│   ├── transcriber/     # AssemblyAI transcription (assemblyai-go-sdk)
│   ├── watcher/        # fsnotify-based inbox watcher
│   ├── web/            # on-demand local web viewer (goldmark) for the library
│   ├── status/         # live processing state published to .state/status.json
│   ├── tui/            # synthwave TUIs: dashboard (bubbletea) + wizard theme (huh)
│   └── pipeline/       # orchestration: ProcessVideo + mocks
├── knowledge/           # generated knowledge library (index.md, topics/, meetings/, transcripts/)
└── .state/processed.json  # deduplication state
```

## Configuration

Configuration is YAML. Search order:

1. `--config PATH` flag
2. `./config.yaml` (current directory)
3. `~/.config/patro/config.yaml`

The loader expands `~` and resolves everything relative — `inbox`, `library`, `.state/`, `patro.log` — against the directory containing the config file.

Important keys:

- `inbox` — watched folder for new recordings
- `library` — knowledge library root
- `video_extensions` — list of extensions that trigger processing
- `stability_checks` / `stability_interval_seconds` — how long to wait for OBS to finish writing a file (serve waits until file size is unchanged across this many probes)
- `analyzer_backend` — `"kimi"` (default), `"claude"`, or `"lemur"`
- `kimi_path` / `claude_path` — path to the respective CLI binary

The AssemblyAI API key is **never** stored in `config.yaml`; it is read only from the `ASSEMBLYAI_API_KEY` environment variable.

## Runtime architecture

Commands: `patro serve [--mock] [--config PATH]`, `patro process <file> [--mock] [--config PATH]`, `patro init [--config PATH]`, `patro run web [--port N] [--config PATH]`, `patro run dashboard [--config PATH]`, plus `patro --version`.

- **`serve`**: starts an fsnotify watcher on `config.inbox`; stable files are queued and processed sequentially; on startup, existing unprocessed videos in the inbox are scanned and enqueued; runs until SIGINT/SIGTERM; logs to `patro.log` and stdout.
- **`process`**: processes a single file end to end and exits; skips the file if already recorded in `.state/processed.json` with the same name and size.
- **`init`**: interactive setup wizard — asks for the AssemblyAI API key, inbox, library, and analyzer backend, locates the CLI binary, writes the config, and optionally installs/starts a user-level background service (systemd user unit on Linux with the key in `override.conf`, LaunchAgent plist on macOS). On an interactive terminal it runs a synthwave `huh` TUI (`runInitTUI`); with no TTY (piped/non-interactive) it falls back to the line-based prompt wizard (`runInitPrompt`).
- **`run web`**: on-demand, foreground web viewer for the knowledge library (`internal/web`). Renders `.md` to HTML with goldmark, shows `.txt` transcripts as escaped preformatted text, serves everything else raw. Read-only, no external assets/JS, binds to `127.0.0.1:<port>` (default 8765), and shuts down gracefully on Ctrl+C. Not a background service — meant to be started when needed and stopped with Ctrl+C.
- **`run dashboard`**: on-demand, foreground synthwave status TUI (`internal/tui`, Bubble Tea + Lipgloss). Reads `.state/status.json`, `.state/processed.json`, the service state and the `patro.log` tail on a 1s tick; shows counters (processing/stage, queue, processed total+session, failed), the in-flight job, config + alerts, recent meetings, a focusable failures list, and a colorized follow/scroll log. Keys: `q` quit, `tab` focus, `↑/↓` move, `enter` retry selected failure (spawns `patro process`), `f` follow, `r` refresh, `o`/`w` launch the web viewer. Requires an interactive terminal.

### Live status (`internal/status`)

`serve` owns a `*status.Tracker` that records the live queue, in-flight file + pipeline stage, session processed/failed counts, recent meetings and failures, flushing atomically to `.state/status.json` on every change. It is threaded through the watcher (queue/dequeue/panic-fail) and `pipeline.ProcessVideo` (stage transitions + done); the serve worker reports pipeline failures. **All `Tracker` methods are nil-safe** — the one-shot `process` command passes a nil tracker. This file is the read-only data source for `run dashboard`.

### Pipeline (`internal/pipeline`)

1. Transcribe the video via the injected transcriber (real AssemblyAI or a deterministic mock) — speaker labels, auto chapters, language detection.
2. Analyze the transcript via the injected analyzer (backend from config, or a deterministic mock).
3. Write results via `internal/library`: raw transcript under `knowledge/transcripts/<id>.txt`, meeting note under `knowledge/meetings/<YYYY-MM-DD>-<slug>.md`, topic updates under `knowledge/topics/<slug>.md`, regenerated `knowledge/index.md`.
4. Record the file in `.state/processed.json`.

The pipeline receives transcriber/analyzer as injected functions so `--mock` requires no conditional logic inside the pipeline itself — keep this pattern when extending it.

### Analyzer backends (`internal/analyzer`)

All backends share the same prompt schema and parser:

- The prompt builder optionally points to a transcript file on disk.
- The parser extracts a JSON object from the model response and falls back to a minimal `general` topic if parsing fails.
- `cli.go` runs the kimi/claude backends: writes the transcript to a temp file, shells out to the configured binary with `-p`, collects the assistant text, and parses it.
- `lemur.go` calls LeMUR via the AssemblyAI Go SDK.

The analyzer JSON contract must stay stable — all backends depend on it.

## Code style and conventions

- Code, comments, and docs are in English; doc comments follow standard Go style.
- Keep the code `gofmt`-clean and `go vet`-clean.
- Logging goes through `internal/logging`; do not use `log`/`fmt` ad hoc in library packages.
- Do not store secrets in `config.yaml`; use environment variables.
- When writing files, create parent directories and write UTF-8.

## Deployment

Distributed via **Homebrew**, runs as a **user-level background service**, no sudo required:

```bash
brew tap fernando143/patro https://github.com/fernando143/patro.git
brew trust fernando143/patro  # recent Homebrew requires trusting third-party taps
brew install patro
patro init
```

The formula also ships a service (`brew services start patro`) running `patro serve`, logging to Homebrew's `var/log/patro.log`. `brew services` does not inherit the user's shell environment, so the API key must come from elsewhere (the wizard-installed service, or the launchctl/systemd environment).

### Release workflow

Pushing a tag `v*` triggers `.github/workflows/release.yml`, which runs GoReleaser (`.goreleaser.yaml`):

- Builds darwin/linux × amd64/arm64 binaries (CGO disabled, version injected via `-X main.version`).
- Creates the GitHub release on `fernando143/patro` with archives and checksums.
- Commits an updated `patro` formula to `Formula/patro.rb` in this same repo (the repo doubles as the Homebrew tap; users tap it with an explicit URL since it isn't named `homebrew-*`).

Runs with the workflow's built-in `GITHUB_TOKEN` (`contents: write`); no extra secrets required. The repository must stay public for `brew tap`/`brew install` to work for other users.

## Security considerations

- The AssemblyAI API key is read only from the `ASSEMBLYAI_API_KEY` environment variable — never commit it, never put it in `config.yaml`.
- The kimi/claude backends shell out to the binary configured in `kimi_path`/`claude_path` with `-p` (non-interactive, auto-approved permissions) — ensure the configured path is trustworthy.
- Writes stay confined to the knowledge library and `.state/` directories.
