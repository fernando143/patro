# patro

`patro` is a small, local service written in Go that turns your meeting recordings into a growing Markdown knowledge library.

It watches a folder for new videos (for example, where OBS Studio saves your recordings), sends the audio to [AssemblyAI](https://www.assemblyai.com/) for transcription, and then asks a local AI — [Kimi Code CLI](https://www.kimi.com/code) or [Claude Code CLI](https://claude.ai/download) — to write structured notes. The results are saved locally and organized by topic.

You can also analyze transcripts with AssemblyAI's own LeMUR model by setting `analyzer_backend: lemur` in `config.yaml`.

## What you get

```
knowledge/
├── topics/<slug>.md                  # distilled knowledge per topic, appended over time
├── meetings/<YYYY-MM-DD>-<slug>.md   # full note per meeting (summary, decisions, action items, chapters)
├── transcripts/<transcript_id>.txt   # raw transcript with speaker labels
└── index.md                          # regenerated on every run
```

## What you need before installing

- **Linux or macOS** (Windows is not supported yet)
- An **[AssemblyAI](https://www.assemblyai.com/) API key** — transcription runs through their service
- Either **Kimi Code CLI** or **Claude Code CLI** installed locally, if you want to use a local AI to write the notes
- **Homebrew**, for the recommended install (or Go 1.26+ to build from source)

## Quick install (recommended)

```bash
brew tap fernando143/patro https://github.com/fernando143/patro.git
brew install patro
```

Then run the interactive setup wizard:

```bash
patro init
```

The wizard will:

1. Ask for your AssemblyAI API key.
2. Ask where your recordings folder is.
3. Ask where to write the knowledge library.
4. Ask whether you want to use Kimi or Claude as the note writer (and locate the CLI binary).
5. Write the config file.
6. Optionally install and start a user-level background service.

After it finishes, the service is already running. Check the logs:

- **Linux**: `journalctl --user -u patro -f`
- **macOS**: `log stream --predicate 'process == "patro"'` (or watch the log file configured in the plist)

Check the service status:

- **Linux**: `systemctl --user status patro`
- **macOS**: `launchctl list | grep com.patro`

### Running via `brew services`

The formula ships a service that runs `patro serve` and logs to Homebrew's `var/log/patro.log`:

```bash
brew services start patro
```

Note that the `brew services` environment does not include your API key. The key must come from the environment — for example, use the service installed by `patro init` instead (which stores the key in a systemd `override.conf` on Linux or in the LaunchAgent plist on macOS), or otherwise export `ASSEMBLYAI_API_KEY` in the service environment.

## Manual setup

If you prefer not to use the wizard, create a config file yourself. `patro` looks for configuration in this order:

1. `--config PATH` flag
2. `./config.yaml` (current directory)
3. `~/.config/patro/config.yaml`

Everything relative (`inbox`, `library`, `.state/`, `patro.log`) resolves against the directory containing the config file.

Config keys:

- `inbox`: absolute path to the folder where recordings appear
- `library`: path where the knowledge library should be written
- `video_extensions`: list of extensions that trigger processing
- `stability_checks` / `stability_interval_seconds`: how long to wait for OBS to finish writing a file
- `analyzer_backend`: `kimi`, `claude`, or `lemur`
- `kimi_path` / `claude_path`: absolute path to the CLI binary when running as a service

The AssemblyAI API key is read only from the `ASSEMBLYAI_API_KEY` environment variable — never put it in `config.yaml`:

```bash
export ASSEMBLYAI_API_KEY=<your-key>
```

## Usage

After installation, drop or save a video into the configured inbox folder. The service will pick it up once the file stops growing and process it automatically. Stop the watcher with `SIGINT`/`SIGTERM` (Ctrl+C).

You can also run commands manually:

```bash
# Process a single file
patro process /absolute/path/to/meeting.mkv

# Watch the inbox forever
patro serve

# Run the full pipeline with fake data, no API calls (great for testing)
patro process --mock /absolute/path/to/any-video.mkv
patro serve --mock

# Print the version
patro --version
```

Already-processed files are tracked in `.state/processed.json` (next to the config file) by file name and size, so they are not reprocessed unless the file changes.

## How it works

1. **Watch**: the `serve` command watches the `inbox` folder for new `.mkv`, `.mp4`, `.mov`, or `.webm` files.
2. **Stabilize**: because OBS writes files progressively, the service waits until the file size is unchanged across `stability_checks` probes spaced `stability_interval_seconds` apart.
3. **Transcribe**: the audio is sent to AssemblyAI (speaker labels, auto chapters, language detection). A raw transcript with speaker labels is saved under `knowledge/transcripts/`.
4. **Analyze**: the transcript is passed to the chosen AI backend (Kimi, Claude, or LeMUR), which returns a structured JSON note.
5. **Write**: a meeting note is saved under `knowledge/meetings/`, topic files are updated under `knowledge/topics/`, and `knowledge/index.md` is regenerated.

## Testing without spending API credits

Use `--mock` mode to verify the whole pipeline without calling AssemblyAI or any AI backend:

```bash
patro process --mock /path/to/a/video.mkv
```

This uses deterministic fake transcripts and analysis, and still writes real output files to the knowledge library. It is the recommended way to check that the installation works.

## Troubleshooting

### Service fails with "ASSEMBLYAI_API_KEY is not set"

Re-run `patro init`, or manually add the key to the service environment (`systemctl --user edit patro` on Linux, the `EnvironmentVariables` dict in `~/Library/LaunchAgents/com.patro.plist` on macOS).

### "'kimi' executable not found" or "'claude' executable not found"

Make sure the CLI is installed and that `config.yaml` points to the absolute path of the binary. Systemd services do not read your shell profile, so relative names like `kimi` may not resolve. `patro init` locates the binary for you.

### Videos are not being processed

- Check that the `inbox` path in `config.yaml` is correct and absolute.
- Check the logs.
- Make sure the video extension is in `video_extensions`.

## Development

Requires Go 1.26 or newer.

```bash
git clone https://github.com/fernando143/patro.git
cd patro

go build ./...
go vet ./...
go test ./...

# Build the binary and run the smoke test (no API key needed)
go build -o patro ./cmd/patro
./patro process --mock /path/to/a/video.mkv
```

Build a local release snapshot with [GoReleaser](https://goreleaser.com/):

```bash
goreleaser release --snapshot --clean
```

## Security notes

- The AssemblyAI API key is read only from the `ASSEMBLYAI_API_KEY` environment variable, never from `config.yaml` or the repository.
- The Kimi and Claude backends shell out to your local CLI binaries with `-p`. Make sure you trust the paths configured in `config.yaml`.
- Writes stay under the knowledge library and `.state/` directories.
- Everything runs at user level; no `sudo` is required.
