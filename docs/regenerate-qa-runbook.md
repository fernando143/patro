# QA runbook: `patro regenerate`

Manual test plan for the `patro regenerate <transcript-file>` subcommand
(PR: `feat/regenerate-transcript-cli`) before merging to `main`.

## Isolation

Use the repo's own `knowledge/` directory, not `~/Documents/knowledge` or
any other real library. `knowledge/` is gitignored, so nothing written
here ends up in git.

Do **not** reuse the repo's real `config.yaml` — it points `library` at
your actual personal knowledge base. Instead create a separate,
untracked QA config:

```bash
cat > config.qa.yaml <<'EOF'
library: ./knowledge
analyzer_backend: kimi
EOF
```

`config.qa.yaml` is gitignored (see `.gitignore`); never remove that
entry or `git add -f` this file.

## Build

```bash
git checkout feat/regenerate-transcript-cli
go build -o patro ./cmd/patro
```

`./patro` is gitignored (`/patro` in `.gitignore`).

## Scenario A — external transcript, first run, no `--date`

Must fail cleanly and write nothing.

```bash
echo "Speaker A: hablamos del roadmap Q3." > /tmp/fixture.txt
./patro regenerate /tmp/fixture.txt --mock --config config.qa.yaml
```

Expected:
- non-zero exit code
- stderr asks for `--date`
- `knowledge/meetings/` and `knowledge/transcripts/` stay empty (or absent)

## Scenario B — external transcript with `--date` (happy path)

```bash
./patro regenerate /tmp/fixture.txt --date 2026-08-01 --mock --config config.qa.yaml
```

Verify:
- `knowledge/meetings/2026-08-01-*.md` was created
- `knowledge/transcripts/<sha256-id>.txt` exists and matches `/tmp/fixture.txt`
- `knowledge/topics/` is still empty or absent — **this is the hard
  invariant of the whole change**

```bash
ls knowledge/meetings knowledge/transcripts
ls knowledge/topics 2>/dev/null || echo "topics/ absent, as expected"
```

## Scenario C — regenerate the same transcript (overwrite in place)

Run the same command again, this time **without** `--date`:

```bash
./patro regenerate /tmp/fixture.txt --mock --config config.qa.yaml
```

Expected:
- the command auto-detects the original date from the existing note
  and overwrites it in place
- `ls knowledge/meetings/` still shows **exactly one** file — no
  duplicate/orphaned note, even if the mock analyzer's title changed

```bash
ls knowledge/meetings | wc -l   # must stay 1
```

## Scenario D — `--date` rejected outside `regenerate`

```bash
./patro process /tmp/fixture.txt --date 2026-08-01 --config config.qa.yaml
```

Expected: `patro: --date is only valid with regenerate`, exit code 2.

## Optional — real analyzer backend

Drop `--mock` to exercise a real configured CLI (`kimi`, `claude`, or
`codex` must be installed and match `config.qa.yaml`'s
`analyzer_backend`). This makes a real subprocess call and takes
longer.

## Cleanup

```bash
rm -rf knowledge config.qa.yaml patro /tmp/fixture.txt
```

## Pass criteria

- [ ] Scenario A fails with a clear error, no files written
- [ ] Scenario B writes exactly one meeting note + one transcript copy
- [ ] Scenario C overwrites the same file, never creates a second one
- [ ] Scenario D is rejected with the documented error
- [ ] `knowledge/topics/` and `knowledge/index.md` are never created or
      modified in any scenario
