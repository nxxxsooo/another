# Adding a provider

A provider is one adapter over one coding agent's own session store. It reads that agent's native format, writes it back in the same format, and declares only the operations the agent itself supports. Nothing about a provider may depend on state another invents.

Read [`docs/architecture.md`](architecture.md) first for the data flow and the digest rules.

## Before writing code

Answer these about the agent, with evidence from a real local install:

1. **Where does it store sessions, and can the root be overridden?** A single JSONL tree, a SQLite database, one file per session, or several stores that must agree. Note the environment variable, if any.
2. **How is a session identified?** A UUID in the filename, a column, or a field inside the file. This ID must be stable enough for the index and for the agent's own resume command.
3. **Which directory does a session belong to?** another attributes a session to the directory it started in. Find where the agent records that.
4. **What does a message look like?** User and assistant text is what travels; the adapter must be able to read it in order with timestamps.
5. **Can it resume by ID from the command line?** If yes, the exact command. If it needs an entry in its own index first, that is a `ResumeEnsurer`.
6. **Which lifecycle operations does it own natively?** Rename, archive, delete, relocate. Each one you claim must be a real operation the agent performs on its own state.

If an operation has no native contract, do not implement it. An unsupported answer is correct; an emulated one is a bug that disappears on the next refresh.

## The package

Create `internal/providers/<id>/<id>.go` with `const ProviderID = "<id>"` and a `New()` constructor that resolves its root through `config.EnvOrDefault`. Use an existing adapter of the same storage shape as the model: `internal/providers/qwen` for a JSONL tree, `internal/providers/opencode2` for an HTTP-backed store, `internal/providers/agy` for a database with a lock.

Implement the core interface from `internal/provider/provider.go`:

| Method | Contract |
|---|---|
| `ID`, `DisplayName` | Stable ID, human name as the agent brands itself. |
| `DefaultPaths` | Every path another should show in `another paths`, with its env override. |
| `Installed` | True when the binary is on `PATH` **or** the store exists. Never prompt or run the agent. |
| `Discover` | Cheap `model.Summary` records. Honor `opts.SkipSource` and `opts.SkipUnchanged` so unchanged files are not re-read, and `opts.ProjectFilter`. |
| `Load` | Full `model.Conversation` for one ref, ordered, with timestamps. |
| `Write` | Serialize atomically into the agent's own format, honor `opts.DryRun`, return the real storage path. |
| `SupportsResume`, `ResumeCommand` | The exact one-line command a person can paste. |

Then add only the optional contracts you verified: `PreviewLoader`, `ResumeEnsurer`, `WriteCleaner`, `SessionRenamer`, `SessionArchiver`, `SessionDeleter`, `ReversibleSessionDeleter`, `SessionRelocator`.

Rules that are not negotiable:

- `Write` must be atomic: temp file in the same directory, then rename. Never write in place.
- `CleanupWrite` removes only the artifact `Write` returned, refuses paths outside the provider root, and refuses suspicious symlinks.
- A rename writes to the agent's own title store. If the agent reads titles from more than one place and you can only reach some of them, return `provider.ErrPartial` wrapped with what was missed. That is a caveat, not a failure.
- Return `provider.ErrRelocateUnsupported` for a mode you do not own. `SupportsRelocate` is per mode, because an agent can own a copy without owning a move.
- Never modify a source session during migration.

## Registration

Adding the package is not enough. Six places name the agent:

| File | What to add |
|---|---|
| `internal/registry/registry.go` | The constructor in `newRegistry`, the binary in `CLICommand`, every alias in `NormalizeID`, and an entry in `compatibilityAdapters` if the agent is not continuously tested. |
| `internal/tui/theme.go` | A color in `providerColors` that stays apart from the others in a dense column. |
| `internal/tui/agent_chip.go` | A three-letter code in `agentCodes`, read like a ticker. |
| `internal/titler/titler.go` | A `launchers` entry only if the agent can run a headless one-shot prompt. Omitting it means no AI titles, which is fine. |
| `README.md`, `README.en.md` | The agent table row, in both languages, with the capability marks that match the code. |
| `docs/providers.md`, `docs/providers.zh.md` | Any behavior a person would be surprised by. |

An invariant test in `internal/tui/batch_suggest_agent_test.go` requires every provider that can rename to also be able to suggest a title. If you add a `SessionRenamer` without a `launchers` entry, that test fails by design.

## Tests

Put fixtures under `internal/providers/<id>/testdata/` in the agent's real format, taken from an actual session with anything private removed. Cover, at minimum:

- Discovery over a fixture tree, including project attribution and the skip callbacks.
- A load and write round trip whose `model.ContentDigest` matches.
- Each optional contract you implemented, including its refusal path.
- The unsupported paths: what a caller gets when the operation does not exist.

## Verify

```sh
go build ./...
go vet ./...
go test ./...
go test -race ./...
gofmt -l .
golangci-lint run ./...
```

Then against your real install:

```sh
go run ./cmd/auditproviders
another providers doctor
another index rebuild && another list --provider <id>
another migrate <session> --to <id> --dry-run
```

`cmd/auditproviders` exercises every adapter against the local stores and is the fastest way to catch a discovery or attribution mistake that fixtures miss.

## Tiers

The README distinguishes two tiers. Continuously tested agents go through an end-to-end pass every release. Compatibility adapters are kept working but are not exercised end to end each time; mark those in `compatibilityAdapters` so setup does not bury the tested ones. A new adapter starts as a compatibility adapter unless the maintainer commits to testing it on every release.
