# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary: developers who run two or more coding-agent CLIs on the same machine and the same projects — Pi, Codex, Claude Code, Cursor, OpenCode, OpenCode 2, CommandCode, Hermes. They reach for `another` when they hit a rate limit, want a different model mid-task, or an agent is stuck and they do not want to lose the conversation. They also accumulate hundreds to thousands of past sessions across those tools with no single place to find, rename, or clean them up.

Secondary: the maintainer, who uses it daily. This is a real user, not a persona.

Stated in the maintainer's brief and not contradicted; no external user research exists.

## Product Purpose

`another` is a local, keyboard-driven session manager for coding agents. From one list it browses, searches, previews, resumes, renames, archives, and deletes real sessions across eight agent CLIs, and it can carry a session into a different agent by writing that agent's own native session format so the target's own resume command works.

Success is two-fold: a developer who switches agents mid-task keeps the actual conversation rather than a summary, and a developer with sessions scattered across eight tools manages them from one place.

## Positioning

Migration writes native sessions, not handoff summaries. The target agent resumes its own file or database as if it had always owned the session. Every write is reloaded, digest-compared against the source content, and rolled back on mismatch.

Management is native on the same terms: rename, archive, and delete land in the provider's real state (Codex threads DB and `session_index.jsonl`, Claude `custom-title`, Pi `session_info`, OpenCode native rows, OpenCode 2 official API). A provider without a verified native contract reports the action as unsupported rather than faking it in a private index.

A neighboring tool could copy the browser. It could not truthfully copy "the other agent resumes it natively, and we proved the write survived."

## Operating Context

- The product itself is a terminal UI: Go 1.24, Bubble Tea, Lip Gloss, Charmtone palette, run as `another` in a normal terminal.
- `## Platform: web` refers to the public design surface, which is the GitHub repository page: README, badges, TUI preview, release assets, About panel, social preview. The application is terminal-native and is not a web app.
- Install is `curl -fsSL .../install.sh | bash` or `go install github.com/nxxxsooo/another/cmd/another@latest`. Updates are manual; installed binaries do not self-update.
- First run opens `another setup`, a multi-select of which agents `another` manages. It is re-runnable at any time.
- Reads each agent's native local store. The private search index (SQLite + FTS5) lives at `~/.cache/another/index.db`; config at `~/.config/another/config.json` with `0700`/`0600` permissions.
- No account, no network requirement, no telemetry.

## Capabilities and Constraints

- Eight providers: `pi`, `codex`, `claude-code`, `cursor`, `opencode`, `opencode2`, `commandcode`, `hermes`. OpenCode V1 and OpenCode 2 are parallel providers, not versions of one — isolated databases, CLIs, and schemas.
- Actions: browse, search (FTS5 over titles and normalized message text), preview, native resume, cross-agent migrate, rename, archive and unarchive, delete, JSON export and import.
- Capability is per provider and deliberately unequal. Unsupported actions are reported, never simulated.
- Migration carries real user and assistant text. It does not compress into a summary; reasoning and tool noise are dropped.
- A migration never mutates the source session.
- Delete shows provider, title, directory, and full session ID, and defaults to cancel. Sessions identifiable as running are blocked from rename, archive, and delete.
- Disabling a provider in setup prunes it from the index and registry but never deletes native sessions.
- Archived sessions are excluded from the default list. There is no archive-browsing view yet — a deliberate omission so roughly ten thousand archived Codex rollouts do not flood the list.
- macOS and Linux. Go 1.24+ to build from source.

## Brand Commitments

- Name is `another`, always lowercase.
- Tagline: "Keep the session. Change the agent." Chinese line: 在多个 coding agent 之间浏览、管理、迁移并继续真实会话。
- The name is a stated, non-affiliated nod to P.A.WORKS' *Another* (2012): the abstract idea of an extra presence whose identity is hard to distinguish. Conceptual only. No characters, eyepatch, doll, poster imagery, or series artwork, ever.
- Product palette, shared with the TUI: `#0A0A0A` surface, `#141414` panel, `#2D2D38` line, `#F4F4F5` text, `#7E7E8F` muted, `#6B50FF` source violet, `#00FFB2` target mint, `#68FFD6` intersection, and `#FF6B6B` coral. The TUI uses violet for current/source state, mint for destination/migration/success, and intersection cyan-white for the selected row; magenta remains a provider or transient campaign color rather than the global focus color. Colors are named through Charmtone (Charple, Julep, Bok, Sriracha, Malibu, Blush, Dolly).
- Monospace-first typography in product artwork.
- MIT licensed. The CyrusSE copyright line in `LICENSE` stays for license compliance, and the fork origin is acknowledged in the README.
- **Identity is resolved:** the approved master is the tightly overlapped purple-and-green double-`a` V1 with cyan-white intersection bloom, tactile halftone grain, and screen-print texture (Iterlay source `264e4b25-…-1`). Two near-coincident forms read as one identity that cannot shed its extra presence. Neither is designated as the original.
- Static applications use the purple-and-green master. The README identity banner adds one short neon rupture: horizontal tears briefly expose pink and blue channels plus a three-frame extra ghost, then restore the mark intact. Pink and blue are motion accents, not stable logo colors.
- Purple/pink, monochrome violet, bone-white, and low-glow treatments remain optional campaign extensions. They are not the primary mark.
- Four flat-vector "paradox icon" candidates remain rejected; do not re-propose flat vector marks. Earlier failures came from forcing an image model to render precision vector assets and from generic migration metaphors such as arrows, nodes, and portals.
- When generating future campaign extensions, use low-constraint prompts that let the image model art-direct. Do not specify "flat crisp vector" or icon-size legibility rules.

## Evidence on Hand

- Public repository `github.com/nxxxsooo/another`, MIT, default branch `main`. Release `v0.2.0` published with darwin and linux, amd64 and arm64 tarballs plus `checksums.txt`.
- `docs/assets/tui-preview.svg` — deterministic, de-identified TUI preview generated by the tracked `scripts/render-readme-assets.py`. Currently the README hero. Its session titles and paths are synthetic by design.
- `docs/assets/another-mark-master.png` — approved purple-and-green static mark, preserved at its native `1254×1254` resolution.
- `docs/assets/another-motion-static.jpg` — approved `1200×360` static README composition.
- `docs/assets/another-motion.gif` — `1200×360`, 2.4-second looping README identity banner. Reproducible with `scripts/render-motion-banner.py` from the two checked-in masters.
- `/tmp/another-logo.png` (900×190) — an earlier code-drawn wordmark/banner kept only as exploration; not a shipping asset.
- `docs/assets/banner.svg` and `banner.png` exist only in git history at `5b2b268`. They carry the retired `agenthop` brand and are not current assets.
- Iterlay board `another — icon & launch` (`cb3b646d-4b9a-4c22-bda6-be51bae4717c`) holds the approved master, campaign extensions, and rejected candidates.
- No testimonials, user counts, benchmarks, press coverage, adoption metrics, or third-party endorsements exist. Future work must not fabricate any.

## Product Principles

1. **Native state or nothing.** Every action lands in the provider's real format, or is honestly reported as unsupported.
2. **Verify the write, do not trust it.** Reload, digest-compare, roll back on mismatch.
3. **The source is sacred.** Migration and management never damage what the user already has.
4. **One keyboard, one screen.** The whole product is a single full-width list plus centered modals; direction keys map to source, session, and target.
5. **Local and quiet.** No account, no telemetry, no network requirement, and the private index never leaves the machine.

## Accessibility & Inclusion

- Terminal rendering must not depend on inherited background fills, which break under ANSI resets and leave black rectangles in Ghostty. Meaning is carried by text, position, and shape as well as color.
- CJK session titles are double-width. Layout must preserve enough columns for a user to recognize a session by its title; this is why the list is full-width rather than three columns.
- The README must stay legible at narrow mobile widths and in both GitHub light and dark color schemes.
