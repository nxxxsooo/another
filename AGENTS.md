# another — Project Instructions

## Release Contract

Treat implementation, publication, distribution, and maintainer installation as separate states. A change intended for delivery is not complete merely because code and tests pass.

For every release, update and verify the full release-surface matrix:

1. **GitHub source** — commit the exact intended files, push the default branch, and verify the remote commit.
2. **GitHub README** — keep `README.md` and `README.en.md` aligned with current behavior, installation, update, compatibility, and user-visible changes.
3. **GitHub Release** — create the version tag on the verified commit, publish release notes, wait for release automation, and verify downloadable artifacts and checksums.
4. **Homebrew** — verify the generated Cask update in the public `nxxxsooo/homebrew-tap`, then test the real consumer upgrade path.
5. **Local installation** — update Mingjian's installed `another` through its real installation source and verify both `which another` and `another --version`; a successful remote release does not imply the local binary changed.
6. **Project website** — the site is live at `https://mjshao.fun/another` (landing page `mjshao-portfolio/public/another/index.html`, case page `src/app/work/projects/another.mdx`, release post `src/app/blog/posts/another-migrate-coding-agent-sessions.mdx`). Update all three for every release, including the version string, then verify each through the deployed URL.
7. **WeChat Official Account** — prepare the matching release article and verify it in the draft box for every release. Public broadcast requires Mingjian's explicit confirmation.

Tag at most once per day. A release costs the user an install and costs the maintainer seven surfaces, so same-day follow-up tags mean the first one was published too early; hold the change on `main` and let the next day's tag carry it. The exception is a hotfix for a bug that loses data, corrupts an agent's state, or leaves the released binary unusable — those ship immediately and say so in the notes.

Release notes are written for the person deciding whether to upgrade: features and fixes in their own words, grouped, with docs, chores, CI, tests, and merge commits filtered out. `.goreleaser.yaml` does the grouping and the install header; do not replace it with a raw commit list.

Before publishing, record the target version and all surfaces. Do not mix unexplained working-tree changes into a release. Do not report “released”, “published”, or “fully updated” until every applicable surface has been verified through its consumer-facing entry point; report partial success and blocked or unavailable surfaces explicitly.

## Provider Support Tiers

- **Continuously tested:** Pi, OpenCode 2, Claude Code, Codex, Antigravity, and Qwen Code. Include all six in every release regression plan.
- **Compatibility adapters:** Cursor, OpenCode, CommandCode, and Hermes. Preserve working adapters, but do not imply they receive end-to-end maintainer testing on every release.
- Setup must start with no providers selected on first run. Detection means “available”, never “chosen”; users explicitly select which agents another indexes and exposes.
- Session management is capability-driven. Show rename, archive, relocate, and delete only when the selected provider implements the corresponding native operation. Never emulate unsupported lifecycle actions with another-only state.
- Relocation is native or absent. Carrying a session into another directory uses that agent's own move or copy — currently OpenCode 2's `fork`/`move` endpoints and Pi's session file — never a re-render through the portable model, which would silently drop tool calls and reasoning. A same-provider migration is not a relocation.
