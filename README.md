# meeting-scribe

Local service that watches the folder where OBS Studio saves recordings, uploads new
videos to [AssemblyAI](https://www.assemblyai.com/), transcribes them, distills each
meeting with your local [Kimi Code CLI](https://www.kimi.com/code) (`kimi -p`) or
[Claude Code CLI](https://claude.ai/download) (`claude`) by default, and appends the
results to a Markdown knowledge library organized by topic, which grows with every
meeting.

You can also switch the analyzer backend to AssemblyAI LeMUR by setting
`analyzer_backend: lemur` in `config.yaml`.

```
knowledge/
├── topics/<slug>.md                  # distilled knowledge per topic, appended over time
├── meetings/<YYYY-MM-DD>-<slug>.md   # full note per meeting (summary, decisions, action items, chapters)
├── transcripts/<transcript_id>.txt   # raw transcript with speaker labels
└── index.md                          # regenerated on every run
```

## Quick install (recommended)

Run the interactive wizard. It creates the virtual environment, asks for your
AssemblyAI API key, asks where to watch for recordings, and installs scribe as a
user-level background service.

```bash
cd ~/projects/meeting-scribe
python3 install-wizard.py
```

After the wizard finishes, the service is already running. Skip to
[Usage](#usage) or check the logs:

- **Linux**: `journalctl --user -u scribe -f`
- **macOS**: `tail -f /tmp/scribe.out.log /tmp/scribe.err.log`

## Manual setup

```bash
cd ~/projects/meeting-scribe
python3 -m venv .venv
.venv/bin/pip install -e .        # or: .venv/bin/pip install watchdog assemblyai pyyaml

export ASSEMBLYAI_API_KEY=<your-key>   # required for real transcription
```

The Kimi and Claude backends use your already-installed `kimi` or `claude` binary
(`kimi -p` / `claude -p`) and do not need an extra API key. If you prefer to analyze
with AssemblyAI LeMUR instead, set `analyzer_backend: lemur` in `config.yaml`.

Edit `config.yaml`:

- `inbox`: folder OBS records into (`~/Videos/obs` on Linux; OBS defaults to `~/Movies` on macOS)
- `library`: where the knowledge library is written
- `stability_checks` / `stability_interval_seconds`: a file is processed once its size
  stops changing (OBS writes progressively)

## Usage

```bash
# Process a single file
.venv/bin/python -m scribe process ~/Videos/obs/meeting.mkv

# Watch the inbox forever
.venv/bin/python -m scribe serve

# Full pipeline without an API key (deterministic fakes, great for verification)
.venv/bin/python -m scribe process --mock <file>
.venv/bin/python -m scribe serve --mock
```

Already-processed files (tracked in `.state/processed.json` by name + size) are skipped.

## Install as a service

```bash
install/install.sh
```

- **Linux**: installs a systemd *user* unit (`~/.config/systemd/user/scribe.service`) and
  enables it. Set the key with `systemctl --user edit scribe` (drop-in
  `Environment=ASSEMBLYAI_API_KEY=...`). Logs: `journalctl --user -u scribe -f`.
- **macOS**: installs a LaunchAgent (`~/Library/LaunchAgents/com.scribe.plist`). Add an
  `EnvironmentVariables` dict with the API key to the plist. Logs: `/tmp/scribe.out.log`.

Everything runs at user level — no sudo required. The service auto-restarts on failure.
