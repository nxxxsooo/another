# Windows support

Implementation branch `research/windows-support`, rebased onto `2fcc9af`.
Cross-compiled and unit-tested on macOS, green on `windows-latest`, then run
on a local Parallels Windows 11 ARM64 VM (Windows 10.0.26200.9168,
PowerShell 5.1.26100.9168). The real-machine evidence and remaining provider
gaps are listed below.

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

The VM exposed one self-update bug before release: Windows permits renaming a
running executable but not overwriting or deleting it. `another update` now
passes its pid to the installer; the installer renames the live image aside,
installs the new one with rollback on copy failure, and starts a hidden helper
that waits for the old process to exit before deleting it. The exact path was
tested with a live `another.exe`: version `0.0.0-windows-test` stayed running,
`0.0.1-installer-test` was callable at the original path immediately, and the
`.old-<pid>` image disappeared after the old process exited. The original
binary and the test directory were then removed.

## Real Windows VM results

- Native ARM64 binary starts and reports the stamped version; `--help` renders.
- First-run setup is visually correct in Windows Terminal: zero providers are
  preselected; Codex and Qwen detection appears; the title page and save flow
  work; settings land under `%APPDATA%\another`.
- `providers doctor` found the VM's real Codex, OpenCode, and Qwen installs.
  Codex sessions were at `~\.codex\sessions`. OpenCode 1's real database was
  at `~\.local\share\opencode\opencode.db`, proving the native-first / legacy
  fallback selects the right branch on this machine.
- `list --refresh`, `index status`, and `show` loaded two real Codex sessions.
  The TUI listed the project-scoped session and previewed all 20 messages,
  including mixed English/Chinese, with correct colors and layout.
- Enter produced and executed the PowerShell command
  `Set-Location -LiteralPath 'C:\…'; codex resume '01…'`. Codex reached its
  own trust prompt and then attempted the exact session resume. It finally
  stopped on the VM's unrelated Codex configuration (`config.toml` originally
  contained only invalid `-NoNewline`; with a temporary empty config, the VM
  lacked the session's `OpenAI` model-provider definition). The another side
  of the handoff is therefore verified; a successful model turn is not.
- The invalid Codex config was backed up byte-for-byte for the test, restored,
  and verified against its original SHA-256. another's test settings/index,
  binaries, installer directories, screenshots, and HTTP server were removed.
  The VM's user PATH was not changed.

## Still open after the VM pass

1. Handoff into the other continuously tested agents. This VM only had a real
   Codex session; OpenCode and Qwen were installed but their stores were empty,
   while Claude, OpenCode 2, Pi, and AGY were not installed.
2. Confirm where OpenCode 2, Codex Desktop, and CodeM actually keep state on
   Windows; adjust `AgentDataRoot` / `desktopStateDir` if the probe guesses
   wrong. Same for the Claude project-directory encoding: the mapping matches
   Claude's rule, but the on-disk ground truth for a drive-letter path is
   unverified.
3. Run `another update` against a real published Windows asset. The installer
   and live-executable replacement are verified with a locally served zip; the
   final GitHub URL cannot exist before the release does.
4. Clipboard copy, farewell screen, and window-title restoration. Setup/list/
   preview colors and layout in Windows Terminal are verified.
5. Claude Code's project-trust path: `~/.claude.json` location and the
   re-run flow on Windows.
6. Whether AGY on Windows takes a presence lock another could share. Until
   that is established, agy rename/delete of existing conversations refuse
   there (`lock_other.go`), and the delete tests skip on Windows.

## What the Windows CI leg has already caught

The `windows-latest` leg earns its keep: the first run failed ~60 tests in
patterns that fell into three buckets, and the leg is green now.

**Real product bugs, fixed.** SQL `LIKE` child-prefixes hardcoded `/`
(`internal/index/store.go`), so project scoping, search filters, and titler
pruning matched nothing on Windows. `NestedRepoRoots` had the same hardcoded
separator, silently disabling worktree subtraction. pi's project-directory
encoding kept the drive-letter colon, which is illegal in a Windows file name;
it now matches pi upstream (`/[/\\:]/g` → `-`), verified against pi's
`session-manager.ts`. `NormalizeProjectPath` returned the `\\?\`-prefixed
form `EvalSymlinks` produces for existing directories while deleted ones kept
the plain form, so stored and queried paths diverged.

**Real product bugs, fixed.** SQL `LIKE` child-prefixes hardcoded `/`
(`internal/index/store.go`), so project scoping, search filters, and titler
pruning matched nothing on Windows. `NestedRepoRoots` had the same hardcoded
separator, silently disabling worktree subtraction. pi's project-directory
encoding kept the drive-letter colon, which is illegal in a Windows file name;
it now matches pi upstream (`/[/\\:]/g` → `-`), verified against pi's
`session-manager.ts`. `NormalizeProjectPath` returned the `\\?\`-prefixed
form `EvalSymlinks` produces for existing directories while deleted ones kept
the plain form, so stored and queried paths diverged.

**Test fixtures that only occur on Unix.** Drive-less `/repo` literals,
`/tmp`-style resume strings, permission-bit assertions (no Unix mode bits on
Windows), a JSON fixture that embedded a raw backslash path, `sh`-based fake
agent CLIs (unexecutable on Windows — those CLI-contract tests run on Unix),
and agy delete tests (refused without the presence lock, see above).

**One test bug of mine.** `process_other.go` returned unknown even for
non-positive pids; a pid no OS can have is gone everywhere.

## Cheaper option, still true

WSL runs the existing Linux build unchanged today. It sees WSL-side agent
stores only, not Windows-native ones — fine for a developer who already lives
in WSL, nothing for a user running agents natively.
