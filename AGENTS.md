# Agent Guide for patro

This file is written for AI coding agents working on the `patro` project. It describes the project's purpose, technology stack, code organization, runtime behavior, development conventions, and deployment process. Read this before making changes.

## Project overview

`patro` is a local Go service that watches a folder for new video recordings (typically OBS Studio output), uploads them to AssemblyAI for transcription, analyzes the transcript with an LLM, and appends the results to a Markdown knowledge library organized by topic.

The analyzer backend is pluggable:

- **Kimi (default)** — runs the already-installed local `kimi -p` CLI.
- **Claude** — runs the local `claude -p` CLI.
- **LeMUR** — uses AssemblyAI's hosted LLM.

The project is intentionally small and self-contained: a single static binary built from the Go module `github.com/fernando143/patro`, distributed via Homebrew from the GitHub repo `fernando143/patro`.

## Technology stack

- **Language**: Go 1.26+
- **Module**: `github.com/fernando143/patro` (`go.mod`)
- **Dependencies** (production only):
  - `fsnotify` — filesystem watcher for the `serve` command
  - `gopkg.in/yaml.v3` — configuration parsing
  - `assemblyai-go-sdk` — transcription and LeMUR analysis
  - `golang.org/x/text` — text processing
- **No CLI framework** — command and flag parsing uses the stdlib `flag` package.
- **Tests**: stdlib `testing`, table-driven style. No other test or lint tooling is configured beyond `gofmt` and `go vet`.

## Project structure

```
patro/
├── go.mod / go.sum             # Go module definition
├── config.yaml                 # user-facing configuration (repo-local example)
├── README.md                   # human-readable setup and usage
├── patro.log                   # runtime log (created by the app)
├── .goreleaser.yaml            # release build config (binaries + Homebrew formula)
├── .github/workflows/
│   └── release.yml             # tag-triggered release automation
├── cmd/patro/                  # main package: CLI entry point + init wizard
├── internal/
│   ├── types/                  # shared data types
│   ├── config/                 # Config struct + config loading/search
│   ├── state/                  # persistent processed-files state
│   ├── logging/                # shared logger
│   ├── library/                # Markdown knowledge library writer
│   ├── analyzer/               # prompt + shared parser; cli.go (kimi/claude subprocess); lemur.go
│   ├── transcriber/            # AssemblyAI transcription (assemblyai-go-sdk)
│   ├── watcher/                # fsnotify-based inbox watcher
│   └── pipeline/               # orchestration: ProcessVideo + mocks
├── knowledge/                  # generated knowledge library
│   ├── index.md
│   ├── topics/<slug>.md
│   ├── meetings/<YYYY-MM-DD>-<slug>.md
│   └── transcripts/<id>.txt
└── .state/
    └── processed.json          # deduplication state
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
- `stability_checks` / `stability_interval_seconds` — how long to wait for OBS to finish writing a file
- `analyzer_backend` — `"kimi"` (default), `"claude"`, or `"lemur"`
- `kimi_path` / `claude_path` — path to the respective CLI binary

The AssemblyAI API key is **not** stored in `config.yaml`; it is read from the environment variable `ASSEMBLYAI_API_KEY`.

## Runtime architecture

The application has three commands:

```bash
patro serve [--mock] [--config PATH]
patro process <file> [--mock] [--config PATH]
patro init [--config PATH]
patro --version
```

### `serve`

- Starts an fsnotify watcher on `config.inbox`.
- New or moved video files are submitted to a stability checker that waits until the file size is unchanged across `stability_checks` probes spaced `stability_interval_seconds` apart.
- Stable files are queued and processed sequentially.
- On startup, existing unprocessed videos in the inbox are scanned and enqueued.
- Runs until SIGINT/SIGTERM. Logs to `patro.log` and stdout.

### `process`

- Processes a single file end to end and exits.
- Skips the file if it is already recorded in `.state/processed.json` with the same name and size.

### `init`

- Interactive setup wizard: asks for the AssemblyAI API key, inbox, library, and analyzer backend (kimi/claude), locates the CLI binary, and writes the config.
- Optionally installs and starts a user-level background service: a systemd user unit `patro.service` with the API key in an `override.conf` on Linux, or a LaunchAgent `com.patro.plist` on macOS.

### Pipeline

1. Transcribe the video using the injected transcriber (real AssemblyAI transcription or a deterministic mock). Transcription uses speaker labels, auto chapters, and language detection.
2. Analyze the transcript using the injected analyzer (backend selected from config, or a deterministic mock).
3. Write the results via the library package:
   - raw transcript under `knowledge/transcripts/<id>.txt`
   - meeting note under `knowledge/meetings/<YYYY-MM-DD>-<slug>.md`
   - topic updates under `knowledge/topics/<slug>.md`
   - regenerated `knowledge/index.md`
4. Record the file in `.state/processed.json` so it is not reprocessed.

### Analyzer backends

All backends share the same prompt schema and parser in `internal/analyzer`:

- The prompt builder optionally points to a transcript file on disk.
- The parser extracts a JSON object from the model response and falls back to a minimal `general` topic if parsing fails.
- `cli.go` runs the kimi/claude backends: it writes the transcript to a temp file, shells out to the configured binary with `-p`, collects the assistant text, and parses it.
- `lemur.go` calls LeMUR via the AssemblyAI Go SDK.

## Code style and conventions

- Code, comments, and docs are in English; doc comments follow standard Go style.
- Keep the code `gofmt`-clean and `go vet`-clean.
- Tests use the stdlib `testing` package, table-driven style.
- Logging goes through `internal/logging`; do not use `log`/`fmt` ad hoc in library packages.
- Keep the analyzer JSON contract stable; all backends depend on it.
- Keep the pipeline injectable: it receives transcriber/analyzer functions so `--mock` does not require conditional logic inside the pipeline.
- Do not store secrets in `config.yaml`; use environment variables.
- When writing files, create parent directories and write UTF-8.

## Build and run commands

```bash
cd /home/fernando/projects/meeting-scribe

go build ./...
go vet ./...
go test ./...
```

Build the binary:

```bash
go build -o patro ./cmd/patro
```

Set the API key for real runs:

```bash
export ASSEMBLYAI_API_KEY=<your-key>
```

Run the watcher:

```bash
./patro serve
```

Process one recording:

```bash
./patro process ~/Videos/obs/meeting.mkv
```

Run without any API calls (deterministic fake pipeline):

```bash
./patro process --mock <file>
./patro serve --mock
```

Build a local release snapshot (requires [GoReleaser](https://goreleaser.com/)):

```bash
goreleaser release --snapshot --clean
```

## Testing instructions

Run the unit tests:

```bash
go test ./...
```

Also use `--mock` mode as a manual smoke test:

1. Create or copy a small video file into the configured inbox.
2. Run `./patro process --mock <path>`.
3. Verify that `knowledge/meetings/`, `knowledge/topics/`, and `knowledge/index.md` are updated.

## Deployment

`patro` is distributed via **Homebrew** and runs as a **user-level background service** with no sudo required.

User install:

```bash
brew tap fernando143/patro https://github.com/fernando143/patro.git
brew trust fernando143/patro  # recent Homebrew requires trusting third-party taps
brew install patro
patro init
```

The formula also ships a service (`brew services start patro`) that runs `patro serve` and logs to Homebrew's `var/log/patro.log`. The API key must be provided through the environment — for example via the wizard-installed service or the launchctl/systemd environment — since `brew services` does not inherit the user's shell environment.

### Release workflow

Pushing a tag `v*` triggers `.github/workflows/release.yml`, which runs GoReleaser (`.goreleaser.yaml`):

- Builds darwin/linux × amd64/arm64 binaries (CGO disabled, version injected via `-X main.version`).
- Creates the GitHub release on `fernando143/patro` with archives and checksums.
- Commits an updated `patro` formula to `Formula/patro.rb` in this same repository (the repo doubles as the Homebrew tap; users tap it with an explicit URL since it is not named `homebrew-*`).

Everything runs with the workflow's built-in `GITHUB_TOKEN` (`contents: write`); no extra secrets are required. The repository must be public for `brew tap`/`brew install` to work for other users.

## Security considerations

- The AssemblyAI API key is read only from the `ASSEMBLYAI_API_KEY` environment variable. Do not commit it.
- The kimi/claude backends shell out to the binary configured in `kimi_path`/`claude_path` with `-p` (non-interactive, auto-approved permissions). Ensure the binary path is trustworthy.
- The application writes files under the knowledge library and `.state/` directories. It does not require elevated privileges.
