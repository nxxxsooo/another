# Build Contract: all-in-one session manager, recall and @ mentions (v0.21.0)

Status: confirmed 2026-10-29. Product Brief confirmed the same day in session; Hindsight initiative `kp-dfdfbf79a0c4451d8d8c8732768f110b`.

OpenCode decision: the recall integration makes a tracked, comment-preserving edit that adds only `mcp.another` to `opencode.json(c)`, recorded in the manifest and removed cleanly. The title plugin's never-edit promise is unchanged.

## Project and sources of truth

- Repository `nxxxsooo/another`; rules in `AGENTS.md` (Windows runtime, six-agent regression, release matrix, native-or-absent).
- CLI: `internal/cli/root.go` (`list` :169, `show` :224, `search` :674, `export` :617). Index search: `internal/index/search.go`, scope SQL `internal/index/store.go:543-596`.
- Project scope: `util.DiscoverProjectScope` (`internal/util/project_scope.go:23`), applied in TUI `applyProjectScope` (`internal/tui/tui_cmds.go:341`).
- Integrations: `internal/integrations` (`Adapter`, `installBundle`/`removeBundle`, `.another-install.json` manifest, hook-merge adapters for agy/qwen).
- Bounding precedent: `internal/titler/prompt.go:34-38`, `util.NormalizeUserText`.

## Desired behavior

1. **Positioning.** Headline "Every coding-agent session, in one place — and in any agent." (zh equivalent). Manager first; migration is the signature feature second. Name story reinterpreted, name unchanged.
2. **One scope model.** `list`, `search`, `show` resolve the current project exactly like the TUI (Git repo + registered worktrees; plain directory + subtree). `--all` widens to every project. `--project PATH` remains an explicit override. `--cwd` is deleted (superseded).
3. **Bounded context.** `another context <id>` prints a bounded Markdown transcript: header (title, agent, project, created/updated, message count, full ID, `another show` hint), then the first 3 and last 6 user/assistant turns, newlines preserved, per-message cap 2,000 runes, total cap 24,000 runes, explicit elision lines (`… 41 turns omitted …`). Same renderer backs MCP reads, the Pi expansion, and the skill.
4. **Readable export.** `another export <id> --format markdown` writes the full, unbounded cleaned transcript; `json` stays the default portable format.
5. **@ mentions via MCP resources.** `another mcp` is a stdio MCP server exposing resources only (no tools in v1):
   - `resources/list`: the 50 most recent root sessions in the server's project scope, URI `another://session/<provider>/<id>`, name `<title> · <AGENT TAG> · <age>`, description = project-relative directory, mime `text/markdown`.
   - Resource template `another://session/{provider}/{id}` for any indexed session in scope.
   - `resources/read`: the bounded Markdown from (3). Out-of-scope or unknown IDs return an MCP error, never another project's content.
   - Index refresh is incremental at server start; a read re-checks the source fingerprint.
6. **Per-agent mention integrations** (kind `recall`, opt-in, tracked like title adapters):

   | Agent | Mechanism | Picker |
   |---|---|---|
   | Claude Code | register `another mcp` at user scope | native `@` (MCP resources) |
   | OpenCode V2 | register `another mcp` in global config | native `@` |
   | Qwen Code | `mcpServers.another` in `settings.json` (existing merge pattern) | native `@server:` |
   | Pi | extension `another-recall` stacking `ctx.ui.addAutocompleteProvider`; inserts `@another:<id>`, expands to bounded Markdown on submit | `@` in Pi |
   | Codex | skill `another` (Codex `@` picker lists skills); skill runs `another search` / `another context` | `@another <words>` |
   | Antigravity | verify MCP-resource or skill support during slice 5; ship the stronger verified path, else report unsupported | TBD by evidence |

   Registration prefers each agent's own CLI (`claude mcp add -s user`, etc.) when a non-interactive contract exists; otherwise a tracked minimal config merge. Status/install/remove reuse `another integrations` with `--kind recall`.
7. **Setup.** The integrations page gains one opt-in row per enabled agent ("Claude Code session mentions"), off by default, naming the file or command it touches.

## Must preserve

- Native state or nothing: no another-only tags/notes; MCP and mentions are read-only.
- Title adapters, their manifests and statuses unchanged.
- Windows: stdio server, paths, config locations, `.cmd` shims; CI `test` + `windows` green with race.
- Interface strings bilingual in the same change; CLI help/errors stay English.

## Non-goals (v1)

MCP tools/prompts, semantic search, secret redaction, hygiene, usage insight, user-authored metadata, cloud anything.

## Interfaces, data, state, errors

- New package `internal/recall`: `Render(conv, Bounds) string`, `Bounds{Head, Tail, PerMessage, Total}`, `Full` bounds for export. Pure, deterministic, golden-tested.
- New `internal/cli/mcp.go`, `internal/cli/context.go`; scope helper shared by CLI and TUI (move `applyProjectScope` logic to a non-TUI package; TUI calls it).
- MCP library: official `github.com/modelcontextprotocol/go-sdk` (new dependency; avoids hand-rolled JSON-RPC lifecycle).
- Errors: unknown/out-of-scope session → exit 1 with explicit message (CLI) / `-32002` resource not found (MCP). Missing index → build it, never silently empty.

## Security and privacy

- Default project scope everywhere; `--all` explicit. MCP server scope fixed at launch directory and never widened by a client.
- Mentioned content goes to whichever model the target agent uses; README states this plainly. No redaction in v1 (residual risk).
- Config files written keep existing mode; manifests record exactly what was added for clean removal.

## Must-pass scenarios

1. In repo A, `another search foo` returns only A + its worktrees; `--all` returns B's hits too; same on Windows paths with drive letters.
2. `another context <id>` on a 200-turn session stays ≤ 24,000 runes, keeps newlines, shows elision and the `show` hint.
3. Claude Code, OpenCode, Qwen Code in repo A: typing `@` lists recent A sessions from other agents; selecting one injects the bounded Markdown; no repo-B session is listed or readable.
4. Pi: `@` suggests sessions; submitted prompt carries the expanded transcript.
5. Codex: `@another` + words activates the skill, which finds and quotes the right session.
6. `another integrations remove --kind recall` restores every touched config byte-for-byte except unrelated user edits.
7. Fresh setup: no recall rows enabled; enabling one writes only the named target.
8. Six-agent regression pass plus existing migration/relocate suites green.

## Implementation slices

1. **Scope unification** — shared scope helper; CLI default + `--all`; delete `--cwd`; tests incl. sibling-prefix and traversal on Windows fixtures.
2. **Recall renderer** — `internal/recall`, `another context`, `export --format markdown`; golden tests.
3. **MCP server** — `another mcp` resources list/template/read; in-memory transport tests; scope enforcement test.
4. **Integrations kind `recall`** — Claude Code, OpenCode, Qwen Code MCP registration; status/install/remove; setup rows + strings.
5. **Pi extension + Codex skill + Antigravity verification** — bundles under `integrations/`; live check in each client. *Review milestone: Mingjian tries `@` in Claude Code and Pi on `adev` before slices 6–7.*
6. **Docs** — README zh/en rewrite, `docs/providers*.md` "Mention" column, `docs/architecture.md`, `docs/product.md`.
7. **Release prep** — `.goreleaser.yaml` Compatibility (scope default change, `--cwd` removal, new integrations), site landing/case/release-post copy.

## Verification and release

- Per slice: `go test ./...`, `go test -race ./...`, `go vet`, `golangci-lint`.
- Dev test: `make install`, verify `adev` → `another-dev` `+dev` matches build; live `@` checks in the six agents.
- Ship v0.21.0 only after acceptance via `mj-ship`, full release-surface matrix per `AGENTS.md`.

## Implementation notes (2026-10-30)

- Slices 1–3 landed as designed. Go minimum is 1.25 (official MCP go-sdk v1.8.0).
- Slice 4: recall adapters are `Kind: recall` entries beside the title adapters (`integrations --kind recall`). Their setup rows sit in the same adapter block on the title page.
- Claude Code: registered through `claude mcp add --scope user another -- <binary> mcp`, because `.claude.json` is rewritten by running sessions. Status reads the file.
- OpenCode V2: the canonical location is `mcp.servers.another`. The direct `mcp.<name>` form is deprecated V1 and is still recognized and migrated. The edit is a JSONC AST insert/remove (tailscale/hujson) that copies sibling indentation, line endings, and trailing-comma style, and a remove after an install restores the file byte for byte. `opencode mcp add` was rejected: it writes through the running service into whatever config that service uses, ignoring the directory another resolved.
- The registered command is the installing binary's name (`another` or `another-dev`), so dev builds can be tested.
- Qwen Code: `mcpServers.another` in the same `settings.json` as the title hooks, patched as JSONC. Verified live: `qwen mcp list` shows it Connected.
- OpenCode V2's TUI `@` picker lists skills, references, agents and files, but not MCP resources (V1 did). Found in Mingjian's live test on 2026-11-02 and confirmed in `packages/tui/src/component/prompt/autocomplete.tsx`. The OpenCode recall adapter therefore also installs the `another` skill (`integrations/another-skill/`) into `<config>/skills/another`, so `@another <words>` loads instructions to find and read the session through the MCP server or CLI. Upstream: anomalyco/opencode#61071. A per-session picker via a TUI plugin (`/session` plus a searchable dialog) is deferred; that API cannot insert into the composer.
- OpenCode cannot be isolated for end-to-end testing: it reads the real home directory whatever `HOME`/XDG say. Its live `@` check happens on the real config during dev testing.

## Residual risks

- Claude Code's `@` list is snapshotted at session start (no `list_changed` refresh): sessions created after launch appear only via the template or next launch.
- The current session itself may appear in the list; acceptable in v1.
- Large resource lists may crowd file results in pickers; capped at 50.
- Pi and Codex mechanisms depend on young extension/skill APIs; pinned to verified versions in docs.
- Secrets in transcripts can reach another model provider.
