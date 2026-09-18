# Linux support

Implementation branch `feat/linux-support`, based on `c4a471e`. Linux has been
a build target since `v0.2.0` — goreleaser ships `linux/amd64` and
`linux/arm64` tarballs, `scripts/install.sh` branches on `uname`, the Homebrew
cask covers Linux, and CI has always run the full suite on `ubuntu-latest`.
What had never happened is a run on a real Linux machine. This pass did that
and fixed what it found.

## How it was verified

An Ubuntu 24.04 ARM64 container (Docker 29.4.0, non-root user `dev`, no
display), with the real `@openai/codex` 0.155.0 and `@anthropic-ai/claude-code`
2.1.276 CLIs installed from npm, driven through tmux so the TUI was rendered
and keyed exactly as a user would. Codex sessions were seeded from the repo's
own fixtures into `~/.codex/sessions/`; Claude Code received its sessions from
another itself.

- **Install script.** `curl -fsSL https://mjshao.fun/another/install.sh | bash`
  installed `v0.15.4` to `~/.local/bin`, appended the PATH line to `~/.bashrc`,
  and printed the current-shell `export`. The short URL is a 308 to the raw
  file on GitHub; both the sh and ps1 bodies came back byte-identical to `main`.
- **Detection.** `another providers` and `providers doctor` found the real
  Codex install (`data=ready cli=ready sessions=2`) and, once Claude Code was
  installed, `claude-code cli=ready`.
- **State locations.** Config landed in `~/.config/another/config.json` and the
  index in `~/.cache/another/index.db` — the XDG layout, not the macOS one.
- **CLI.** `list --refresh`, `index status`, `show`, and `export` all read the
  seeded sessions correctly.
- **Setup TUI.** Rendered correctly in tmux at 110×32: zero providers
  preselected, Codex shown as detected, the adapters folded into one line, the
  title page and save flow both working.
- **Migration.** Codex → Claude Code wrote
  `~/.claude/projects/-home-dev-demo/<uuid>.jsonl` with Claude's own project
  directory encoding and valid JSONL, then indexed and listed it.
- **Handoff.** Enter on a Codex session exec'd
  `cd '/home/dev/demo' && codex resume 'test-session-001'`; `ps` confirmed the
  real `codex resume test-session-001` process, which then stopped at its own
  sign-in screen because the container has no credentials. The another side of
  the handoff is verified; a model turn is not.
- **Self-update.** A binary stamped `0.10.0` in `~/.local/bin` was classified
  as a script install, re-ran the installer, and came back as `0.15.4`.
- **Farewell screen.** The logo and `Keep the session. Change the agent.`
  render correctly on exit.

## Second pass: a real Ubuntu desktop VM

The container answers for a server. A Parallels Ubuntu 24.04 LTS ARM64 desktop
VM (GNOME, `Type=wayland`, user `parallels`, Rosetta enabled) answers for a
workstation, and this one already had a real OpenCode install with a 16 MB
`opencode.db`.

- **Install in a real login session.** The installer ran end to end from a real
  release tarball, wrote the PATH line to `~/.bashrc`, and — because `SHELL` is
  exported in a login session — installed the `a` alias through the ordinary
  path, not the new fallback.
- **Real OpenCode data.** `providers doctor` found the primary V1 database and
  the V2 compat database through the unified provider; `list --refresh` and
  `show` read the machine's actual session.
- **Real migration and handoff.** That OpenCode session migrated to Codex on
  the VM, and Enter resumed each side for real: `codex resume '01a0b368…'`
  reached Codex's sign-in screen, and OpenCode reopened its own session with
  the original message and its earlier error intact.
- **amd64 through Rosetta.** The `linux/amd64` build ran and reported its
  version, and the cross-compiled `shortcuts`, `migrate`, `index`, and `tui`
  test binaries all passed. This is x86-64 userspace on an ARM64 kernel, not a
  native amd64 machine, so it proves the binary and the packages, not the
  kernel interface.
- **Wayland clipboard, both ways.** A stock Ubuntu desktop has no
  `wl-clipboard`, so `c` reported `No clipboard available` on a real GNOME
  Wayland session — the same result as the headless container, from a machine
  that visibly has a clipboard. After `apt install wl-clipboard`, `c` reported
  the copy and `wl-paste` returned the exact command.

Everything another touched in the VM was removed afterwards: its binary,
config, index, the seeded Codex store, the npm prefix, and both `~/.bashrc`
lines. `wl-clipboard` was left installed. The OpenCode store was only read.

### The network, and what the short URL does not fix

From that VM's network (mainland China, no proxy), `https://mjshao.fun` resolved
and returned its 308 in 1.3s while `raw.githubusercontent.com` and `github.com`
both timed out; only `api.github.com` answered. The short URL shortens what a
user types and nothing else — the script body and the release tarball still come
from GitHub, so an install there fails either way. Making installs work on that
network would mean serving the script body and mirroring release assets from the
site, which is a product decision, not a packaging one.

`scripts/install.sh` gained `ANOTHER_DOWNLOAD_URL`, which `install.ps1` has
honored since Windows support landed. It made this VM test possible at all —
the tarball was served from the host — and it is the same escape hatch for a
mirror or for testing an installer before a release owns its asset.

## What it found, and the fixes

### 1. The alias step failed whenever `SHELL` was not exported

`curl … | bash` runs as a child of nothing in particular, and bash sets `SHELL`
as a plain shell variable when the environment lacks it. The installer reads
that variable and picks the right startup file, but every process it starts —
including `another aliases install` — sees an empty `SHELL` and refused with
`shortcut aliases: unsupported shell ""; skipped`, exit 1. It reproduced on
every install and on every `another update`. Containers, cron jobs, and
`ssh host 'command'` all land in the same place.

`planFor` now falls back to the user's login shell from the passwd database
(`/etc/passwd`, then `getent` for LDAP/SSSD users) before giving up, treating
`nologin` and `/bin/false` as no shell at all
(`internal/shortcuts/login_shell_unix.go`). Windows keeps its
`powershell.exe` default. Verified on the container: with `SHELL` unset,
`another aliases install` now writes `a` into `~/.bashrc`.

### 2. `c` claimed to copy on machines with no clipboard

`atotto/clipboard` shells out to `xclip`, `xsel`, or `wl-copy` on Linux and
returns an error when none exists — which is the normal state of a headless
server, the exact machine people SSH into to use another. The error was
discarded and the TUI printed `Resume command copied` anyway, sending the user
to paste something that was never copied. The TUI now reports
`No clipboard available; the command is on screen` instead
(`internal/tui/tui_update.go`). With `xclip` and an Xvfb display present, the
success path still copies and `xclip -o` returns the exact command.

### 3. The copy confirmation was never visible on any platform

Found while testing #2: `footerView` matched `m.lastResume != ""` before
`m.status != ""`, and `c` is only offered while a resume command is on screen.
Every copy confirmation had been losing that race since the footer was written.
Both lines now render (`internal/tui/tui_view.go`).

### 4. A repeat migration dropped the `cd`

Also platform-independent, surfaced by migrating the same session twice on
Linux. A dedup hit builds its `WriteResult` from the index, which stores no
project path, and every provider's `ResumeCommand` drops the `cd` when the
project path is empty. Migrating an already-migrated session therefore handed
back `claude --resume '<id>'`, which resumes wherever the user is standing
rather than in the session's project. The dedup branch now fills the project
path from the request, then the source conversation
(`internal/migrate/engine.go`); `TestEngine…` fails without the fix.

## Still open

1. Pi, Qwen Code, and Antigravity are untested on Linux against real stores.
   Codex, Claude Code, and OpenCode (V1 and V2 databases) are covered.
2. No real model turn. Codex reached its sign-in screen and OpenCode reopened
   its session, but no agent completed a turn — neither machine has usable
   credentials.
3. No native amd64 kernel. The amd64 binary and four test packages pass under
   Rosetta on an ARM64 kernel; a real x86-64 machine has not run them.
4. Distro variance. Ubuntu 24.04 only — no Fedora/Arch/NixOS, no musl system
   (CGO is off, so the binary is static), no systemd-less init.
5. The alias fallback resolves the login shell, which can differ from the shell
   the user is actually sitting in. `SHELL` still wins when it is exported.
6. Installing on a network that cannot reach GitHub, as described above.
