# Contributing

Thanks for looking. `another` moves a live coding-agent session from one agent to another by reading and writing each agent's own session store. That makes correctness a safety property, not a preference: a bad write can cost someone a conversation they cannot get back.

Start with [`docs/architecture.md`](docs/architecture.md). To adapt one more agent, follow [`docs/adding-a-provider.md`](docs/adding-a-provider.md).

## Development loop

```bash
git clone https://github.com/nxxxsooo/another.git
cd another
make build
go test ./...
```

Requires Go 1.24 or newer. The SQLite index is a cache under `~/.cache/another/`; `another index rebuild` recreates it from the agents' own stores, so deleting it is always safe.

`make build` writes `bin/another`, and running it as `./bin/another` keeps a development build out of `PATH`. To carry one between terminals, `make install` puts it at `~/.local/bin/another-dev` — never at `another`, which belongs to whatever you installed from a release. Both builds stamp their own version, and a build from this Makefile always ends in `+dev`, so `--version` says which one you are looking at even when the tree sits exactly on a tag.

## What must pass

Everything below is enforced in CI, and running it locally is faster than a failed run:

```bash
go build ./...
go vet ./...
gofmt -l .                # must print nothing
go test ./...
go test -race ./...
golangci-lint run ./...   # brew install golangci-lint
```

For a change that touches provider adapters, also run them against your real local stores:

```bash
go run ./cmd/auditproviders
another providers doctor
```

## The rules that are not up for negotiation

- **Never modify a source session.** Migration reads the source and writes a new session elsewhere. Nothing else.
- **Verify every write.** Reload what was written and compare `model.ContentDigest`. On mismatch, remove exactly that artifact and report failure.
- **Native state or nothing.** An operation another cannot perform through the agent's own contract is reported as unsupported. Never emulate rename, archive, delete, or relocate with another-only state; it disappears on the next refresh and misleads the person reading the list.
- **Relocation is not migration.** Carrying a session into another directory must use the agent's own move or copy, never a re-render through the portable model, which would silently drop tool calls and reasoning.
- **Destructive actions confirm first**, default to cancel, and say whether the action can be undone.

## Tests

Test code is expected to grow with production code. New behavior needs a test that would fail without it. Provider adapters are tested against fixtures in the agent's real storage format under `internal/providers/<id>/testdata/`, not against mocks. CLI commands are tested through the fake-provider harness in `internal/cli/harness_test.go`, which gives every command a real index and a controllable provider.

Scrub fixtures: no absolute home paths that identify a person, no tokens, no private conversation content.

## Provider tiers

Pi, OpenCode 2, Claude Code, Codex, Antigravity, and Qwen Code are continuously tested and enter every release regression pass. Cursor, OpenCode, CommandCode, and Hermes are compatibility adapters: kept working, but not promised an end-to-end maintainer test on every release. A change that would break a compatibility adapter still needs a reason.

## Pull requests

- One concern per pull request. A pure refactor and a behavior change do not belong in the same commit.
- Conventional commit subjects: `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`. Say what changed and why, not which files.
- In the description, state what you verified and how, including the commands above and anything you exercised against a real agent.
- If you touched the capability table, update `README.md` and `README.en.md` together, and keep them consistent with the code.

## Reporting a bug

Include:

```bash
another --version
another providers doctor
```

and, for a migration problem, the output of the same command with `--dry-run`. Say which agent was the source and which was the target, and whether the session had been migrated before. Do not paste private conversation content; the session ID and the dry-run output are usually enough.
