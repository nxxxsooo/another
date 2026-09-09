# Windows support — feasibility

Research note, not a commitment. Branch `research/windows-support`.
Verified against `e3781be` on macOS with cross-compilation; nothing here was run on Windows.

## The compile barrier is already zero

```
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...   # clean
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build ./...   # clean
GOOS=windows go vet ./...                                # clean
```

A 25 MB `another.exe` falls out of the tree today with no source change. SQLite is
`modernc` (pure Go), and bubbletea, lipgloss, and the clipboard all carry Windows
implementations. The one darwin-only file, `internal/tui/input_source_darwin.go`, is
already tagged, and `internal/providers/agy/lock_other.go` already refuses under
`!darwin && !linux` rather than pretending the lock was taken.

So "can it build" is the wrong question. Everything below compiles and then behaves
wrongly at runtime.

## 1. The handoff, which is the product, does not work

`internal/tui/tui.go:266-287`:

- `os.Getenv("SHELL")` is empty on Windows, so it falls back to `/bin/sh`.
- `syscall.Exec` on Windows is a stub that returns `EWINDOWS`. Nothing is executed.

`internal/util/paths.go:110` quotes POSIX-style, and every provider composes
`cd '<path>' && <agent> …` — 20 call sites across 10 providers. That line is invalid in
`cmd.exe` (single quotes are literal) and unreliable in the default PowerShell 5.1
(no `&&`). So the copy-command path hands the user a line that does not run either.

This is the largest piece of real work and also the most contained: one shell seam that
picks a target shell and renders `cd` plus quoting for it, and one Windows launch path
built on `exec.Command` + exit code instead of process replacement.

## 2. Session-store discovery needs a real Windows machine

Probably fine, because the agents use `~/.<name>` on every platform: Codex, Claude Code,
Qwen, Antigravity, CommandCode, Hermes.

Known wrong today:

| Site | Assumes | Windows reality |
|---|---|---|
| `providers/opencode2/opencode2.go:34`, `providers/opencode/opencode.go:30` | `~/.local/share` | `%LOCALAPPDATA%` |
| `providers/codex/gui_state.go:209` | `~/Library/Application Support/Codex` | different path and singleton mechanism |
| `config/paths.go:29-39` | `~/.cache`, `~/.config` for another's own state | works, but not where a Windows user looks |

`providers/cursor/cursor.go:270` already branches to `%APPDATA%` correctly, so this was
considered once before. None of the rest can be desk-checked; each provider has to be
confirmed against an installed agent.

## 3. Safety checks degrade silently, which is worse than failing

- `providers/codex/gui_state.go:233` and `providers/qwen/qwen.go:660` decide "is this
  agent running" with `proc.Signal(syscall.Signal(0))`. On Windows that always errors, so
  both report *not running* and another will mutate a session store that is open. That is
  the data-loss shape the release contract calls a hotfix.
- `os.Rename` over a file another process holds open fails on Windows with a sharing
  violation. Six write paths depend on it, including `util/paths.go:169` and
  `config/settings.go:143`.
- `providers/agy/lock_other.go` is the correct precedent: refuse the operation and say so.
  Codex and Qwen should do the same rather than guess.

## 4. Distribution does not exist yet

`.goreleaser.yaml` builds `linux` and `darwin` only, ships `tar.gz` only, and installs
through a Homebrew cask or `scripts/install.sh`. `internal/cli/update.go` offers `brew`,
`sh -c curl | bash`, or `go install` — none of which is a Windows answer.

Windows needs `goos: windows`, zip archives, and a package manager (Scoop or WinGet).
Per `AGENTS.md` that is a seventh release surface to verify on every tag. CI is
`ubuntu-latest` only, so without a `windows-latest` job there is no regression signal at all.

## What it costs

| Step | Effort |
|---|---|
| Windows binaries in releases, TUI opens, sessions list | hours |
| Handoff and copied command actually run | 1–2 days, contained to the shell seam |
| Path discovery, liveness, and locking trustworthy across the six tested providers | the bulk, and only doable on real Windows |
| Forever after | +1 CI leg, +1 release surface, +1 provider regression pass per release |

## Cheaper option

WSL runs the existing Linux build unchanged today. It sees WSL-side agent stores only,
not Windows-native ones, so it serves a developer who already lives in WSL and does
nothing for a user running Codex or Claude Code natively. Worth naming in the README
either way, since it is free.
