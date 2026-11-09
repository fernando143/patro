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
| T1 | S1, S3 | Portable `~` expansion (`~/` and `~\`) in config and setup | inline | |
| T2 | S1, S3 | Windows executable validation in `setup.ValidateExecutable` | inline | |
| T3 | S1, S3 | Run `.cmd`/`.bat` analyzer CLIs through `cmd.exe /c` on Windows | inline | |
| T4 | S1, S3 | Windows background service (schtasks + user env var) in `delivery/setup` | worker | |
| T5 | S1, S3 | Windows process liveness and service status in the TUI | worker | |
| T6 | S1 | Windows-portable test suite (`go test ./...` green on Windows) | worker | |
| T7 | S2 | Release artifacts: goreleaser `windows/amd64` zip + Scoop bucket, CI matrix | inline | |
| T8 | S1, S2 | Docs: Windows install, config paths, service and logs | inline | |

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
