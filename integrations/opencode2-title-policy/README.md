# OpenCode 2 title-policy adapter

This adapter makes OpenCode 2's first automatic session title follow another's
shared `MMDD｜Type｜Topic` policy without a second model call.

It does two things:

1. overrides the built-in hidden `title` agent so the existing title request
   returns `Type｜Topic` in the language selected by
   `~/.config/another/config.json`;
2. listens for the native rename that carries it, reads the session's own
   creation time, prefixes that date in `Asia/Shanghai`, validates the result,
   and writes it back through OpenCode 2's official `session.rename()` API.

Every rename is judged on its own title, with no state kept between events.
That is deliberate: the previous version remembered which sessions it was
waiting for, and silently dropped the date whenever the plugin reloaded, the
server restarted, or the session belonged to a directory other than the
instance that happened to be watching.

Invalid output is left untouched, and a title that already carries a date is
not processed again, which is what stops the rename it performs from looping.
The prompt never offers the model a way to decline. OpenCode 2 has no sentinel
for "no title" — it writes the title agent's answer to the session verbatim —
so an earlier escape hatch named every unsummarizable session `KEEP`. A refusal
that still arrives is replaced with the dated `Explore｜Untitled session` /
`探索｜未命名会话` rather than shown.
Child sessions keep the name of the task that spawned them. A manual rename
that happens to be exactly `Type｜Topic` is treated as policy output and gets
the date; write anything else to opt out. This targets the beta plugin API
version pinned in `package.json`.

## Develop

```bash
npm install --ignore-scripts
npm test
npm run typecheck
```

## Install

another ships this plugin inside its own binary and installs it on request:

```bash
another integrations install   # or turn the row on in `another setup`
another integrations status
```

The files land in `<opencode2 config dir>/plugins/another-title-policy/`, which
OpenCode 2 discovers on its own — no `plugins` entry in `opencode.json(c)`, and
another never edits that file. An entry left over from a hand installation is
harmless, and `another integrations status` points it out so it can be removed.

another resolves the configuration directory by asking the OpenCode 2 CLI where
its own configuration document is, because a machine that runs V1 and V2 side by
side keeps them apart with `OPENCODE_CONFIG_DIR`. Override it with
`--config-dir`, or with `ANOTHER_OPENCODE2_CONFIG_DIR` for every command.

A manifest at `.another-install.json` records the release and the file hashes
another wrote, so `another integrations status` can tell a pending upgrade from
a local edit. another refuses to overwrite files it did not write; `--force`
says otherwise. `another integrations remove` takes back only those files.

The plugin reads the title language when OpenCode 2 loads it. Changing that
language in `another setup` rewrites these files, which is what makes OpenCode 2
reload the plugin — no service restart.
