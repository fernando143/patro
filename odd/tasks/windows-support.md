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
| T4 | S1, S3 | Windows background service (per-user autostart entry) in `delivery/setup` | inline | e4365c3 |
| T5 | S1, S3 | Windows process liveness and service status in the TUI | inline | e4365c3 |
| T6 | S1 | Windows-portable test suite (`go test ./...` green on Windows) | worker | 63e4424 |
| T7 | S2 | Release artifacts: goreleaser `windows/amd64` zip + Scoop bucket, CI matrix | inline | d46ac88 |
| T8 | S1, S2 | Docs: Windows install, config paths, service and logs | inline | d46ac88 |
| T9 | S1 | Keep Git from rewriting the embedded model assets (`-text`) | inline | c050b7d |
| T10 | S1 | Flush the vector store and clean URL paths without POSIX assumptions | inline | 77994b1 |

### Delivery slices

The feature is too large for one reviewable change, so it is delivered in
slices; each slice keeps tests and docs with the behavior it ships.

- **Slice 1** — `f85073f..0c0f87d`: the ODD task documents plus T1-T3 (533
authored lines). Portable paths, Windows executable validation, `.cmd` shim
execution.
- **Slice 2** — `c050b7d`, `e4365c3`: the embedded-asset fix and T4-T5 (the
per-user autostart watcher and the two runtime probes). The test files for
this slice land with slice 3, because the portable-test sweep is rewriting
several of them at once.
- **Slice 3** — `63e4424`, `77994b1`: T6 plus T10 — the portable test suite, the
`logging` handle fix, and the two Windows-only bugs the suite exposed.
- **Slice 4** — `d46ac88`: T7-T8, release artifacts, CI matrix and
documentation.

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
- **L5** (decision): the Windows background service is a **per-user autostart
  entry** in `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, with the
  API key in the user environment authored through `reg add` under
  `HKCU\Environment`. No administrator rights, and no Windows Service Control
  handler (a Go process without one would be killed by the SCM).
- **L11** (evidence, measured — supersedes the `schtasks` decision in L5):
  `schtasks /create /sc once` succeeds unprivileged on this host, but
  `schtasks /create /sc onlogon` fails with "Acceso denegado": a logon trigger
  is a security boundary, so a Scheduled Task cannot satisfy the project's
  "no administrator rights" promise. The HKCU Run key can. Verified end to end
  with a throwaway value name: the registry stores
  `"C:\…\probe dir\patro.exe" serve --mock --config "C:\…\config.yaml"`
  with quoting intact, `Get-CimInstance Win32_Process` with a
  `CommandLine -like '* serve*'` filter reports the watcher as running, the same
  filter stops it, and the probe entry was then deleted. Consequence accepted:
  a Run entry has no supervisor, so there is no restart-on-failure — a crashed
  watcher returns at the next logon or from the settings screen.
- **L12** (evidence, measured): the portable suite turned two "test failures"
  into real Windows bugs. `OSCommitFS.SyncFile` opened the target read-only and
  then flushed it, and Windows answers `FlushFileBuffers` on a read-only handle
  with access denied, so every vector-store commit failed; `SyncDir` has no
  Windows equivalent at all — NTFS journals the rename itself — so it is now a
  documented no-op there. And the web viewer cleaned the request path with
  `filepath.Clean`, which turns `/search` into a backslash path on Windows and
  sent every route to the 404 branch.
- **L13** (evidence, measured): `go vet ./...` and `go test ./...` are green on
  this Windows host — 20 packages, no failures. The sweep added eleven skip
  sites covering four distinct reasons, all POSIX permission-bit or
  Linux-only-branch cases, each naming the missing capability.
- **L15** (review, measured): the independent verification of this branch found
  a regression the feature itself introduced — folding request paths onto a
  slash-based clean fixed the viewer's Windows 404s but let `/..\..\secret.txt`
  reach `filepath.Join` as one component and escape the library root. The
  request path is now slash-normalised *before* `path.Clean`, and
  `internal/delivery/web/web_traversal_test.go` pins both halves. The same
  review found `cmd/patro`'s fallback wizard still carrying its own copy of the
  executable-bit rule (rejecting every Windows path in an unbounded loop, now
  delegating to `setup.ValidateExecutable`) and the gray-zone call launching
  `pi` with a bare `-p`, which would have run with write and edit enabled; both
  now have regression tests.
- **L14** (note): `.gitignore`'s `knowledge/` entry also matches the tracked
  package `internal/domain/knowledge/`, so `git add` needs `-u` for files there.
  Pre-existing, unrelated to this feature, left alone.
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
- **L16** (review, native lifecycle `review-507153c520d2a8dc`, tier high, four lenses):
  **approved and acknowledged; authority burned.** Three CRITICAL findings — R1-001,
  R2-001 and R4-cmdquote-metachar — all named the same defect: `quoteForCmd`
  escaped embedded quotes as `\"`, but cmd.exe has no escape character and toggles
  its in-quote state at every quote, so at odd parity a later `&` fell outside the
  quotes and ran as a command. My own probe had only exercised an even number of
  interior quotes, which is why it looked safe. One bounded correction (90 diff
  lines, commit `8542143`) makes tokens safe instead of escaped. Seventeen
  non-blocking advisory findings remain as separate later work; none of them
  reopens this review.
