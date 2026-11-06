# OpenCode 2 title-policy adapter

This adapter makes OpenCode 2's first automatic session title follow another's
shared `MMDD｜Type｜Topic` policy, normally with one title model call.

It does four things:

1. overrides the built-in hidden `title` agent with another's language policy
   from `~/.config/another/config.json`;
2. uses the `title` hook to rewrite OpenCode's own title request so the model
   answers with the finished `MMDD｜Type｜Topic`, dated with the session's
   creation day in `Asia/Shanghai`; an answer that misses the policy is dated
   or corrected (up to three corrective retries), then retained with the
   creation date as a provisional title;
3. listens for manual `Type｜Topic` renames and dates them through the official
   `session.update()` API, and repairs unnamed or provisional titles when a
   turn settles;
4. keeps its own titles current on idle: on the first turn of a later day, or
   after three more user turns, it asks the title model whether the session
   has moved on, and dates the result with the latest activity.

The refresh follows the same rule as `another rename --if-stale`, which names
the other agents' sessions. The plugin records the last title it wrote in its
own storage (`ctx.storage`, key `title-mark:<session>`). A title that differs
from that record was set by someone else and is never refreshed; a policy
title with no record predates the feature and is adopted. The refresh prompt
weighs the latest user messages, attachment metadata, and recent assistant text,
and keeps the current `Type｜Topic` when the
model repeats it, so it still offers no refusal word. A kept topic on a later
day is only redated. Turns that meet neither condition make no model call.

The idle repair exists because OpenCode 2 asks for a title once, moments after the
first prompt, and never asks again. When that single request does not land —
the title provider fails, or the server restarts mid-session — the session
keeps no name at all, and nothing repairs it: OpenCode 2 lists it as
`New session - <timestamp>`, and another falls back to showing the raw prompt.
On a completed turn with an empty title the plugin waits five seconds in case the native
title is merely slow, then asks the session's model for one line, using the
opening user message and bounded supporting context. The answer goes through the same
policy and bounded corrective retries as an automatic one.

Fallbacks and retained rejected answers are explicitly **plugin-owned and
provisional**. The next settled turn retries them even with no additional user
messages. There is at most one recovery attempt per user turn and three attempts
per recovery budget; after exhaustion, a later day or three more user turns opens
a new budget. Marks survive reloads. A manual rename cancels recovery, including
policy-shaped manual titles. Previously stored generic fallback titles can also
recover; arbitrary unmarked invalid titles remain untouched.

The listener handles `session.idle` and terminal `session.execution.*` events:
OpenCode 2.2.5 delivered execution completion without idle events in live testing.
It waits for queued session work and any native title request before repair.
Events and session state must match the instance's directory and workspace;
foreign locations never make title-model calls. In-flight guards and persisted
turn marks coalesce completion/idle duplicates. A late answer is discarded after
a move, manual rename, or plugin disposal.

Prompts include at most three attachment names/MIME types/descriptions per user
message and the last two assistant text replies (1,000 characters each). They omit
attachment bytes and URLs, tool output, and reasoning. Screenshot-only openings
remain provisional until an assistant explanation is available, allowing a label
such as `Image 1` to become a meaningful topic at the end of the first turn.

English-category titles containing Han characters in their topic are rejected,
even when the category itself is English. Chinese titles may retain English
technical names. Validation is local and adds no model call for valid output.
Rejected output gets at most three correction rounds, stopping on the first
valid answer. If every correction is invalid or throws, the first rejected
answer is retained, not the last rejection or a generic replacement. An empty
or refused original answer, or a failed initial generation, uses the dated
`Explore｜Untitled session` / `探索｜未命名会话` fallback when possible.

The first title is OpenCode 2's own title request: the `title` hook replaces
its system and user message with the dated policy and the plugin's prompt, so
it runs once on the `title` agent's model and variant and usually lands final.
OpenCode stores that answer itself; the plugin learns it from the rename that
follows and renames again only when the answer misses the policy. OpenCode
stores it only if nobody renamed the session meanwhile, and the plugin tells the
answer from a person's rename by the usage OpenCode records just before it, or
else by the answer's `Type｜Topic` shape. A person's rename always wins.

Repairs, corrections, and refreshes call `session.generate` on the session
itself, which returns the answer without storing anything. The `generate` hook
strips that request to the title prompt alone — no agent instructions,
conversation history, or tool definitions — so it costs about what a title
should (around 1.5 KB). It routes like the session's own requests: the
session's model, through every session HTTP/WebSocket routing and credential
hook. No session is created. Standalone `generate.text` is not used: it
bypasses those hooks and can return 401 even when the same model works in
ordinary sessions.

If the title model rejects the low reasoning variant OpenCode picks for it, the
first attempt fails and the session's own model answers. Pin a variant to keep
the first title on the title model:

```jsonc
"agents": { "title": { "model": { "providerID": "openai", "model": "gpt-5-mini", "variant": "low" } } }
```

Generation attempts, successes, and failures are recorded as JSON lines at
`$XDG_STATE_HOME/another/opencode-title-policy.log`, defaulting to
`~/.local/state/another/opencode-title-policy.log`. Records identify the session,
location, and error type/message/status; credentials are redacted and
request/response payloads are excluded. The Promise plugin API has no server
logger, and console output alone is not reliably captured in `opencode.log`.

The first automatic title is learned from its rename event. If that event is
missed after a plugin reload, service restart, or subscription error, a dated
policy answer is adopted on the next idle; anything else stays as OpenCode
wrote it. Manual renames are dated when appropriate and stored as manually
owned. An event failure does not stop the listener from processing later renames.

Invalid manual output is left untouched, and a title that already carries a
date is not processed again, which stops manual renames from looping.
The prompt never offers the model a way to decline. OpenCode 2 has no sentinel
for "no title" — it writes the title agent's answer to the session verbatim —
so an earlier escape hatch named every unsummarizable session `KEEP`. Automatic
refusals now go through the same correction budget and provisional fallback.
The manual-rename listener still replaces a refusal with a dated generic title.
Child sessions keep the name of the task that spawned them. A manual rename
that happens to be exactly `Type｜Topic` is treated as policy output and gets
the date; write anything else to opt out. Either way it is the person's title,
not the plugin's, so idle refreshes leave it alone. This targets the OpenCode 2.2.5
plugin API version pinned in `package.json`.

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

To install the current working-tree plugin without replacing the released binary,
run from the repository root:

```bash
go run ./cmd/another integrations install opencode
go run ./cmd/another integrations status opencode
```

This installs the embedded source as a development plugin; it is not a release.
Verify installed `index.ts`, `policy.ts`, and `diagnostics.ts` against `src/`, and inspect OpenCode's
server log for plugin reload. A reload and matching files do not by themselves
prove a live model generation succeeded.

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
