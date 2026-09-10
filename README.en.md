<p align="center">
  <picture>
    <source media="(prefers-reduced-motion: reduce)" srcset="docs/assets/another-motion-static.jpg">
    <img src="docs/assets/another-motion.gif" width="100%" alt="another — a native coding-agent session manager; its purple and green mark briefly splits into neon channels before returning intact">
  </picture>
</p>

<p align="center">
  <a href="README.md">简体中文</a> · <strong>English</strong>
</p>

<p align="center">
  <a href="https://github.com/nxxxsooo/another/releases"><img src="https://img.shields.io/github/v/release/nxxxsooo/another?style=flat-square&color=6B50FF" alt="Latest release"></a>
  <a href="https://github.com/nxxxsooo/another/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/nxxxsooo/another/ci.yml?branch=main&style=flat-square&label=build&color=29D398" alt="Build status"></a>
</p>

<p align="center">
  Browse, manage, and resume native coding-agent sessions — or carry one into another agent without replacing it with a summary.
</p>

<p align="center">
  <img src="docs/assets/tui-preview.svg" width="100%" alt="another TUI showing sessions from several coding agents and the target picker">
</p>

You are deep in a session when the model runs out of quota, or the work turns
out to want a different one. The usual escape is to summarize what happened and
paste it somewhere else, which throws away the conversation and asks you to
re-explain it. `another` moves the session itself into the other agent's own
store, so you open it there and keep going.

## Features

- **Native sessions:** resumes in the target agent's own format — not a pasted summary.
- **Eleven agents:** Pi, Codex, Claude Code, Cursor, OpenCode, OpenCode 2, CommandCode, CodeM, Hermes, Qwen Code, and Antigravity.
- **One screen:** browse, search, preview, rename, archive, delete, relocate, and migrate without leaving the list.
- **Project-aware:** starts with the current Git project and combines sessions from its main worktree and every registered linked worktree; press `f` to see all projects, or `g` to group the list by worktree.
- **English or Chinese:** the interface follows your terminal's locale by default, or is pinned in setup; the title language is a separate setting.
- **Verified migration:** reloads every write, compares a content digest, rolls back on mismatch, and never mutates the source.
- **Local and fast:** reads native local stores; the private SQLite index under `~/.cache/another/` skips unchanged sessions on re-scan.

## Install

macOS and Linux:

```bash
# Homebrew
brew trust nxxxsooo/tap
brew install nxxxsooo/tap/another

# Install script
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash
```

Homebrew 6 requires third-party taps to be trusted before it will load them, and
refuses the install otherwise. Older versions load the tap directly and treat
`brew trust` as an unknown command; skip that line on those.

<details>
<summary><strong>Install fails with <code>undefined method 'run'</code></strong></summary>

The cask clears the download's quarantine attribute through `postflight_steps`,
which needs Homebrew 6.0.16 or newer. This error means the local Homebrew is
older:

```text
Error: Cask 'another' definition is invalid: undefined method 'run' for an instance of Homebrew::InstallSteps::DSL
```

Usually it is not a missing upgrade but a stuck one. If `brew update` has been
printing `Could not 'git stash' in /opt/homebrew`, it fetches the new version
and then aborts the merge, leaving the source behind — official casks fail the
same way with `undefined method 'command_wrapper'`. Check that
`/opt/homebrew` (`/usr/local/Homebrew` on Intel) holds no local changes worth
keeping, then reset and upgrade:

```bash
cd /opt/homebrew
git status
git reset --hard origin/HEAD
brew update
brew install nxxxsooo/tap/another
```

A tap you have already trusted stays trusted; no need to repeat `brew trust`.

</details>

<details>
<summary><strong>From source</strong></summary>

Go 1.24+:

```bash
go install github.com/nxxxsooo/another/cmd/another@latest
```

</details>

<details>
<summary><strong>Manual download</strong></summary>

Grab a `darwin`/`linux` `amd64`/`arm64` tarball from [Releases](https://github.com/nxxxsooo/another/releases), verify it against `checksums.txt`, and put the binary on your `PATH`:

```bash
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf another_*_darwin_arm64.tar.gz
install -m 755 another ~/.local/bin/another
```

</details>

With `~/.local/bin` or your Go bin directory on `PATH`, run:

```bash
another
```

The first run opens a Charmtone setup screen. The top of the first page picks the interface language with `←→`: **Auto** (the default, which follows the terminal's locale), **English**, or **中文**. The page redraws as you press, so a wrong choice is visible immediately instead of after saving. Use `↑↓` to move, `Space` to enable or disable an agent, and `Shift+↑↓` to order agents across the source picker, target picker, and `providers` output, then press `Enter` to continue. The page lists the six continuously tested agents; the four compatibility adapters sit behind one folded row that `Space` opens. The fold starts open when the saved configuration already enables one of them, because a setting you cannot see is a setting you cannot turn off. The second page picks an optional agent for AI title suggestions; it defaults to off. When OpenCode 2 is one of your agents, that page also carries an `OpenCode 2 title plugin` row: `t` turns it on, and only then does another write the plugin into OpenCode 2's configuration directory. The row names that directory and what is in it already. Run `another setup` any time to change any of these choices.

### Update

```bash
brew upgrade another                     # Homebrew
curl -fsSL https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.sh | bash   # script
go install github.com/nxxxsooo/another/cmd/another@latest                                      # source
```

Outside Homebrew, updates are manual; installed binaries do not follow the repository.

## Use the TUI

```text
Enter     resume the selected session in its native agent
→         migrate the selected session to another agent
←         choose a source agent
↑ / ↓     move through sessions or picker items
Space     preview the conversation
f         switch between the current project and all projects
Ctrl+R    rename in the source agent's native title store
Tab       accept the AI title suggestion, when one is configured and arrives
m         fork or move the session into another project directory
a         archive; press a again for one-step undo
x / X     mark the row under the cursor / mark or clear the whole page
Ctrl+D    delete after an explicit confirmation
u         undo that delete, where the agent's session is a file another can put back
/         search titles and normalized conversation text
r         refresh the local index
Esc       close a picker or dismiss transient state
q         quit
```

The interface language and the title language are separate settings, and their `auto` means different things: the interface follows the terminal's locale (`LC_ALL` → `LC_MESSAGES` → `LANG`, where a `zh` tag selects Chinese and everything else selects English), while a title follows the session it names. The CLI's `--help`, flag descriptions, and error messages stay English. The bridge sentence written into Pi when a migrated conversation ends on an assistant turn follows the interface language, and both wordings are recognized and stripped when the session is read back.

While the TUI runs it sets the terminal title to `another`, or `another setup` on the setup screen. On exit the title goes back to the working directory, and handing the terminal to another agent names that agent instead, so the tab always says what is actually running in it.

On macOS, opening the TUI temporarily selects the current ASCII-capable keyboard layout so letter shortcuts are not intercepted by a Pinyin IME. The input source active before launch is restored on exit or before handing the terminal to the target agent. Linux input sources are left untouched.

Migration shows the exact resume command first. Press `Enter` to hand the terminal to the target agent, `c` to copy the command, or `Esc` to keep browsing.

The TUI starts scoped to the current project. A Git repository's main worktree, every registered linked worktree, and their subdirectories form one project; outside Git, the scope is an exact current-directory match. The header always shows the active scope, and search keeps that scope. An empty project view stays empty rather than silently switching global; press `f` to view all projects.

One project is not one directory. When the listed sessions really do span several — worktrees, a monorepo's subtrees — a directory column appears: a chip in the path's own color, like the agent's, carrying the part of the path below the project root (`.worktrees/delete-undo`, `packages/api`, and the root's own name for a session that started there) so the shared prefix never costs the title its width. The column is only as wide as the longest path it has to show; the rest goes to the title. A directory that no longer exists keeps its path and loses its color. With every session in one directory the column stays away.

Press `g` to group that list by tree. Each worktree gets a heading in its own color, carrying the tree's name and how many sessions it holds, with its sessions beneath it; a session recorded in a subdirectory counts for the tree it sits in rather than starting a group of its own. Trees are ordered by their newest session, so the work you just left is still at the top, and inside a tree the order stays by recency. The cursor never rests on a heading, and grouping is a view of the page already loaded — no reload, and the session under the cursor stays under it. Grouped, the directory column drops what the heading already said and keeps only what it did not, such as the `internal/tui` a session actually ran in. A list holding one tree is left ungrouped.

## Relocate

Same agent, different working directory: a worktree you just created, a repository that moved, or work that belonged in the project next door all along. Press `m`, type the target directory, and `Tab` chooses between two readings:

- **Fork** (the default): the original stays where it is, and the target directory gains a session you can continue.
- **Move:** the session itself changes directory, and the old one no longer has it.

This is not a migration. Migration rewrites the conversation through a portable format, and tool calls and reasoning are dropped in that step. Relocation runs each agent's own native operation and keeps the content intact: OpenCode 2 calls the official `fork` and `move` endpoints, and Pi copies its own session file line for line, rewriting only the `id` and `cwd` in the header. An agent with no verified native contract reports the action as unsupported rather than passing a rewrite off as a move.

Every relocation is read back before it is reported: OpenCode 2 compares the directory in the session's own row, Pi compares a content digest. Move deletes the original only after the new file verifies, and a fork that cannot be created leaves nothing behind in the source directory. The target directory has to exist — a mistyped path should fail immediately instead of producing a session that points at nothing.

## Agents

OpenCode and OpenCode 2 are deliberately separate. They use different commands, databases, schemas, and service lifecycles.

The continuously tested set is **Pi, OpenCode 2, Claude Code, Codex, Antigravity, and Qwen Code**; these six enter every release regression pass. Cursor, OpenCode, CommandCode, CodeM, and Hermes remain compatibility adapters, but are not promised an end-to-end maintainer test on every release. First-run setup does not auto-select agents from detected binaries or stale local data; the user chooses what another indexes and exposes.

The session list marks each agent with a fixed-width color chip. Agent names differ by up to nine characters, and set as words they leave a ragged column where a short name reads as a lesser agent and every row starts its title somewhere else. The source and target pickers show the chip next to the full name, which is where this table is read from.

| Agent | Provider ID | List tag | Native resume | Rename | Archive | Relocate | Delete |
|---|---|:---:|---|:---:|:---:|:---:|:---:|
| Pi | `pi` | `PI` | `pi --session <file>` | ✓ | — | ✓ | ✓ |
| Codex | `codex` | `CDX` | `codex resume <id>` | ✓ | ✓ | — | ✓ |
| Claude Code | `claude-code` | `CLA` | `claude --resume <id>` | ✓ | — | — | ✓ |
| Cursor | `cursor` | `CUR` | `cursor-agent --resume <id>` | — | — | — | ✓ |
| OpenCode | `opencode` | `OPC` | `opencode --session <id>` | ✓ | ✓ | — | ✓ |
| OpenCode 2 | `opencode2` | `OC2` | `opencode2 --session <id>` | ✓ | — | ✓ | ✓ |
| CommandCode | `commandcode` | `CMD` | `commandcode --resume <id>` | — | — | — | ✓ |
| CodeM | `codem` | `CDM` | `codem --resume <id>` | — | — | — | ✓ |
| Hermes | `hermes` | `HRM` | `hermes --resume <id>` | — | ✓ | — | ✓ |
| Qwen Code | `qwen` | `QWN` | `qwen --resume <id>` | ✓ | ✓ | — | ✓ |
| Antigravity | `agy` | `AGY` | `agy --conversation <id>` | ✓ | — | — | ✓ |

A dash means that agent has no verified native contract for the operation. Rename, archive, relocate, and delete change the corresponding agent's native state rather than an another-only marker; `another` shows only operations the selected agent actually supports and does not keep private state that disappears on refresh.

Per-agent behavior worth knowing before you rely on it — which deletes can be taken back, Codex's three title stores and its subagent threads, child sessions, and sessions whose directory is gone — is in [`docs/providers.md`](docs/providers.md).

Check the local installation:

```bash
another providers
another providers doctor
```

## AI title suggestions

When setup names an agent, `Ctrl+R` opens the rename box on the original title and asks that agent, in the background, for one title using another's own fixed contract: `MMDD｜类型｜主题` in Chinese or `MMDD｜Type｜Topic` in English. It does not depend on a Skill. The date comes from the indexed creation time converted to `Asia/Shanghai`, never from the model. Suggestions that miss the contract, arrive after the box closes, or fail outright are discarded without touching what you typed. The agent runs in a throwaway directory, so your project's own instructions never reach it.

The second setup page picks the agent and the language; `Enter` opens a third page for the model. That list comes from the agent's own CLI (`pi --list-models`, `agy models`, `opencode models`, `opencode2 models`), typing filters it, the first row leaves the choice to the CLI, and the last row still accepts a name typed by hand. Claude Code has no listing subcommand, but its headless control protocol answers `list_models`, so another asks that one question: no prompt, no model call, and the answer is the same catalog its own `/model` panel shows. Codex and Qwen expose nothing to ask, so they go straight to typing with the reason shown — a guessed list of model IDs would only fail later, at rename time.

The second setup page picks the title language with `←→`, independently of the interface language on page one: **Auto** (default), **English**, or **中文**. Auto uses Chinese when the first meaningful user message contains a Han character and English otherwise. The date and `｜` separator are identical in every language, and the eight types map one to one: 功能/Feature, 设计/Design, 修复/Fix, 优化/Optimize, 发布/Release, 探索/Explore, 文档/Docs, 研究/Research.

For more than one title at a time, mark sessions with `x` (`X` toggles the whole page) and press `Ctrl+T`. A row that fails transiently — a timeout, a rate limit, a CLI that died once — is retried once after two seconds; a missing CLI, an agent that cannot generate titles, or a session without a creation date fails straight to the review page, because a second attempt would print the same line. On the review page `r` re-runs the rows that failed or were cut short by `esc`, keeping the suggestions that already landed. Rows that fail during apply keep their marks, so `Ctrl+T` retries exactly those.

The batch header names the agent, model, and language this run uses, and `m` opens the same picker setup uses.

Generating titles leaves a trace of its own: Codex, Antigravity, and OpenCode record a session for every headless run with no way to opt out. Claude Code offers one, so another uses it: `--no-session-persistence` means the run is never written down at all, which beats recognizing the leftover afterwards (that run also passes `--disable-slash-commands`, because session content is untrusted input and has no business invoking a command or a skill). another recognizes the leftovers the other agents still write and keeps them out of the index, along with the ones older builds of Claude Code left behind — the prompt's fixed first line, or the `another-titler-*` throwaway directory the run happened in, identifies them. Antigravity evades both, because it names the run after the answer, which leaves the leftover wearing a title that satisfies another's own naming contract, and it records no working directory at all; sessions short enough to be a leftover are therefore opened and matched against the prompt itself, while longer ones are never read. Leftovers an older build already indexed are evicted too. Only another's index is touched; the agent's own session files stay where that agent put them. Once generation finishes or is cancelled, `m` swaps the model for this batch only — empty means that CLI's default — and `Enter` re-runs the same sessions on it. The override is never written back to config: a cheap model for forty old sessions should not become the default for the next single rename.

OpenCode 2 can enforce the same contract during its native first-title request without a second model call. That adapter ships inside the another binary: turn on the `OpenCode 2 title plugin` row on the second setup page, or run `another integrations install`, and another writes the plugin into OpenCode 2's `plugins/another-title-policy/` and records what it wrote. It writes that one directory and never edits your `opencode.json(c)`, because OpenCode 2 discovers the directory on its own. After upgrading another, `another integrations status` reports whether the installed plugin is behind and another setup brings it back in line; files you have edited are never overwritten. Source and details are under [`integrations/opencode2-title-policy/`](integrations/opencode2-title-policy/). Pi works the same way: another ships an extension of its own, turned on by the `Pi title extension` row on the second setup page or by `another integrations install pi`, and written into Pi's `extensions/another-title-policy/` — Pi discovers that directory itself, and another never edits your `settings.json`. The extension generates no titles: when a turn settles (`agent_settled`) it calls `another rename --auto`, so the policy, the language, and the title agent all stay in the binary. Source is under [`integrations/pi-title-policy/`](integrations/pi-title-policy/). Claude Code exposes no title agent to override and no "session was named" event, so it can only be named after the session ends: the SessionEnd hook under [`integrations/claude-code-title-hook/`](integrations/claude-code-title-hook/) calls `another rename --auto` and writes Claude Code's own `custom-title` record, so the name also shows in its own session list. A session titled by hand is skipped and never overwritten; this one is installed by hand. Codex has no stable native title-policy surface yet and remains managed through another.

## CLI

```bash
# Browse and search
another list [--provider ID] [--project PATH] [--cwd] [--limit N] [--json] [--refresh]
another search "query" [--provider ID] [--project PATH] [--cwd] [--limit N] [--json]
another show <session-id> [--provider ID] [--limit N]

# Move a session
another migrate <session-id> --to <provider> [--from ID] [-y]
another migrate <session-id> --to codex --context full -y
another resume <session-id> --to <provider> [--from ID]

# Relocate (same agent, another project directory)
another relocate <session-id> --to-dir <path> [--from ID] [--dry-run] [-y]
another relocate <session-id> --to-dir ../feature-worktree --move -y

# Rename (written to the agent's own title store)
another rename <session-id> --title "0908|Feature|Title policy" [--from ID]
another rename <session-id> --auto [--dry-run]   # ask the configured title agent

# Portable backup
another export <session-id> -o session.another.json
another import session.another.json --to <provider> [--context MODE] [--dry-run] -y

# Configuration and index
another setup
another integrations                      # adapter status
another integrations install [agent...]  # install or update adapters; no agent means all
another integrations remove [agent...]
another index update
another index rebuild

# When a project moves
another paths
another paths link <old-directory> <new-directory>
another paths unlink <old-directory>
```

A session belongs to the directory it **started** in. Agents record their working directory on every turn, so moving into a subdirectory, a temporary path, or another repository mid-session does not change the owner; project filtering then covers a directory together with everything below it, and every registered worktree of a Git repository.

After a project is renamed or relocated, its earlier sessions still point at the directory the agent recorded. `another paths` lists those directories with checkable candidates — a live path that shares a provider storage folder with the missing one, or an existing directory ending in the same path segments — and `another paths link` records your decision and re-projects the index. The directory the agent wrote is kept as evidence, so `another paths unlink` restores it.

`--json` makes `list` and `search` emit machine-readable records. Child agent sessions are hidden by default; `--include-subagents` includes them.

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
- The delete confirmation states whether that agent's delete can be undone, and an undo is offered only where the same session comes back — never as a re-rendered copy.
- Relocation opens on fork every time; move is an explicit choice, and it deletes the original only after the new location verifies.
- Exactly identified active sessions are protected from rename, archive, relocate, and delete.
- The configuration directory is mode `0700`; configuration and SQLite index files are mode `0600`.
- Disabling an agent in setup removes only its local index rows, never its native sessions.
- Title suggestions borrow an agent CLI you already authenticated; `another` stores no API keys of its own.
- A suggestion is only ever a suggestion: it is shown next to the rename field, and the rename still needs `Tab` and `Enter`.

## Development

```bash
git clone https://github.com/nxxxsooo/another.git
cd another
make build
go test ./...
go test -race ./...
go vet ./...
golangci-lint run ./...   # brew install golangci-lint
```

Contributor guides:

- [`CONTRIBUTING.md`](CONTRIBUTING.md) — the development loop, what must pass, and what a pull request should carry.
- [`docs/architecture.md`](docs/architecture.md) — layout, data flow, index schema, digests, and the safety rules.
- [`docs/adding-a-provider.md`](docs/adding-a-provider.md) — how to adapt one more agent.
- [`docs/providers.md`](docs/providers.md) — per-agent behavior behind the capability table.

Regenerate artwork. The rasterized wordmark is checked in, so an ordinary build
never needs a font installed:

```bash
./scripts/render-readme-assets.py                                  # TUI preview SVG
python3 ./scripts/render-motion-banner.py                          # identity banner; Pillow and ffmpeg
python3 ./scripts/render-goodbye-gif.py                            # goodbye GIF; Pillow
python3 ./scripts/render-logo-face.py > internal/tui/logo_face.go  # wordmark; Pillow and JetBrains Mono ExtraBold
python3 ./scripts/render-mark-transparent.py                       # transparent mark, derived from the master; Pillow
```

## The name

The literal meaning comes first: keep the session, continue in **another** agent.

The name is also a quiet nod to [*Another*](https://www.pa-works.jp/works/another/), the 2012 mystery-horror anime from P.A.WORKS based on Yukito Ayatsuji's novel. Its idea of an extra presence whose identity is hard to distinguish became a secondary metaphor for migrated sessions: the same conversational identity appears somewhere else, in another native form. This is conceptual inspiration, not an affiliation or a visual adaptation; the project does not reuse the series' logo, characters, or artwork.

## Acknowledgements

`another` began as a fork of [CyrusSE/agenthop](https://github.com/CyrusSE/agenthop) and is distributed under the MIT License. It now has its own module path, provider contracts, TUI, setup flow, and release surface.

## License

[MIT](LICENSE)

<p align="center">
  <picture>
    <source media="(prefers-reduced-motion: reduce)" srcset="docs/assets/tui-goodbye-static.png">
    <img src="docs/assets/tui-goodbye.gif" width="500" alt="the another wordmark printed on exit; two copies of the mark, one violet and one mint, close on each other from either side, tearing into offset horizontal bands on the way in as both colours give way to the intersection cyan they share, then meeting as a single cyan wordmark">
  </picture>
</p>

<p align="center">
  <sub>Quitting leaves this in your scrollback. Handing the terminal to another agent does not.<br>
  <code>ANOTHER_NO_MOTION=1</code>, <code>NO_COLOR</code>, <code>CI</code>, and pipes get the still frame.</sub>
</p>
