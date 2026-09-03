<p align="center">
  <a href="https://github.com/nxxxsooo/another">
    <img src="https://github.com/nxxxsooo/another/raw/main/docs/assets/banner.svg" alt="another — hop AI coding sessions between agents" width="100%" />
  </a>
</p>

<p align="center">
  <a href="https://github.com/nxxxsooo/another/actions/workflows/ci.yml"><img src="https://github.com/nxxxsooo/another/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/nxxxsooo/another/releases"><img src="https://img.shields.io/github/v/release/nxxxsooo/another?label=release" alt="Release" /></a>
  <a href="https://goreportcard.com/report/github.com/nxxxsooo/another"><img src="https://goreportcard.com/badge/github.com/nxxxsooo/another" alt="Go Report Card" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="MIT License" /></a>
</p>

<p align="center">
  <strong>Hop AI coding sessions between agents</strong> — browse, preview, migrate, and resume across Claude Code, Codex, Cursor, OpenCode, and more.
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#quick-start">Quick start</a> ·
  <a href="#tui">TUI</a> ·
  <a href="#providers">Providers</a> ·
  <a href="#cli">CLI</a>
</p>

---

## Why another?

You hit a rate limit mid-task, or you want a different model for the next step. **another** keeps your context: it reads sessions from one coding agent and writes them in another agent's native format so you can resume where you left off.

| | What you get |
|---|---|
| **Browse** | Unified, paginated session list across agents, filtered by **here** or **everywhere** |
| **Search** | Find words in titles and normalized user/assistant messages |
| **Preview** | Read the complete wrapped conversation before you migrate |
| **Migrate** | One command (or TUI flow) to hop a session to another provider |
| **Resume** | Copy or print the exact resume command for the target agent |
| **Fast** | Source-aware SQLite index at `~/.cache/another/index.db` — unchanged sessions are not reparsed |
| **Safe** | Snapshot dedup, atomic writes, destination verification, and exact rollback |

---

## Install

```bash
# Recommended: install script (linux / macOS)
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash

# From source
go install github.com/nxxxsooo/another/cmd/another@latest

# ...or clone and build
git clone https://github.com/nxxxsooo/another.git
cd another && make install
```

Requires **Go 1.22+** for building from source. The install script places `another` on your `PATH` (typically `~/.local/bin`).

---

## Quick start

**1. Open the TUI** (shows cached sessions instantly and refreshes metadata only when the index is stale):

```bash
another
```

**2. Or use the CLI:**

```bash
# Sessions for the current directory only (--cwd uses exact path match)
another list --cwd

# At ~, --cwd lists sessions under home projects (~/Documents/..., not global)

# All indexed sessions (default: no limit)
another list

# Cap output if you prefer
another list --limit 20

# Preview a session
another show <session-id> --limit 15

# Search titles and conversation text
another search "database timeout" --cwd

# Guided migration (both forms are equivalent)
another --migrate
another migrate

# Direct ctxmv-style migration and resume command
another <session-id> --from claude-code --to codex -y
another migrate <session-id> --from claude-code --to codex -y
another resume <session-id> --from claude-code --to codex

# Auto keeps every cleaned message when it fits, otherwise uses recent context.
another migrate <session-id> --to codex --context auto -y

# Force ctxmv-style full history, even when the destination may compact/reject it.
another migrate <session-id> --to codex --context full -y

# Always prefer a bounded recent working context.
another migrate <session-id> --to codex --context recent -y

# The unchanged source remains searchable/exportable in every mode.
another export <session-id> --provider cursor -o session.another.json
```

**3. Refresh the index** when you've created new sessions in your agents:

```bash
another index update          # incremental metadata + changed full text
another index rebuild         # transactional metadata rebuild + full text
another index update --metadata-only
another list --refresh        # rescan then list
```

> Full-text search stores normalized user/assistant conversation text locally in the private index. The cache directory is mode `0700` and SQLite files are mode `0600`.

---

## TUI

The default interface is a Codex-style **session browser**: one list for all agents, scoped to **here** by default.

```
   ╭──────◆──────╮
   │  another   │     here  everywhere
   ╰─────────────╯
  session browser    ~/projects/my-app

  3d ago  Fix auth bug          Claude Code · 67417609 · …/my-app
  1h ago  Refactor API           Codex · 8a2f1c3e · …/my-app
  …

  ↑↓ navigate · enter preview · / search · m migrate · w here · a everywhere
```

### Here vs everywhere

| Where you run `another` | **Here** (`w`) shows |
|--------------------------|----------------------|
| A project directory (e.g. `~/projects/my-app`) | Sessions whose `project_path` is **exactly** that folder — not subfolders like `my-app/web` |
| Home (`~`) | Sessions in projects **under** home (`~/Documents/...`, etc.) — excludes loose `~`-only tags |
| **Everywhere** (`a`) | All indexed sessions, any path |

`list --cwd` follows the same rules as **here** in the TUI.

| Key | Action |
|-----|--------|
| `Enter` | Preview the selected session |
| `/` | Search titles and user/assistant messages |
| `s` | Toggle subagent sessions |
| `w` / `a` | Toggle **here** (this folder) vs **everywhere** |
| `[` / `]` | Previous / next page (status shows `page N/M` when more sessions exist) |
| `p` | Filter by agent provider |
| `m` | Migrate selected session |
| `r` | Refresh index |
| `c` | Copy resume command (after migrate) |
| `Esc` | Back |
| `q` | Quit |

At 100 columns or wider, detail and migration views use split panes. Narrow terminals use a full-width drilldown; previews rewrap when the terminal is resized.

---

## Providers

| Agent | ID | Resume command |
|-------|-----|----------------|
| Claude Code | `claude-code` | `claude --resume <id>` |
| Codex | `codex` | `codex resume <id>` |
| Pi | `pi` | `pi --session <absolute-session-file>` |
| Cursor CLI | `cursor` | `cursor-agent --resume <id>` |
| OpenCode | `opencode` | `opencode --session <id>` |
| CommandCode | `commandcode` | `commandcode --resume <id>` |
| Hermes | `hermes` | `hermes --resume <id>` |

Check that agent data paths are discoverable:

```bash
another providers
another providers doctor
```

---

## CLI

```bash
another [<id> --to provider] [--migrate]
another list [--cwd] [--provider ID] [--include-subagents] [--limit N] [--refresh]
another search <keywords> [--cwd] [--provider ID] [--include-subagents] [--no-wait]
another show <id> [--provider ID] [--limit N]
another migrate [<id>] [--to provider] [--from ID] [--context auto|full|recent] [--dry-run] [-y]
another resume <id> --to <provider> [--from ID]
another index {status|rebuild|update} [--provider ID] [--metadata-only]
another export <id> -o session.another.json
another import session.another.json --to <provider> [--context auto|full|recent] [-y]
another providers [doctor]
another tui [--context auto|full|recent] # explicit TUI (default when no subcommand)
```

**Portable bundles** — export a session to JSON, import on another machine:

```bash
another export abc123 -o backup.another.json
another import backup.another.json --to codex -y
```

---

## Development

```bash
git clone https://github.com/nxxxsooo/another.git
cd another
make build test      # compile + unit tests
./scripts/smoke.sh   # integration smoke test
make install         # go install + copy to ~/.local/bin (on PATH)
```

---

## Limitations

- Cursor formats evolve frequently; another writes the native `store.db` graph, CLI `meta.json`, and transcript fallback, then verifies the conversation it can reload.
- The portable fidelity contract covers ordered user/assistant text and timestamps. Tool, reasoning, image, and system structures are best-effort because providers do not share equivalent formats.
- `--context full` preserves every cleaned user/assistant message in destination storage, but no migration tool can make a transcript larger than the destination model's context window simultaneously active. Codex may compact or reject oversized history.
- **Claude Code** resume may require `cd` to the original project directory.

---

## License

[MIT](LICENSE)
