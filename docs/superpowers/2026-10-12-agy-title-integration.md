# agy-title-integration — implementation plan (bounded)

Worktree: `.worktrees/agy-title-integration`, branch `feat/agy-title-integration`.

## Verified contract (spike, 2026-10-12, agy 1.3.3)

- Stop hooks fire at the end of **every turn of an interactive session**; `--print` runs no hook events at all.
- Payload arrives on the hook command's **stdin as JSON**:
  `conversationId`, `transcriptPath`, `workspacePaths[]`, `fullyIdle`, `terminationReason`, `error`, `modelName`.
- The hook environment carries `ANTIGRAVITY_CONVERSATION_ID`, so `provider.IsCurrentSession`
  matches and the rename needs `--allow-current`.
- agy does not block the UI on Stop hooks: the prompt returns while a 10s hook still runs.
  Failures land in agy's `cli-*.log` (`stophooks.go`); they never interrupt the session.
- Two install surfaces exist: the shared `~/.gemini/config/hooks.json`
  (`group → {Event: [{command, timeout}]}`, array per event — this is the one hindsight uses,
  multiple groups coexist) and a plugin `hooks.json` whose schema is **different**
  (`Stop` is a single object, not an array) and additionally requires `agy plugin install`
  registration. Chosen surface: **shared hooks.json group**, because it is proven working
  and coexists with foreign groups; a native plugin would have another replicate agy's
  private install/registration mechanics.

## Design

- The hook command calls **another itself**, not a model: the policy, title agent, language,
  and validation already live in the binary (same split as the Pi extension).
  Command: `another rename --from agy --auto --skip-conforming --allow-current --refresh --hook-stdin`.
- `--hook-stdin` (new flag on `rename`): reads the hook payload from stdin, extracts the
  session id by trying `conversation_id`, `conversationId`, `session_id`, `sessionId`,
  and skips the rename when `fullyIdle` is explicitly `false` (a queued continuation is
  still running — the same rule that makes the Pi extension listen to `agent_settled`
  and not `agent_end`). With the flag, the positional id becomes optional;
  exactly one of the two must be present.
- Adapter `agy-title-policy` in `internal/integrations/`:
  - ConfigDir: sibling `config` directory of the agy root (`$AGY_HOME/../config`,
    default `~/.gemini/config`), overridable with `ANOTHER_AGY_CONFIG_DIR`.
  - Status: group present + command matches expected → Current; group present with a
    different command → Foreign; no group → Missing. (No language or version drift:
    the command line is the whole contract.)
  - Install: merge the group into the existing hooks.json, preserving every foreign
    group; refuse to touch a `another-title-policy` group another did not write
    without `--force`. File written with the perms it already had (0600 observed).
  - Remove: drop the group only when its command matches (or `--force`).
- Register in `adapters.go`; consent field `AgyTitlePolicy` in `config.Integrations`.
- Setup label: `setupPluginLabelAgy` ("Antigravity title hooks" / "Antigravity 标题钩子").
- Setup, `another integrations`, and `providers doctor` all iterate `Adapters()`
  already — no caller edits needed beyond the label switch.

## Steps (TDD)

1. `rename --hook-stdin`: tests for id extraction (both key spellings), fullyIdle skip,
   exactly-one-of-arg/flag, and the end-to-end flag combination the hook writes.
2. `internal/integrations/agy.go` + tests mirroring `opencode2_test.go` / `pi_test.go`:
   status states, install merge preserving foreign groups, remove.
3. adapters.go + config field + setup label (+ test).
4. Full `go build ./... && go test ./... && go vet ./...`.
5. Docs: README.md, README.en.md (AI title section), docs/providers.md + zh.
6. End-to-end on this machine with the dev binary: install the adapter, run one
   interactive agy turn in tmux, verify the conversation gets a policy title,
   then report state (leave installed or remove per user).

## Risks

- Renaming happens at first turn end; later turns keep the title (skip-conforming),
  same as Pi. Manual titles are never overwritten (`titler.Conforms` refuses policy-
  shaped manual titles? No — Conforms would match a manually-typed conforming title
  and skip; a non-conforming manual title WOULD be overwritten by the next turn.
  Same exposure exists for Pi; accepted, note in docs).
- `another` must be on the hook's PATH; failure is silent except in agy's log.
