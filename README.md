# patro

[![ci](https://github.com/fernando143/patro/actions/workflows/ci.yml/badge.svg)](https://github.com/fernando143/patro/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/fernando143/patro/branch/main/graph/badge.svg)](https://codecov.io/gh/fernando143/patro)

`patro` is a small, local service written in Go that turns your meeting recordings into a growing Markdown knowledge library.

It watches a folder for new videos (for example, where OBS Studio saves your recordings), sends the audio to [AssemblyAI](https://www.assemblyai.com/) for transcription, and then asks a local AI — [Kimi Code CLI](https://www.kimi.com/code), [Claude Code CLI](https://claude.ai/download), Codex CLI, or the Pi coding agent — to write structured notes. The results are saved locally and organized by topic.

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

- **Linux, macOS or Windows**
- An **[AssemblyAI](https://www.assemblyai.com/) API key** — transcription runs through their service
- Either **Kimi Code CLI** or **Claude Code CLI** installed locally, if you want to use a local AI to write the notes (Codex CLI and the Pi coding agent work too)
- **Homebrew** for the macOS/Linux install, **Scoop** for Windows, or **Go 1.26+** to build from source

## Quick install (recommended)

```bash
brew tap fernando143/patro https://github.com/fernando143/patro.git
brew trust fernando143/patro  # recent Homebrew requires trusting third-party taps
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
- **Windows**: see `patro.log` next to your config file (see below)

Check the service status:

- **Linux**: `systemctl --user status patro`
- **macOS**: `launchctl list | grep com.patro`
- **Windows**: the `patro` entry under `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

## Windows

Every release ships `patro_<version>_windows_amd64.zip` (and `arm64`). Two ways in:

**Scoop** (recommended — installs and keeps it updated):

```powershell
scoop bucket add patro https://github.com/fernando143/patro
scoop install patro
patro init
```

**Manual**: download the `.zip` from the [releases page](https://github.com/fernando143/patro/releases), extract `patro.exe` somewhere on your `PATH`, then run `patro init`.

Windows has no user-level systemd or LaunchAgent, and both Windows substitutes fail without administrator rights (a real service needs the Service Control Protocol and `sc create`; `schtasks /sc onlogon` is refused for unprivileged users). So the wizard registers the watcher in your **per-user autostart list** (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`) and starts it immediately — no elevation, nothing runs outside your session:

- Check it: `reg query HKCU\Software\Microsoft\Windows\CurrentVersion\Run /v patro`
- Stop the autostart: `reg delete HKCU\Software\Microsoft\Windows\CurrentVersion\Run /v patro /f`
- Logs: `%USERPROFILE%\.config\patro\patro.log`

Because there is no supervisor behind a Run entry, a crashed watcher comes back at your next logon, or right away from the settings screen in `patro run tui`.

The API key goes into your user environment (the Windows spelling of `export`), so the watcher sees it. To set it yourself:

```powershell
setx ASSEMBLYAI_API_KEY "<your-key>"
```

A new shell picks the key up; the wizard has already started the watcher.

### Running via `brew services`

The formula ships a service that runs `patro serve` and logs to Homebrew's `var/log/patro.log`:

```bash
brew services start patro
```

Note that the `brew services` environment does not include your API key. The key must come from the environment — for example, use the service installed by `patro init` instead (which stores the key in a systemd `override.conf` on Linux or in the LaunchAgent plist on macOS), or otherwise export `ASSEMBLYAI_API_KEY` in the service environment.

This channel is macOS/Linux only; on Windows use Scoop or the release zip.

## Manual setup

If you prefer not to use the wizard, create the canonical config file at `~/.config/patro/config.yaml` (`%USERPROFILE%\.config\patro\config.yaml` on Windows). `patro` uses this same file for the service and TUI:

1. `--config PATH` flag (explicit override)
2. `~/.config/patro/config.yaml`

An existing repository-local `./config.yaml` is migrated to the canonical path
only when the canonical file does not exist yet. Once the canonical file
exists, `./config.yaml` is not read implicitly.

Everything relative (`inbox`, `library`, `.state/`, `patro.log`) resolves against the directory containing the config file.

Config keys:

- `inbox`: absolute path to the folder where recordings appear
- `library`: path where the knowledge library should be written
- `video_extensions`: list of extensions that trigger processing
- `stability_checks` / `stability_interval_seconds`: how long to wait for OBS to finish writing a file
- `analyzer_backend`: `kimi`, `claude`, `codex`, `pi`, or `lemur`
- `kimi_path` / `claude_path` / `codex_path` / `pi_path`: absolute path to the CLI binary when running as a service

The AssemblyAI API key is read only from the `ASSEMBLYAI_API_KEY` environment variable — never put it in `config.yaml`:

```bash
export ASSEMBLYAI_API_KEY=<your-key>
```

On Windows that is `setx ASSEMBLYAI_API_KEY "<your-key>"` for the persistent user
environment, or `$env:ASSEMBLYAI_API_KEY = "<your-key>"` for the current PowerShell.

## Usage

After installation, drop or save a video into the configured inbox folder. The service will pick it up once the file stops growing and process it automatically. Stop the watcher with `SIGINT`/`SIGTERM` (Ctrl+C).

You can also run commands manually:

```bash
# Process a single file
patro process /absolute/path/to/meeting.mkv

# Watch the inbox forever
patro serve

# Preview merge candidates across every historical topic (never writes files)
patro reconcile --all --dry-run

# Review and selectively apply historical topic merges
patro run tui  # open Migrate

# Run the full pipeline with fake data, no API calls (great for testing)
patro process --mock /absolute/path/to/any-video.mkv
patro serve --mock

# Print the version
patro --version
```

Already-processed files are tracked in `.state/processed.json` (next to the config file) by file name and size, so they are not reprocessed unless the file changes.

### Migrating pre-v0.4.0 topic libraries

`patro reconcile --all --dry-run` reads every `knowledge/topics/*.md`, computes a deterministic merge plan with the configured embedding backend, and prints titles, paths, cosine scores, impact counts, and content hashes. It does not modify the knowledge library, state, or indexes.

Apply historical merges only through **Migrate** in `patro run tui`. Every proposal must be accepted or rejected independently (with an accept-all shortcut), followed by a final confirmation. Only accepted sources are merged and removed. Before writing, Patro verifies the preview hashes and creates a timestamped backup under `.state/backups/historical-topic-migration/`; stale previews are rejected without mutation. Vector and full-text indexes are rebuilt after a successful migration.

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

### Documentation

The project documentation is served from `docs/` with Docsify. Install the
CLI once and start the local server from the repository root:

```bash
npm install --global docsify-cli@5
docsify serve docs
```

```bash
git clone https://github.com/fernando143/patro.git
cd patro

# Source builds require Git LFS to hydrate the embedded Cybertron model.
git lfs pull

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

Git LFS is only required for source/build contributors. Homebrew and direct
release installs download an ordinary Patro binary with the Cybertron model
already embedded, so they do not download a separate model at runtime.

## Security notes

- The AssemblyAI API key is read only from the `ASSEMBLYAI_API_KEY` environment variable, never from `config.yaml` or the repository.
- The Kimi and Claude backends shell out to your local CLI binaries with `-p`. Make sure you trust the paths configured in `config.yaml`.
- Writes stay under the knowledge library and `.state/` directories.
- Everything runs at user level; no `sudo` is required.
