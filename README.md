# patro-scriber

`patro-scriber` is a small, local Python service that turns your meeting recordings into a growing Markdown knowledge library.

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

- **Python 3.11 or newer**
- **Linux or macOS** (Windows is not supported yet)
- An **[AssemblyAI](https://www.assemblyai.com/) API key** — transcription runs through their service
- Either **Kimi Code CLI** or **Claude Code CLI** installed locally, if you want to use a local AI to write the notes

## Quick install (recommended)

The easiest way to install `patro-scriber` is the interactive wizard. It will:

1. Create a Python virtual environment.
2. Ask for your AssemblyAI API key.
3. Ask where your recordings folder is.
4. Ask where to write the knowledge library.
5. Ask whether you want to use Kimi or Claude as the note writer.
6. Install and start a user-level background service.

```bash
git clone https://github.com/fernando143/patro.git
cd patro
python3 install-wizard.py
```

The wizard uses a simple TUI built with [Rich](https://github.com/Textualize/rich). Just follow the prompts.

After it finishes, the service is already running. Check the logs:

- **Linux**: `journalctl --user -u scribe -f`
- **macOS**: `tail -f /tmp/scribe.out.log /tmp/scribe.err.log`

Check the service status:

- **Linux**: `systemctl --user status scribe`
- **macOS**: `launchctl list | grep com.scribe`

## Manual setup

If you prefer not to use the wizard:

```bash
cd patro
python3 -m venv .venv
.venv/bin/pip install -e .

export ASSEMBLYAI_API_KEY=<your-key>
```

Edit `config.yaml`:

- `inbox`: absolute path to the folder where recordings appear
- `library`: absolute path where the knowledge library should be written
- `analyzer_backend`: `kimi`, `claude`, or `lemur`
- `kimi_path` / `claude_path`: absolute path to the CLI binary when running as a service

Then install the service manually:

```bash
install/install.sh
```

And add the API key to the service:

```bash
# Linux
systemctl --user edit scribe
# Add: [Service] Environment=ASSEMBLYAI_API_KEY=<your-key>

# macOS
# Edit ~/Library/LaunchAgents/com.scribe.plist and add an EnvironmentVariables dict.
```

## Usage

After installation, drop or save a video into the configured inbox folder. The service will pick it up once the file stops growing and process it automatically.

You can also run commands manually. The installed command is `patro-scriber` (`scribe` is also available as an alias):

```bash
# Process a single file
.venv/bin/patro-scriber process /absolute/path/to/meeting.mkv

# Watch the inbox forever
.venv/bin/patro-scriber serve

# Run the full pipeline with fake data, no API calls (great for testing)
.venv/bin/patro-scriber process --mock /absolute/path/to/any-video.mkv
.venv/bin/patro-scriber serve --mock
```

Already-processed files are tracked in `.state/processed.json` by file name and size, so they are not reprocessed unless the file changes.

## How it works

1. **Watch**: the `serve` command watches the `inbox` folder for new `.mkv`, `.mp4`, `.mov`, or `.webm` files.
2. **Stabilize**: because OBS writes files progressively, the service waits until the file size stops changing.
3. **Transcribe**: the audio is sent to AssemblyAI. A raw transcript with speaker labels is saved under `knowledge/transcripts/`.
4. **Analyze**: the transcript is passed to the chosen AI backend (Kimi, Claude, or LeMUR), which returns a structured JSON note.
5. **Write**: a meeting note is saved under `knowledge/meetings/`, topic files are updated under `knowledge/topics/`, and `knowledge/index.md` is regenerated.

## Testing without spending API credits

Use `--mock` mode to verify the whole pipeline without calling AssemblyAI or any AI backend:

```bash
.venv/bin/patro-scriber process --mock /path/to/a/video.mkv
```

This uses deterministic fake transcripts and analysis, and still writes real output files to `knowledge/`. It is the recommended way to check that the installation works.

## Troubleshooting

### Service fails with "ASSEMBLYAI_API_KEY is not set"

Re-run the wizard, or manually add the key to the service environment.

### "'kimi' executable not found" or "'claude' executable not found"

Make sure the CLI is installed and that `config.yaml` points to the absolute path of the binary. Systemd services do not read your shell profile, so relative names like `kimi` may not resolve.

### Videos are not being processed

- Check that the `inbox` path in `config.yaml` is correct and absolute.
- Check the logs.
- Make sure the video extension is in `video_extensions`.

## Development

Install in editable mode:

```bash
python3 -m venv .venv
.venv/bin/pip install -e .
```

There is no automated test suite yet. Use `--mock` mode as a smoke test.

## Security notes

- The AssemblyAI API key is stored only in the service environment, never in `config.yaml` or the repository.
- The Kimi and Claude backends shell out to your local CLI binaries. Make sure you trust the paths configured in `config.yaml`.
- Everything runs at user level; no `sudo` is required.
