# another — Project Instructions

## Development Test Contract

- When Mingjian asks for “dev test” or “test first”, deliver the current changes through `adev` (`another-dev`), not only `./bin/another`. After the relevant automated checks pass, run `make install` to update the development installation.
- Verify that `another-dev` resolves to the installed development binary, its `--version` carries `+dev`, and it matches the build just tested. Confirm that `adev` points to `another-dev`; if interactive-shell verification is blocked, report that gap rather than claiming the shortcut was tested.
- Keep the released `another` and its `a` shortcut untouched while testing. Development testing alone does not authorize a tag, release, publication, or consumer upgrade; Mingjian's acceptance of the tested build does, through the delivery routine in `../AGENTS.md`.

## Cross-Platform and CI Contract

- Windows is a supported runtime, not merely a cross-compilation target. Consider Windows behavior in every filesystem, process, shell, and environment change, including test fixtures.
- Build native filesystem fixtures with `t.TempDir()` and `filepath.Join`. Do not use hard-coded Unix paths such as `/repo` or `/opt/agent-home` as portable absolute paths: on Windows they lack a drive. Keep literal platform-specific paths only in tests explicitly covering their semantics.
- Follow the production path contract in fixtures: normalize scope roots and candidate paths consistently before containment checks. Cover child paths, sibling-prefix collisions, and traversal outside the root; when parsing user paths, account for Windows drive letters and native separators. Use `filepath` for filesystem paths, not URL-style `path` operations.
- Treat symlinks, file permissions, process signals, executable lookup, and shell syntax as platform-dependent. Use native implementations or narrowly justified platform-specific tests; do not skip Windows coverage or weaken assertions just to make CI green.
- Before pushing, run relevant local tests. After an authorized push or PR update, use `gh pr checks` / `gh run view --log-failed` to inspect the checks for the exact submitted commit. Both CI jobs (`test` and `windows`), including their race tests, must pass before reporting the code ready to merge. Local macOS success and Windows cross-compilation do not prove Windows runtime correctness.
- For a CI repair, trace the failing assertion to its cause, fix it, and verify a new run on the corrected commit. Re-running an unchanged deterministic failure is not a fix. If native Windows verification is unavailable, state the gap explicitly rather than claiming Windows is fixed.
- Before merging a stale PR, incorporate current `main` where needed and verify the resulting merge candidate. After an authorized merge, check the CI run on the actual `main` commit as well. This verification contract does not itself authorize publication or changes to branch protection; publication follows Mingjian's acceptance under the delivery routine in `../AGENTS.md`.

## Release Contract

Treat implementation, publication, distribution, and maintainer installation as separate states. A change intended for delivery is not complete merely because code and tests pass.

For every release, update and verify the full release-surface matrix:

1. **GitHub source** — commit the exact intended files, push the default branch, and verify the remote commit.
2. **GitHub README** — keep `README.md` and `README.en.md` aligned with current behavior, installation, update, compatibility, and user-visible changes.
3. **GitHub Release** — create the version tag on the verified commit, publish release notes, wait for release automation, and verify downloadable artifacts and checksums.
4. **Homebrew** — verify the generated Cask update in the public `nxxxsooo/homebrew-tap`, then test the real consumer upgrade path.
5. **Local installation** — update Mingjian's installed `another` through its real installation source and verify both `which another` and `another --version`; a successful remote release does not imply the local binary changed.
6. **Project website** — the site is live at `https://mjshao.fun/another` (landing page `mjshao-portfolio/public/another/index.html`, case page `src/app/work/projects/another.mdx`, release post `src/app/blog/posts/another-migrate-coding-agent-sessions.mdx`). Update all three for every release, including the version string, then verify each through the deployed URL. The same site owns the documented install URLs: `mjshao-portfolio/next.config.mjs` redirects `https://mjshao.fun/another/install.sh` and its `install.ps1` sibling to the raw scripts on GitHub, and the READMEs and release header point there. Both redirects must keep resolving; `another update` deliberately fetches the raw GitHub URLs so self-update never depends on the site.

Writing about the project is not a release surface. A launch post, a WeChat article, or any other promotion is its own piece of work, published when there is something worth telling people about; a release does not owe one, and no draft is required to call a release complete.

There is no fixed release cadence. A user-visible change Mingjian accepts after dev testing ships in its own release under the delivery routine in `../AGENTS.md`: a patch for fixes, a minor for new behavior. Docs, tests, and chores with no user-visible effect wait on `main` for the next release.

Release notes are written for the person deciding whether to upgrade: features and fixes in their own words, grouped, with docs, chores, CI, tests, and merge commits filtered out. `.goreleaser.yaml` does the grouping and the install header; do not replace it with a raw commit list. Its `release.header` is a hand-written template, not a generated one: the `## Compatibility` section describes whichever release last edited it and will silently ship stale text otherwise. Rewrite that section for the version being tagged, before the tag, and check the published notes afterwards.

Before publishing, record the target version and all surfaces. Do not mix unexplained working-tree changes into a release. Do not report “released”, “published”, or “fully updated” until every applicable surface has been verified through its consumer-facing entry point; report partial success and blocked or unavailable surfaces explicitly.

## Provider Support Tiers

- **Continuously tested:** Pi, OpenCode, Claude Code, Codex, Antigravity, and Qwen Code. Include all six in every release regression plan.
- **Compatibility adapters:** CodeM is offered in setup alongside the tested six, in one flat list with no fold. Cursor, CommandCode, and Hermes stay registered (`registry.HiddenFromSetup`) so existing configurations keep working, but setup no longer offers them to new users. Preserve working adapters, but do not imply they receive end-to-end maintainer testing on every release.
- Setup must start with no providers selected on first run. Detection means “available”, never “chosen”; users explicitly select which agents another indexes and exposes.
- Session management is capability-driven. Show rename, archive, relocate, and delete only when the selected provider implements the corresponding native operation. Never emulate unsupported lifecycle actions with another-only state.
- Relocation is native or absent. Carrying a session into another directory uses that agent's own move or copy — currently OpenCode V2's `fork`/`move` endpoints and Pi's session file — never a re-render through the portable model, which would silently drop tool calls and reasoning. A same-provider migration is not a relocation.
