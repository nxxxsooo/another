# Adaptive Migration Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve complete cleaned conversation history when safe or explicitly requested, while retaining a resumable bounded mode for oversized sessions.

**Architecture:** The migration engine owns context-mode parsing, cleaning, safe-size detection, and projection. CLI and TUI only pass the requested mode. Migration metadata keys deduplication by source snapshot plus selected mode so full and recent targets cannot collide.

**Tech Stack:** Go 1.22, Cobra, Bubble Tea, existing provider and SQLite index interfaces.

---

### Task 1: Context projection and metadata identity

**Files:**
- Modify: `internal/model/conversation.go`
- Modify: `internal/migrate/engine.go`
- Modify: `internal/migrate/dedup.go`
- Test: `internal/migrate/engine_write_test.go`
- Test: `internal/migrate/dedup_origin_test.go`

- [ ] **Step 1: Add failing projection tests**

Add table tests that call `projectConversation` with `auto`, `full`, and `recent`. Assert that safe `auto` is complete, oversized `auto` is bounded, `full` is unshortened, `recent` is bounded, repeated turns survive, and unsupported/empty/`[REDACTED]` records do not.

- [ ] **Step 2: Verify the tests fail**

Run `go test ./internal/migrate` and expect undefined context-mode/projection symbols.

- [ ] **Step 3: Implement the shared projection**

Add `ContextAuto`, `ContextFull`, and `ContextRecent`, plus:

```go
func ParseContextMode(value string) (ContextMode, error)
func projectConversation(source *model.Conversation, mode ContextMode) (*model.Conversation, ProjectionInfo)
```

Clean user/assistant text once, preserve order/timestamps/repeated turns, choose full or recent using the existing 48-message, 64,000-rune, and 16,000-rune limits, and emit a source-ID retrieval handoff only when reduced.

- [ ] **Step 4: Make dedup mode-aware**

Add `contextMode` to `MigrationMeta`, bump `MigrationTargetFormatVersion`, and derive the marker digest from the source snapshot plus mode. Make `FindDuplicateE` use an explicit `WriteMigration` marker when present, while preserving old behavior for callers without one.

- [ ] **Step 5: Run and commit**

Run `go test ./internal/model ./internal/migrate`; expect PASS. Commit the engine/model/dedup tests and implementation.

### Task 2: CLI and TUI plumbing

**Files:**
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/import.go`
- Modify: `internal/tui/tui.go`
- Test: `internal/cli/root_test.go`

- [ ] **Step 1: Add failing CLI tests**

Assert `--context` exists on root migration, `migrate`, and `import`, defaults to `auto`, and rejects values outside `auto|full|recent`.

- [ ] **Step 2: Verify the tests fail**

Run `go test ./internal/cli`; expect missing-flag failures.

- [ ] **Step 3: Pass context mode through every migration entry point**

Add `--context auto|full|recent`, validate before confirmation/write, set `migrate.Options.ContextMode`, and pass the root guided mode into `tui.RunMigrate`. Regular TUI migrations remain `auto`.

- [ ] **Step 4: Report projection facts**

Extend `migrate.Result` with selected mode and source/cleaned/projected counts. Print those counts for dry-run and print overflow warnings for reduced auto/recent or oversized full migrations.

- [ ] **Step 5: Run and commit**

Run `go test ./internal/cli ./internal/tui`; expect PASS. Commit CLI/TUI changes.

### Task 3: Documentation and regression validation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document the modes**

Replace the claim that migration is always bounded with examples for default `auto`, explicit `--context full`, and explicit `--context recent`. State that stored full history can still exceed a model context window.

- [ ] **Step 2: Run repository checks**

Run `gofmt` on changed Go files, `go test ./...`, `go test -race ./...`, `go vet ./...`, `bash -n scripts/*.sh`, the project build, and `git diff --check`; expect all checks to pass.

- [ ] **Step 3: Commit**

Commit documentation and any validation-only adjustments.

### Task 4: Real migration, install, and release proof

**Files:**
- Build output: `bin/agenthop`
- Installed binary: `/home/cyrus/.local/bin/agenthop`

- [ ] **Step 1: Exercise real Cursor-to-Codex modes**

Dry-run the known Cursor source in `auto`, verify it reports reduction, then migrate it with `full` only if requested for overflow testing. Create a clean `auto` target and confirm `codex resume` accepts a one-line no-tool prompt.

- [ ] **Step 2: Install and verify**

Build `bin/agenthop`, install it to `/home/cyrus/.local/bin/agenthop`, and require identical SHA-256 hashes.

- [ ] **Step 3: Push and verify CI**

Push `main`, verify remote HEAD equals local HEAD, and wait for the GitHub Actions test workflow to complete successfully.
