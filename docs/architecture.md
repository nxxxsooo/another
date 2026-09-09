# Architecture

`another` is a local Go program with two entry points, a Bubble Tea TUI and a Cobra CLI, sharing one core: a provider registry, a SQLite index, and a migration engine. It reads each coding agent's native session store, keeps a private, rebuildable index of what it found, and writes back only through contracts the agent itself honors.

## Layout

| Path | Responsibility |
|---|---|
| `cmd/another` | Binary entry. Builds the app and dispatches to the CLI or the TUI. |
| `cmd/auditproviders` | Maintainer tool that exercises every adapter against the real local stores. |
| `internal/cli` | Cobra commands: `list`, `show`, `search`, `migrate`, `resume`, `relocate`, `rename`, `export`, `import`, `index`, `paths`, `providers`, `integrations`, `setup`, `tui`. `App` is the composition root. |
| `internal/tui` | The session browser: list, pickers, preview, rename, batch titles, relocate, archive, delete, setup. Split by responsibility: `tui.go` (model, run loop), `tui_cmds.go` (async messages and the commands that produce them), `tui_update.go` (one handler per message), `tui_view.go`, `tui_list.go`, `tui_text.go`, `tui_batch*.go`, `setup*.go`. |
| `internal/provider` | The provider interface, optional capability contracts, shared errors, and the current-session check. |
| `internal/providers/<id>` | One adapter per agent. Owns that agent's paths, storage format, resume command, and whichever native lifecycle operations it has verified. |
| `internal/registry` | Constructs the adapter set, normalizes provider IDs, knows each agent's CLI binary and which agents are compatibility adapters. |
| `internal/model` | `Conversation`, `Message`, `Summary`, `MigrationMeta`, and the digests. |
| `internal/index` | SQLite store: discovery reconciliation, FTS5 search, incremental refresh, path aliases, migration dedup records. |
| `internal/migrate` | The migration engine: context modes, projection, write, verify, rollback, dedup. |
| `internal/titler` | AI title suggestions through an installed agent CLI, single and batch. |
| `internal/integrations` | Adapters another installs into other agents: the OpenCode 2 title plugin and the Pi title extension, behind one shared install, compare, and remove path. |
| `internal/config` | Settings file, paths, atomic writes, permissions. |
| `internal/i18n` | English and Chinese strings for the TUI. |
| `internal/util` | Paths, project scope and nested-repo detection, JSONL scanning, title picking, display helpers. |
| `integrations/` | Sources shipped to other agents: the OpenCode 2 title plugin, the Pi title extension, the Claude Code SessionEnd hook. |
| `docs/assets/`, `scripts/` | Generated artwork and the scripts that regenerate it, plus the install script. |

## Data flow

1. **Discover.** Each enabled provider scans its own storage and returns lightweight `Summary` records: ID, title, timestamps, message count, project directory, storage path, source fingerprint. Discovery honors the index's skip callbacks so unchanged files are never re-read.
2. **Index.** `index.UpdateIncremental` reconciles those summaries into the SQLite store: `session_sources` holds every physical representation, `sessions` the canonical row per `(provider, id)`, and `source_files` the per-file fingerprint. A session belongs to the directory it started in.
3. **Search.** Changed sessions are loaded once and their normalized user and assistant text goes into `session_fts`. Search and list read the index only; nothing scans live.
4. **Load.** When a person picks a session, the provider parses the full `Conversation` from its native file or database.
5. **Write.** For a migration, the engine projects the conversation by context mode, embeds a `MigrationMeta` marker, and the target provider serializes it atomically in its own format.
6. **Verify.** The engine reloads exactly what it wrote and compares `ContentDigest`. On mismatch it calls the provider's `CleanupWrite` to remove that artifact and reports failure. The source is never modified.
7. **Dedup.** The `(target, digest)` pair is recorded in `migration_dedup`, so the same source and context mode is not migrated twice, and `another resume` can find the target later.

Rename, archive, delete, and relocate follow a shorter loop: the provider changes its own native state, and the index is refreshed for that provider so the list matches what the agent now shows.

## Index schema

| Table | Purpose |
|---|---|
| `sessions` | Canonical session per `(provider, id)`, including `kind` (`root` or `subagent`) and `parent_id`. |
| `session_sources` | Every physical file or row that represents a session, with its fingerprint and priority. |
| `source_files` | Per-file mtime and size, so a session spread across several files is refreshed correctly. |
| `session_fts` | FTS5 over title and normalized message text. |
| `content_index` | Per-session content fingerprint and `ready` / `pending` / `error` state. |
| `migration_dedup` | Verified target for a source snapshot and context mode, with `origin_id` and `origin_source`. |
| `meta` | Last update and rebuild times, per-provider discover counts, attribution rule versions. |

The index lives at `~/.cache/another/index.db` with `0700` on the directory and `0600` on the files. It is a cache: `another index rebuild` recreates it from the agents' own stores, and disabling an agent in setup prunes its rows without touching anything native.

## Digests

- `ContentDigest(conv)` hashes ordered user and assistant text with millisecond-truncated timestamps. It is provider-agnostic and is what write verification compares.
- `SnapshotDigest(conv)` adds the source provider and ID, so identical text from two sources does not collide.
- `MigrationContextDigest(conv, mode)` adds the context mode, so `auto`, `full`, and `recent` migrations of one source are distinct targets.

Migrations record the context-mode digest. A lookup that has not chosen a mode, such as `another resume`, tries the snapshot digest and every context-mode variant.

## Provider contracts

Every adapter implements `provider.Provider`: `ID`, `DisplayName`, `DefaultPaths`, `Installed`, `Discover`, `Load`, `Write`, `SupportsResume`, `ResumeCommand`.

Everything else is optional and means the adapter has verified the agent's own behavior:

| Contract | Meaning |
|---|---|
| `PreviewLoader` | Load a bounded tail of a large session for the preview pane. |
| `ResumeEnsurer` | Register a written session where the agent's resume command looks for it. |
| `WriteCleaner` | Remove exactly the artifact a failed write produced. |
| `SessionRenamer` | Write a title into the agent's own title store. |
| `SessionArchiver` | Toggle the agent's own reversible archived state. |
| `SessionDeleter` | Delete the agent's own artifacts for a session. |
| `ReversibleSessionDeleter` | Offer undo because the agent's session is bytes another can put back unchanged. |
| `SessionRelocator` | Fork or move a session into another directory with the agent's own operation. |

An operation the agent has no verified native contract for is reported as unsupported. It is never emulated with another-only state, because such state disappears on the next refresh and misleads the person reading the list.

## Migration versus relocation

Migration renders a conversation through the portable model into a different agent's format. Reasoning signatures, tool calls, images, and system records do not travel; ordered user and assistant text, timestamps, project directory, and title do.

Relocation moves or forks a session within the same agent, using that agent's own copy or move. Nothing is re-rendered, so tool calls and reasoning survive. Only OpenCode 2 and Pi have such an operation today.

## Project scope

In a Git repository, the main worktree and every registered linked worktree form one project, and their subdirectories belong to it. A nested independent repository is excluded and stands as its own project. Outside Git, scope is an exact directory match. The TUI opens scoped to the current project; `f` switches to all projects.

## Safety rules

- Verify every write by reloading and comparing digests; roll back on mismatch.
- Never modify the source session during migration.
- Cleanup removes only the artifact just written, and refuses paths outside the provider's root or through suspicious symlinks.
- A session identified as the running one is protected from rename, archive, relocate, and delete. `another rename --allow-current` exists only for SessionEnd hooks.
- Destructive operations confirm first, default to cancel, and show the full session ID.
- The index is always rebuildable and never the authority.

## Configuration

Settings live at `~/.config/another/config.json`: enabled providers and their order, interface language, title agent and model, path aliases, and integration consent. First run without a config opens `another setup`.
