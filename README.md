<p align="center">
  <picture>
    <source media="(prefers-reduced-motion: reduce)" srcset="docs/assets/another-motion-static.jpg">
    <img src="docs/assets/another-motion.gif" width="100%" alt="another — a native coding-agent session manager; its purple and green mark briefly splits into neon channels before returning intact">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/nxxxsooo/another/releases"><img src="https://img.shields.io/github/v/release/nxxxsooo/another?style=flat-square&color=6B50FF" alt="Latest release"></a>
  <a href="https://github.com/nxxxsooo/another/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/nxxxsooo/another/ci.yml?branch=main&style=flat-square&label=build&color=29D398" alt="Build status"></a>
</p>

<p align="center">
  Browse, manage, and resume native coding-agent sessions — or carry one into another agent without replacing it with a summary.<br>
  在多个 coding agent 之间浏览、管理、迁移并继续真实会话，而不是粘贴一份摘要。
</p>

<p align="center">
  <img src="docs/assets/tui-preview.svg" width="100%" alt="another TUI showing sessions from several coding agents and the target picker">
</p>

## Features

- **Native sessions:** resumes in the target agent's own format — not a pasted summary.
- **Eight agents:** Pi, Codex, Claude Code, Cursor, OpenCode, OpenCode 2, CommandCode, and Hermes.
- **One keyboard:** `Enter` resumes, `→` migrates, `Space` previews, `Ctrl+R` renames, `A` archives, and `Ctrl+D` deletes.
- **Verified migration:** reloads every write, compares a content digest, and rolls back on mismatch.
- **Real session management:** search, rename, archive, delete, export, and import from one local browser.
- **Local by default:** reads native local stores; the private search index stays under `~/.cache/another/`.
- **Fast after first scan:** SQLite metadata and FTS indexes avoid reparsing unchanged sessions.

## Install

macOS and Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash
```

Or install from source with Go 1.24+:

```bash
go install github.com/nxxxsooo/another/cmd/another@latest
```

Make sure `~/.local/bin` or your Go bin directory is on `PATH`, then run:

```bash
another
```

The first run opens a Charmtone setup screen. Use `↑↓` to move, `Space` to select the agents you use, and `Enter` to continue. The second page picks an optional agent for AI title suggestions; it defaults to off. Run `another setup` any time to change either choice.

### Update

Re-run the install command:

```bash
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash
```

Source installs update with:

```bash
go install github.com/nxxxsooo/another/cmd/another@latest
```

Updates are manual; installed binaries do not follow the repository automatically.

## Use the TUI

```text
Enter     resume the selected session in its native agent
→         migrate the selected session to another agent
←         choose a source agent
↑ / ↓     move through sessions or picker items
Space     preview the conversation
Ctrl+R    rename in the source agent's native title store
Tab       accept the AI title suggestion, when one is configured and arrives
A         archive; press A again for one-step undo
Ctrl+D    permanently delete after an explicit confirmation
/         search titles and normalized conversation text
r         refresh the local index
Esc       close a picker or dismiss transient state
q         quit
```

Migration shows the exact resume command first. Press `Enter` to hand the terminal to the target agent, `c` to copy the command, or `Esc` to keep browsing.

Quitting leaves the wordmark in your scrollback. Handing the terminal to another agent does not — that would be noise in front of the agent about to start.

<p align="center">
  <picture>
    <source media="(prefers-reduced-motion: reduce)" srcset="docs/assets/tui-goodbye-static.png">
    <img src="docs/assets/tui-goodbye.gif" width="500" alt="the another wordmark printed on exit; its upper half slips two cells and turns magenta, then returns to violet and to register">
  </picture>
</p>

Set `ANOTHER_NO_MOTION=1` for the still frame. Pipes, `NO_COLOR`, `CI`, and terminals too short to redraw in already get it.

## Agents

OpenCode and OpenCode 2 are deliberately separate. They use different commands, databases, schemas, and service lifecycles.

| Agent | Provider ID | Native resume | Rename | Archive | Delete |
|---|---|---|:---:|:---:|:---:|
| Pi | `pi` | `pi --session <file>` | ✓ | — | ✓ |
| Codex | `codex` | `codex resume <id>` | ✓ | ✓ | ✓ |
| Claude Code | `claude-code` | `claude --resume <id>` | ✓ | — | ✓ |
| Cursor | `cursor` | `cursor-agent --resume <id>` | — | — | ✓ |
| OpenCode | `opencode` | `opencode --session <id>` | ✓ | ✓ | ✓ |
| OpenCode 2 | `opencode2` | `opencode2 --session <id>` | ✓ | — | ✓ |
| CommandCode | `commandcode` | `commandcode --resume <id>` | — | — | ✓ |
| Hermes | `hermes` | `hermes --resume <id>` | — | ✓ | ✓ |

A dash means that agent has no verified native contract for the operation. `another` reports the limitation instead of storing a private state that disappears on refresh.

Check the local installation:

```bash
another providers
another providers doctor
```

## CLI

```bash
# Browse and search
another list [--provider ID] [--cwd] [--limit N] [--refresh]
another search "query" [--provider ID] [--cwd]
another show <session-id> [--provider ID] [--limit N]

# Move a session
another migrate <session-id> --to <provider> [--from ID] [-y]
another migrate <session-id> --to codex --context full -y
another resume <session-id> --to <provider> [--from ID]

# Portable backup
another export <session-id> -o session.another.json
another import session.another.json --to <provider> -y

# Configuration and index
another setup
another index update
another index rebuild
```

Context modes:

- `auto` keeps all cleaned turns when they fit and otherwise selects recent working context;
- `full` preserves every cleaned user/assistant turn even when the destination may compact or reject it;
- `recent` always creates a bounded recent-context view.

## What crosses the boundary

`another` preserves ordered user and assistant text, timestamps when the target supports them, the project directory, title, and a migration marker used for deduplication and verification.

Provider-specific reasoning signatures, tool calls, tool results, images, and system records do not have portable equivalents and are excluded. The original source session is untouched by migration.

OpenCode and OpenCode 2 writes use their official import/API surfaces. Codex Desktop titles come from its GUI title index rather than guessed injected messages. Pi writes complete assistant records with explicit transport metadata and zero-valued usage for reconstructed history.

## Safety

- Every migrated target is reloaded and content-verified before success is reported.
- Failed verification removes only the artifact created by that migration.
- `Ctrl+D` defaults to **Cancel** and shows the provider, title, project, and full session ID.
- Exactly identified active sessions are protected from rename, archive, and delete.
- The configuration directory is mode `0700`; configuration and SQLite index files are mode `0600`.
- Disabling an agent in setup removes only its local index rows, never its native sessions.
- Title suggestions borrow an agent CLI you already authenticated; `another` stores no API keys of its own.
- A suggestion is only ever a suggestion: it is shown next to the rename field, and the rename still needs `Tab` and `Enter`.

### AI title suggestions

When setup names an agent, `Ctrl+R` opens the rename box on the original title and asks that agent, in the background, for one title shaped `MMDD｜类型｜主题`, the contract from the `title-formatter` skill. The date comes from the indexed creation time converted to `Asia/Shanghai`, never from the model. Suggestions that miss the contract, arrive after the box closes, or fail outright are discarded without touching what you typed. The agent runs in a throwaway directory, so your project's own instructions never reach it.

## Development

```bash
git clone https://github.com/nxxxsooo/another.git
cd another
make build
go test ./...
go test -race ./...
go vet ./...
```

Regenerate README artwork:

```bash
./scripts/render-readme-assets.py
python3 ./scripts/render-motion-banner.py  # requires Pillow and ffmpeg
python3 ./scripts/render-goodbye-gif.py    # requires Pillow
```

Regenerate the TUI wordmark itself only when its typeface or size changes; the
rasterized face is checked in, so building never needs the font:

```bash
python3 ./scripts/render-logo-face.py > internal/tui/logo_face.go  # requires Pillow and JetBrains Mono ExtraBold
```

## The name

The literal meaning comes first: keep the session, continue in **another** agent.

The name is also a quiet nod to [*Another*](https://www.pa-works.jp/works/another/), the 2012 mystery-horror anime from P.A.WORKS based on Yukito Ayatsuji's novel. Its idea of an extra presence whose identity is hard to distinguish became a secondary metaphor for migrated sessions: the same conversational identity appears somewhere else, in another native form. This is conceptual inspiration, not an affiliation or a visual adaptation; the project does not reuse the series' logo, characters, or artwork.

## Acknowledgements

`another` began as a fork of [CyrusSE/agenthop](https://github.com/CyrusSE/agenthop) and is distributed under the MIT License. It now has its own module path, provider contracts, TUI, setup flow, and release surface.

## License

[MIT](LICENSE)
