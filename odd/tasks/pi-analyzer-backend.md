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
| T1 | S1, S3, S4 | Plain-text stdout reader alongside the JSONL reader | worker | |
| T2 | S1, S2, S4 | Register the `pi` backend with its argv and help text | worker | |
| T3 | S1, S2 | Tests: registry, config key `pi_path`, analyzer run | worker | |
| T4 | S1, S3 | Docs: README backends table, `config.example.yaml`, AGENTS.md | inline | |

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
