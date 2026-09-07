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

## Install locally

Add the absolute path to `src/index.ts` to the global OpenCode 2 `plugins`
array, then restart the OpenCode 2 service. The plugin reads the title language
at startup; restart after changing it with `another setup`.
