# Windows support

Make `patro` installable and usable on Windows: a `windows/amd64` release
artifact, an install channel, and the platform-specific pieces that currently
assume a Unix userland (service installation, executable validation, process
liveness, npm `.cmd` shims). macOS and Linux behavior must not change.

## Specs

- **S1** — The project must be installable and usable on Windows.
  > "quiero que este proyecto tambien se pueda instalar y utilizar en windows"
- **S2** — Windows support must be installable from a published release.
  > "que se pueda instalar mediante alguna release"
- **S3** — Existing macOS and Linux behavior must not change: the Homebrew
  formula, systemd/LaunchAgent service installation, and the Unix exec-bit
  validation keep working exactly as they do today. Install paths for
  existing users (`~/.config/patro/config.yaml`) must not move.

## Tasks

| ID | Spec | What | Route | Commit |
|----|------|------|-------|--------|
| T1 | S1, S3 | Portable `~` expansion (`~/` and `~\`) in config and setup | inline | 0c0f87d |
| T2 | S1, S3 | Windows executable validation in `setup.ValidateExecutable` | inline | 0c0f87d |
| T3 | S1, S3 | Run `.cmd`/`.bat` analyzer CLIs through `cmd.exe /c` on Windows | inline | 0c0f87d |
| T4 | S1, S3 | Windows background service (schtasks + user env var) in `delivery/setup` | inline | |
| T5 | S1, S3 | Windows process liveness and service status in the TUI | inline | |
| T6 | S1 | Windows-portable test suite (`go test ./...` green on Windows) | worker | |
| T7 | S2 | Release artifacts: goreleaser `windows/amd64` zip + Scoop bucket, CI matrix | inline | |
| T8 | S1, S2 | Docs: Windows install, config paths, service and logs | inline | |
| T9 | S1 | Keep Git from rewriting the embedded model assets (`-text`) | inline | |

### Delivery slices

The feature is too large for one reviewable change, so it is delivered in
slices; each slice keeps tests and docs with the behavior it ships.

- **Slice 1** — `f85073f..0c0f87d`: the ODD task documents plus T1-T3 (533
authored lines). Portable paths, Windows executable validation, `.cmd` shim
execution.
- **Slice 2** — T4-T5: the Windows background service and the two runtime
probes.
- **Slice 3** — T6: the portable test suite and the `logging` handle fix.
- **Slice 4** — T7-T8: release artifacts, CI matrix and documentation.

## Log

- **L1** (user): "quiero que este proyecto tambien se pueda instalar y utilizar en windows"
- **L2** (user): "agrega soporte para windows entonces, y que se pueda instalar mediante alguna release. Por otro lado agrega tambien como cli pi agents el cli"
  - Split into two feature documents: this one covers Windows support; the `pi`
    CLI backend lives in `odd/tasks/pi-analyzer-backend.md`.
- **L3** (evidence, measured on this Windows host): `go build ./...` and
  `go vet ./...` already pass on `windows/amd64`. The blockers are runtime and
  packaging, not compilation.
- **L4** (decision): the canonical config path stays
  `%USERPROFILE%\.config\patro\config.yaml`. `os.UserHomeDir()` returns
  `%USERPROFILE%` on Windows, so no existing resolver changes and the docs stay
  honest across the three platforms (S3).
- **L5** (decision): the Windows background service is a per-user Scheduled
  Task created with `schtasks /sc onlogon` plus a user environment variable
  authored with `setx` for the API key. No administrator rights, no Windows
  Service Control handler (a Go process without one would be killed by the SCM).
- **L6** (evidence): npm-installed CLIs are `.cmd` shims on Windows and
  `CreateProcess` cannot execute them directly, so every analyzer backend
  (`kimi`, `claude`, `codex`, `pi`) needs the `cmd.exe /d /s /c` route.
- **L7** (evidence, measured with a throwaway probe): with `cmd /d /s /c` and a
  pre-built command line, a multi-line prompt truncated at the first newline
  while the flattened form arrived intact, embedded `"` survived as `"`, and
  `&`, `%` and tabs were preserved. Hence the flatten-and-quote rule.
- **L8** (decision): the TUI's Windows service status asks PowerShell for
  `(Get-ScheduledTask).State` instead of parsing `schtasks` output, whose
  `Running`/`Ready` strings are localized; the enum name is English on every
  locale.
- **L9** (decision): a failed `reg add` must not go through `runQuiet`, which
  echoes the whole command line — on Windows that line carries the API key. The
  key is written by a dedicated runner whose error names the variable only.
- **L10** (evidence, measured): `patro process --mock` built and run on this
  Windows host processed a file end to end — but only after fixing a silent
  data bug: `core.autocrlf=true` had rewritten `weights/cybertron/vocab.txt`
  from 231508 to 262030 bytes (one extra byte per line), so the embedded
  weights failed their sha256 check and reconciliation was disabled with only a
  warning in the log. `.gitattributes` now marks the whole weights directory
  `-text`, which is what model assets need regardless of their extension; the
  LFS line for `spago_model.bin` stays last so it still wins for that file.
  Anyone who checked out the repo on Windows before this fix must re-checkout
  those three files (`rm` + `git checkout --`) or their binary will embed
  corrupted weights.
