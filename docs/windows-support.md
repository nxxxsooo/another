# Windows support

Implementation branch `research/windows-support`, rebased onto `2fcc9af`.
Cross-compiled and unit-tested on macOS; the Windows CI leg runs the suite on
`windows-latest`. Nothing here has been run by a human on a real Windows
machine yet — that is the remaining verification, listed at the bottom.

## What changed and why

### 1. Resume commands render for the local shell (`internal/util/shell.go`)

Every provider used to compose `cd '<path>' && <agent> …` with POSIX quoting.
That line is invalid in `cmd.exe` (single quotes are literal) and unreliable
in the default PowerShell 5.1 (no `&&`), so both the TUI handoff and the
copied command were dead on Windows.

`QuoteArg` / `CdAnd` / `EnvAnd` branch on `runtime.GOOS`: POSIX syntax on
Unix, PowerShell on Windows (`Set-Location -LiteralPath '…'; …`,
`$env:K='…'; …`). The `For` variants take an explicit `ShellKind` so both
syntaxes are pinned by tests on every platform — the Windows CI leg cannot
execute much, but it can still prove what another would have handed
PowerShell. All eleven providers plus the `another paths link` suggestion go
through the seam; `ShellQuote` stays as the documented POSIX quoter.

### 2. The handoff executes on Windows (`internal/tui/launch_*.go`)

`syscall.Exec` is a stub returning `EWINDOWS`, so process replacement was
never an option. The exec surface is split by build tag: Unix keeps
`syscall.Exec` exactly, Windows runs `powershell.exe -NoLogo -NoProfile
-Command <command>` as a child on the same console with stdio inherited. A
nonzero agent exit comes back as the error instead of silently dropping to a
prompt. powershell.exe (5.1) is chosen over pwsh and sh because it is the one
interpreter guaranteed to be there; the rendered syntax avoids everything 5.1
lacks.

### 3. State and store locations follow Windows conventions

- another's own index and config move from `~/.cache` / `~/.config` to
  `%LOCALAPPDATA%\another` / `%APPDATA%\another` (`internal/config/paths.go`).
  Unix paths are deliberately untouched — moving an existing index orphans
  it, and there is no Windows state to orphan.
- OpenCode / OpenCode 2 probe `%LOCALAPPDATA%` first and fall back to
  `~/.local/share` (`config.AgentDataRoot`), because the upstream layout on
  Windows is unverified and an agent that keeps one layout everywhere must
  still be found. `XDG_DATA_HOME` still wins everywhere.
- Everything else (`~/.codex`, `~/.claude`, `~/.qwen`, `~/.pi`,
  `~/.gemini`, `~/.codem`, `~/.commandcode`, `~/.hermes`) is already
  home-relative in the agents themselves; Cursor already branched to
  `%APPDATA%`. Codex Desktop has no established location off macOS, so
  another keeps refusing rather than guessing (`desktop_dir_other.go` from
  #23).

### 4. The liveness refusal already covered Windows (#23, `9090e3e`)

Codex and Qwen read a failed `Signal(0)` check as "not running", which on
Windows — where signal 0 always fails — meant mutating a live agent's store.
`util.Liveness` now refuses on unknown. The qwen and util tests carry
Windows expectations: unknown refuses, and the branches that need a real
check are Unix-only.

### 5. Distribution: zips, a PowerShell installer, and `another update`

- `.goreleaser.yaml` builds `windows/amd64,arm64` and ships zips (tar.gz
  needs a third-party unpacker on stock Windows; `Expand-Archive` handles
  zips). Verified with `goreleaser release --snapshot`: the zips contain a
  real PE32+ `another.exe` at the root, and the names match the
  `another_<version>_windows_<arch>.zip` pattern `install.ps1` constructs.
- `scripts/install.ps1` mirrors `install.sh` (`INSTALL_DIR` default
  `%LOCALAPPDATA%\another`, `VERSION` default `latest`, user-PATH
  persistence for the default only). Syntax-checked with the PowerShell
  parser, locally and in CI — never executed on Windows yet.
- `another update` classifies `another.exe`, re-runs the ps1 installer with
  `INSTALL_DIR` set, and prints Windows-appropriate fallback instructions.
  No Scoop/WinGet: that would be a new release surface for an unproven
  user base. The release matrix stays at seven; the GitHub Release surface
  just gains two zips to verify against `checksums.txt`.
- CI gains a `windows-latest` leg: build, vet, full test suite with `-race`,
  plus the ps1 parser check.

## Still needs a real Windows machine

1. Run the installer, then `another` end to end: setup, list, preview.
2. The handoff into each installed agent (at least the six continuously
   tested ones): does the PowerShell line land, and does the agent resume?
3. Confirm where OpenCode, Codex Desktop, and CodeM actually keep state on
   Windows; adjust `AgentDataRoot` / `desktopStateDir` if the probe guesses
   wrong.
4. `another update` from a ps1 install.
5. TUI feel in Windows Terminal (and conhost, if that matters): colors,
   clipboard copy, farewell screen, window title.
6. Claude Code's project-trust path: `~/.claude.json` location and the
   re-run flow on Windows.

## Cheaper option, still true

WSL runs the existing Linux build unchanged today. It sees WSL-side agent
stores only, not Windows-native ones — fine for a developer who already lives
in WSL, nothing for a user running agents natively.
