# Pi analyzer backend

Add the `pi` coding-agent CLI as a first-class analyzer backend, next to
`kimi`, `claude`, `codex` and the hosted `lemur`, so `analyzer_backend: pi`
works from config, the setup wizards, the settings TUI and the gray-zone
decider without any per-backend special casing.

## Specs

- **S1** — The `pi` CLI must be available as an analyzer backend CLI, the same
  way the existing local CLIs are.
  > "Por otro lado agrega tambien como cli pi agents el cli"
- **S2** — The backend must go through the shared registry so config
  validation, both init wizards, the settings TUI and the gray-zone decider
  pick it up with no second list to keep in sync.
- **S3** — The analyzer JSON contract must not change: `pi` answers with the
  same schema and is parsed by the same defensive `ParseAnalysis`.
- **S4** — `pi` runs non-interactively (`-p`), without persisted sessions, with
  only the `read` tool enabled so it can read the staged transcript and cannot
  write anything.

## Tasks

| ID | Spec | What | Route | Commit |
|----|------|------|-------|--------|
| T1 | S1, S3, S4 | Plain-text stdout reader alongside the JSONL reader | inline | e3f176f |
| T2 | S1, S2, S4 | Register the `pi` backend with its argv and help text | inline | e3f176f |
| T3 | S1, S2 | Tests: reader, registry wiring, `pi_path` config key | inline | e3f176f |
| T4 | S1, S3 | Docs: README backends, `config.example.yaml`, AGENTS.md | inline | e3f176f |

## Log

- **L1** (user): "Por otro lado agrega tambien como cli pi agents el cli"
  - Interpreted as: add the `pi` CLI (the Pi coding agent) as an analyzer
    backend that shells out in non-interactive mode, mirroring the existing
    `kimi`/`claude`/`codex` backends.
- **L2** (evidence, measured): `pi --version` → `1.2.0`; on this Windows host
  `where pi` resolves to `C:\Users\azari\AppData\Roaming\npm\pi.cmd`.
  `pi -p` requires an authenticated provider, exactly like `claude`/`codex`, so
  a missing login surfaces as a normal non-zero-exit error.
- **L3** (decision): `pi` is invoked as
  `pi -p <prompt> --no-session --no-tools --tools read --no-context-files`.
  `--no-session` keeps runs ephemeral, `--tools read` is the minimum needed to
  read the staged transcript, and `--no-context-files` stops a stray
  `AGENTS.md` in the working directory from polluting the answer.
- **L4** (decision): `pi -p` emits the assistant text directly on stdout, not
  JSONL, so the registry grows a plain-text reader mode instead of forcing the
  `stream-json` reader every other CLI backend uses.
- **L5** (implementation): the reader mode is a `PlainText` field on the
  registry entry plus one branch in `AnalyzeCLI`; the stream path, including its
  "no assistant text in its stream-json output" error, stays byte-identical for
  the four existing backends so no current behavior moves.
- **L6** (sequencing): the registry entry (T2) waits for the Windows test sweep
  to land, because `cmd/patro/init_test.go` and
  `internal/delivery/tui/settings_test.go` assert the offered backend list and
  a second writer is editing them.
- **L7** (evidence, measured): after registering `pi`, only four assertions in
  the repository pinned the backend set by hand (`internal/adapter/analyzer/backend`'s
  three tables and `config`'s "invalid analyzer_backend" message).
  `cmd/patro` and `internal/delivery/tui` passed untouched, which is the
  registry doing its job.
- **L8** (evidence, measured): `TestAnalyzeCLIParsesAPlainTextBackend` runs the
  real `pi` registry entry against a fake CLI that prints plain JSON, and gets a
  parsed analysis back — the wiring, not just the reader.
