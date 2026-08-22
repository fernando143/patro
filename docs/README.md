# Patro documentation

Patro turns meeting recordings into a local, searchable Markdown knowledge
library.

## Quick path

1. Install Patro with Homebrew or build it from source.
2. Run `patro init` to configure the inbox, library, API key, and analyzer.
3. Drop a recording into the configured inbox.

For complete installation, configuration, and usage instructions, see the
[repository README](https://github.com/fernando143/patro#readme).

## Preview this documentation locally

Install the Docsify CLI once:

```bash
npm install --global docsify-cli@5
```

From the repository root, start the local server:

```bash
docsify serve docs
```

Then open <http://localhost:3000>. Docsify reloads the site when Markdown files
change. Stop the server with `Ctrl+C`.

## Runbooks

- [Search architecture](search-architecture.md)
- [QA runbook: `patro regenerate`](regenerate-qa-runbook.md)
