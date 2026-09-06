# OpenCode 2 title-policy adapter

This adapter makes OpenCode 2's first automatic session title follow another's
shared `MMDD｜Type｜Topic` policy without a second model call.

It does two things:

1. overrides the built-in hidden `title` agent so the existing title request
   returns `Type｜Topic` in the language selected by
   `~/.config/another/config.json`;
2. listens for that first native rename, prefixes the authoritative session
   creation date in `Asia/Shanghai`, validates the result, and writes it back
   through OpenCode 2's official `session.rename()` API.

Invalid output is left untouched. Existing sessions and manual renames are not
processed. This targets the beta plugin API version pinned in `package.json`.

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
